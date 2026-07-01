package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/api/cloudbuild/v1"
)

// PythonBuilder maneja el despliegue de proyectos Python (FastAPI, Django, Flask, Streamlit)
// a Cloud Run. Genera un Dockerfile dinámico si no existe uno.
type PythonBuilder struct {
	projectID string
}

func NewPythonBuilder() *PythonBuilder {
	return &PythonBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *PythonBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	if b.projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID no está configurado")
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	port := config.AdvancedConfig["port"]
	if port == "" {
		port = "8080"
	}

	// Permitir al usuario especificar un archivo de dependencias personalizado
	depsFile := config.AdvancedConfig["deps_file"]
	if depsFile == "" {
		depsFile = "requirements.txt"
	}

	// Comando de inicio personalizable
	startCmd := config.AdvancedConfig["start_command"]
	if startCmd == "" {
		startCmd = config.EntryPoint
	}

	if config.ProjectType == "streamlit" {
		// Si es Streamlit, nos aseguramos de que corra con streamlit run y los flags adecuados para Cloud Run
		if startCmd == "" {
			startCmd = "app.py" // fallback default
		}
		// Si el usuario especificó solo el archivo (ej: "market.py" o "app.py"), le agregamos el comando streamlit run y los flags necesarios
		if !strings.HasPrefix(startCmd, "streamlit run") {
			startCmd = fmt.Sprintf("streamlit run %s --server.port %s --server.address 0.0.0.0 --server.enableCORS=false --server.enableWebsocketCompression=false --server.enableXsrfProtection=false", startCmd, port)
		} else {
			// Si ya tiene "streamlit run", nos aseguramos de agregar los flags necesarios si no están presentes
			if !strings.Contains(startCmd, "--server.port") {
				startCmd = fmt.Sprintf("%s --server.port %s", startCmd, port)
			}
			if !strings.Contains(startCmd, "--server.address") {
				startCmd = fmt.Sprintf("%s --server.address 0.0.0.0", startCmd)
			}
			if !strings.Contains(startCmd, "--server.enableCORS") {
				startCmd = fmt.Sprintf("%s --server.enableCORS=false", startCmd)
			}
			if !strings.Contains(startCmd, "--server.enableWebsocketCompression") {
				startCmd = fmt.Sprintf("%s --server.enableWebsocketCompression=false", startCmd)
			}
			if !strings.Contains(startCmd, "--server.enableXsrfProtection") {
				startCmd = fmt.Sprintf("%s --server.enableXsrfProtection=false", startCmd)
			}
		}
	} else {
		// Comportamiento por defecto para otros proyectos Python
		if startCmd == "" {
			startCmd = fmt.Sprintf("python -m uvicorn app:app --host 0.0.0.0 --port %s", port)
		}
	}

	serviceName := strings.ToLower(config.SubDomain)
	if len(serviceName) > 40 {
		serviceName = serviceName[:40]
	}

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", b.projectID, serviceName)

	// Script que genera un Dockerfile dinámico para Python si no existe
	pythonSetupScript := fmt.Sprintf(`
echo "=== Preparando proyecto Python para Cloud Run ==="

if [ -f "Dockerfile" ]; then
    echo "✅ Dockerfile detectado. Se usará tal cual."
else
    echo "⚠️ Dockerfile no encontrado. Generando Dockerfile para Python..."

    # Detectar archivo de dependencias
    DEPS_FILE="%s"
    INSTALL_CMD=""

    if [ -f "pyproject.toml" ]; then
        echo "pyproject.toml detectado."
        INSTALL_CMD="pip install ."
    elif [ -f "Pipfile" ]; then
        echo "Pipfile detectado."
        INSTALL_CMD="pip install pipenv && pipenv install --system --deploy"
    elif [ -f "$$DEPS_FILE" ]; then
        echo "$$DEPS_FILE detectado."
        INSTALL_CMD="pip install --no-cache-dir -r $$DEPS_FILE"
    else
        echo "⚠️ No se encontró archivo de dependencias. Se intentará instalar sin dependencias."
        INSTALL_CMD="echo 'Sin dependencias detectadas'"
    fi

    cat << DOCKERFILE > Dockerfile
FROM python:3.12-slim

WORKDIR /app

COPY . .

RUN $$INSTALL_CMD

ENV PORT=%s

EXPOSE %s

CMD ["/bin/sh", "-c", "%s"]
DOCKERFILE
    echo "✅ Dockerfile generado para Python."
fi
`, depsFile, port, port, startCmd)

	// Argumentos para Kaniko
	kanikoArgs := []string{
		"--destination=" + imageURL,
		"--cache=true",
		"--cache-ttl=168h",
	}

	// Argumentos para Cloud Run
	cloudRunArgs := []string{
		"run", "deploy", serviceName,
		"--image", imageURL,
		"--platform", "managed",
		"--region", "us-central1",
		"--allow-unauthenticated",
		"--port", port,
	}

	if len(config.EnvVars) > 0 {
		var runtimeVars []string
		for k, v := range config.EnvVars {
			runtimeVars = append(runtimeVars, fmt.Sprintf("%s=%s", k, v))
		}
		cloudRunArgs = append(cloudRunArgs, "--set-env-vars", strings.Join(runtimeVars, ","))
	}

	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("fallo al inicializar servicio de cloudbuild: %w", err)
	}

	steps := []*cloudbuild.BuildStep{
		{
			Name:       "gcr.io/cloud-builders/git",
			Entrypoint: "bash",
			Args:       []string{"-c", "git clone --branch $_BRANCH https://x-access-token:$_GH_TOKEN@github.com/$_REPO_NAME.git ."},
		},
		{
			Name:       "python:3.12-slim",
			Entrypoint: "bash",
			Args:       []string{"-c", pythonSetupScript},
		},
		{
			Name:       "gcr.io/kaniko-project/executor:debug",
			Entrypoint: "/busybox/sh",
			Args:       []string{"-c", `if [ -f "Dockerfile" ]; then /kaniko/executor ` + strings.Join(kanikoArgs, " ") + `; else echo "Skipping Kaniko"; fi`},
		},
		{
			Name:       "gcr.io/google.com/cloudsdktool/cloud-sdk:latest",
			Entrypoint: "bash",
			Args:       []string{"-c", `if [ ! -f "Dockerfile" ]; then gcloud alpha builds submit --pack image=` + imageURL + ` --location=us-central1 --quiet; else echo "Skipping Buildpacks"; fi`},
		},
		{
			Name: "gcr.io/cloud-builders/gcloud",
			Args: cloudRunArgs,
		},
	}

	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs",
		Options: &cloudbuild.BuildOptions{
			SubstitutionOption: "ALLOW_LOOSE",
		},
		Substitutions: map[string]string{
			"_PROJECT_ID":   config.SubDomain,
			"_USER_ID":      config.UserID,
			"_PROJECT_TYPE": "python",
			"_BRANCH":       branch,
			"_REPO_NAME":    config.RepoName,
			"_GH_TOKEN":     config.GithubToken,
		},
		Steps: steps,
	}

	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild (Python): %w", err)
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
