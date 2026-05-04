package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/api/cloudbuild/v1"
)

// ReactBuilder implementa el despliegue de aplicaciones React en Google Cloud Run.
type ReactBuilder struct {
	projectID string
}

func NewReactBuilder() *ReactBuilder {
	return &ReactBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

// Deploy orquesta el proceso de construcción y despliegue usando la API de Cloud Build.
func (b *ReactBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID no está configurado en el entorno")
	}

	// 1. Generar Dockerfile y configurar URLs
	dockerfile := b.getDockerfile()
	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", projectID, config.SubDomain)

	// 2. Preparar argumentos de construcción y ejecución
	dockerBuildArgs := []string{"build", "-t", imageURL}
	cloudRunDeployArgs := []string{
		"run", "deploy", config.SubDomain,
		"--image", imageURL,
		"--platform", "managed",
		"--region", "us-central1",
		"--allow-unauthenticated",
		"--port", "80",
	}

	// Inyectar variables de entorno si están presentes
	if len(config.EnvVars) > 0 {
		var runtimeVars []string
		for k, v := range config.EnvVars {
			// Argumentos para el tiempo de construcción (Docker)
			dockerBuildArgs = append(dockerBuildArgs, "--build-arg", fmt.Sprintf("%s=%s", k, v))
			// Variables para el tiempo de ejecución (Cloud Run)
			runtimeVars = append(runtimeVars, fmt.Sprintf("%s=%s", k, v))
		}
		dockerBuildArgs = append(dockerBuildArgs, ".")
		cloudRunDeployArgs = append(cloudRunDeployArgs, "--set-env-vars", strings.Join(runtimeVars, ","))
	} else {
		dockerBuildArgs = append(dockerBuildArgs, ".")
	}

	// 3. Definir pasos del pipeline de Cloud Build
	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs",
		Substitutions: map[string]string{
			"_PROJECT_ID":   config.SubDomain,
			"_USER_ID":      config.UserID,
			"_GITHUB_TOKEN": config.GithubToken,
			"_REPO_NAME":    config.RepoName,
		},
		Options: &cloudbuild.BuildOptions{
			SubstitutionOption: "ALLOW_LOOSE",
		},
		Steps: []*cloudbuild.BuildStep{
			{
				Name:       "gcr.io/cloud-builders/git",
				Entrypoint: "bash",
				Args:       []string{"-c", "git clone https://x-access-token:$_GITHUB_TOKEN@github.com/$_REPO_NAME.git ."},
			},
			{
				Name: "ubuntu",
				Args: []string{"bash", "-c", fmt.Sprintf("cat <<'EOF' > Dockerfile\n%s\nEOF", dockerfile)},
			},
			{
				Name: "gcr.io/cloud-builders/docker",
				Args: dockerBuildArgs,
			},
			{
				Name: "gcr.io/cloud-builders/docker",
				Args: []string{"push", imageURL},
			},
			{
				Name: "gcr.io/cloud-builders/gcloud",
				Args: cloudRunDeployArgs,
			},
		},
	}

	// 4. Ejecutar la llamada a la API de Cloud Build
	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("error al inicializar cliente de Cloud Build: %w", err)
	}

	resp, err := cbService.Projects.Builds.Create(projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("error al iniciar operación en Cloud Build: %w", err)
	}

	// 5. Procesar resultado inicial
	result := &BuildResult{
		OperationName: resp.Name,
		Status:        "QUEUED",
	}

	var buildMeta cloudbuild.BuildOperationMetadata
	if err := json.Unmarshal(resp.Metadata, &buildMeta); err == nil && buildMeta.Build != nil {
		result.BuildID = buildMeta.Build.Id
		result.Status = buildMeta.Build.Status
	}

	return result, nil
}

func (b *ReactBuilder) getDockerfile() string {
	// Dockerfile optimizado para aplicaciones React modernas
	return `FROM node:20-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]`
}
