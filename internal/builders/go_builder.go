package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/api/cloudbuild/v1"
)

// GoBuilder maneja el despliegue de proyectos Go nativos a Cloud Run.
// Compila el binario dentro de Cloud Build y lo empaqueta en una imagen distroless ligera.
type GoBuilder struct {
	projectID string
}

func NewGoBuilder() *GoBuilder {
	return &GoBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *GoBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
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

	entryPoint := config.EntryPoint
	if entryPoint == "" {
		entryPoint = "."
	}

	serviceName := strings.ToLower(config.SubDomain)
	if len(serviceName) > 40 {
		serviceName = serviceName[:40]
	}

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", b.projectID, serviceName)

	// Script que genera un Dockerfile optimizado para Go si no existe
	goSetupScript := fmt.Sprintf(`
echo "=== Preparando proyecto Go para Cloud Run ==="

if [ -f "Dockerfile" ]; then
    echo "✅ Dockerfile detectado. Se usará tal cual."
else
    echo "⚠️ Dockerfile no encontrado. Generando Dockerfile multi-stage para Go..."

    cat << 'DOCKERFILE' > Dockerfile
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copiar go.mod y go.sum primero para cachear dependencias
COPY go.mod go.sum* ./
RUN go mod download

# Copiar el resto del código
COPY . .

# Compilar binario estático
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server %s

# Imagen final ultra-ligera (solo el binario)
FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=builder /app/server /app/server

EXPOSE %s

ENTRYPOINT ["/app/server"]
DOCKERFILE
    echo "✅ Dockerfile multi-stage generado para Go."
fi
`, entryPoint, port)

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
			Name:       "golang:1.24-alpine",
			Entrypoint: "sh",
			Args:       []string{"-c", goSetupScript},
		},
		{
			Name: "gcr.io/kaniko-project/executor:latest",
			Args: kanikoArgs,
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
			"_PROJECT_TYPE": "go",
			"_BRANCH":       branch,
			"_REPO_NAME":    config.RepoName,
			"_GH_TOKEN":     config.GithubToken,
		},
		Steps: steps,
	}

	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild (Go): %w", err)
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
