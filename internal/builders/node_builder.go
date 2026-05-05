package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/api/cloudbuild/v1"
)

type NodejsBuilder struct {
	projectID string
}

func NewNodejsBuilder() *NodejsBuilder {
	return &NodejsBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *NodejsBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	if b.projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID no está configurado")
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	startCmd := config.AdvancedConfig["start_command"]
	if startCmd == "" {
		startCmd = "npm start" // Default estándar de Node
	}

	port := config.AdvancedConfig["port"]
	if port == "" {
		port = "8080" // Default de Cloud Run
	}

	// 1. Identificadores y URLs para GCP
	// El nombre del servicio en Cloud Run debe ser en minúsculas y sin caracteres raros
	serviceName := strings.ToLower(config.SubDomain)
	if len(serviceName) > 40 {
		serviceName = serviceName[:40]
	}

	// Asegúrate de tener un repositorio en Artifact Registry llamado "nubbe-repo"
	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", b.projectID, serviceName)

	// 2. Argumentos para Kaniko (Build con Caché)
	kanikoArgs := []string{
		"--destination=" + imageURL,
		"--cache=true",
		"--cache-ttl=168h", // Mantiene el caché por 7 días
	}

	// 3. Argumentos para Cloud Run (Deploy)
	cloudRunArgs := []string{
		"run", "deploy", serviceName,
		"--image", imageURL,
		"--platform", "managed",
		"--region", "us-central1", // O la región que prefieras
		"--allow-unauthenticated",
		"--port", port,
	}

	// Inyectar variables de entorno a Cloud Run
	if len(config.EnvVars) > 0 {
		var runtimeVars []string
		for k, v := range config.EnvVars {
			runtimeVars = append(runtimeVars, fmt.Sprintf("%s=%s", k, v))
		}
		cloudRunArgs = append(cloudRunArgs, "--set-env-vars", strings.Join(runtimeVars, ","))
	}

	// 4. Script para generar un Dockerfile dinámico si no existe
	// Convertimos el string "npm start" en el formato JSON array que requiere Docker CMD: ["npm", "start"]
	cmdParts := strings.Fields(startCmd)
	cmdJSON, _ := json.Marshal(cmdParts)

	dockerfileGenerator := fmt.Sprintf(`
if [ ! -f "Dockerfile" ]; then
    echo "⚠️ Dockerfile no encontrado en el repositorio."
    echo "🛠️ Generando Dockerfile dinámico para Node.js (Nubbe Auto-Build)..."
    
    cat << 'EOF' > Dockerfile
FROM node:20-alpine
WORKDIR /app

# Copiamos archivos de dependencias primero para optimizar el caché de Docker
COPY package*.json ./
COPY yarn.lock* ./
COPY pnpm-lock.yaml* ./
COPY bun.lockb* ./

# Autodetección de gestor de paquetes para instalación
RUN if [ -f "yarn.lock" ]; then \
      corepack enable yarn && yarn install --frozen-lockfile; \
    elif [ -f "pnpm-lock.yaml" ]; then \
      corepack enable pnpm && pnpm install --frozen-lockfile; \
    elif [ -f "bun.lockb" ]; then \
      npm install -g bun && bun install; \
    else \
      npm install; \
    fi

# Copiamos el resto del código
COPY . .

# Exponemos el puerto configurado
EXPOSE %s
ENV PORT=%s

# Comando de inicio configurado por el usuario
CMD %s
EOF
    echo "✅ Dockerfile generado exitosamente."
else
    echo "✅ Dockerfile personalizado detectado. Usando configuración del usuario."
fi
`, port, port, string(cmdJSON))

	// 5. Orquestar el Job en Cloud Build
	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("fallo al inicializar servicio de cloudbuild: %w", err)
	}

	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs",
		Options: &cloudbuild.BuildOptions{
			SubstitutionOption: "ALLOW_LOOSE",
		},
		Substitutions: map[string]string{
			// Para el Webhook (Mantiene consistencia con tu sistema actual)
			"_PROJECT_ID":   config.SubDomain,
			"_USER_ID":      config.UserID,
			"_PROJECT_TYPE": "nodejs",

			"_BRANCH":    branch,
			"_REPO_NAME": config.RepoName,
			"_GH_TOKEN":  config.GithubToken,
		},
		Steps: []*cloudbuild.BuildStep{
			// Paso 1: Clonar el repositorio
			{
				Name:       "gcr.io/cloud-builders/git",
				Entrypoint: "bash",
				Args:       []string{"-c", "git clone --branch $_BRANCH https://x-access-token:$_GH_TOKEN@github.com/$_REPO_NAME.git ."},
			},
			// Paso 2: Generar Dockerfile si hace falta
			{
				Name:       "ubuntu",
				Entrypoint: "bash",
				Args:       []string{"-c", dockerfileGenerator},
				Env: []string{
					"IGNORE_PROJECT=$_PROJECT_ID",
					"IGNORE_USER=$_USER_ID",
					"IGNORE_TYPE=$_PROJECT_TYPE",
				},
			},
			// Paso 3: Construir imagen con Kaniko (Caché optimizado)
			{
				Name: "gcr.io/kaniko-project/executor:latest",
				Args: kanikoArgs,
			},
			// Paso 4: Desplegar en Cloud Run
			{
				Name: "gcr.io/cloud-builders/gcloud",
				Args: cloudRunArgs,
			},
		},
	}

	// 6. Disparar el Build
	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild (Node.js): %w", err)
	}

	var buildMeta cloudbuild.BuildOperationMetadata
	json.Unmarshal(resp.Metadata, &buildMeta)

	result := &BuildResult{
		OperationName: resp.Name,
		Platform:      "gcp_cloudrun",
	}
	if buildMeta.Build != nil {
		result.BuildID = buildMeta.Build.Id
		result.Status = buildMeta.Build.Status
	}

	return result, nil
}
