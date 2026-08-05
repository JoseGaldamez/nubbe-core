package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/api/cloudbuild/v1"
)

// NextjsBuilder maneja el despliegue de proyectos Next.js (SSR) a Cloud Run.
type NextjsBuilder struct {
	projectID string
}

func NewNextjsBuilder() *NextjsBuilder {
	return &NextjsBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *NextjsBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	if b.projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID no está configurado")
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	port := config.AdvancedConfig["port"]
	if port == "" {
		port = "3000" // Default de Next.js
	}

	serviceName := strings.ToLower(config.SubDomain)
	if len(serviceName) > 40 {
		serviceName = serviceName[:40]
	}

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", b.projectID, serviceName)

	var envBuilder strings.Builder
	if len(config.EnvVars) > 0 {
		for k, v := range config.EnvVars {
			cleanVal := strings.ReplaceAll(v, "\"", "\\\"")
			envBuilder.WriteString(fmt.Sprintf("ENV %s=\"%s\"\n", k, cleanVal))
		}
	}

	nextjsSetupScript := fmt.Sprintf(`
echo "=== Preparando proyecto Next.js ==="

if [ -f "Dockerfile" ]; then
    echo "✅ Dockerfile detectado. Se usará tal cual."
else
    echo "⚠️ Dockerfile no encontrado. Generando Dockerfile para Next.js..."

    cat << 'DOCKERFILE' > Dockerfile
FROM node:20-alpine
WORKDIR /app

%s

COPY package*.json pnpm-lock.yaml* yarn.lock* bun.lock* bun.lockb* ./

RUN if [ -f pnpm-lock.yaml ]; then \
      npm install -g pnpm && pnpm config set approve-builds true && pnpm install --no-frozen-lockfile; \
    elif [ -f yarn.lock ]; then \
      yarn install; \
    elif [ -f bun.lock ] || [ -f bun.lockb ]; then \
      npm install -g bun && bun install; \
    else \
      npm install; \
    fi

COPY . .

ENV NEXT_TELEMETRY_DISABLED=1
ENV NODE_ENV=production
ENV PORT=%s

RUN if [ -f pnpm-lock.yaml ]; then \
      pnpm run build; \
    elif [ -f yarn.lock ]; then \
      yarn build; \
    elif [ -f bun.lock ] || [ -f bun.lockb ]; then \
      bun run build; \
    else \
      npm run build; \
    fi

EXPOSE %s

CMD ["sh", "-c", "if [ -f pnpm-lock.yaml ]; then pnpm start; elif [ -f yarn.lock ]; then yarn start; elif [ -f bun.lock ] || [ -f bun.lockb ]; then bun start; else npm start; fi"]
DOCKERFILE
    echo "✅ Dockerfile generado para Next.js."
fi
`, envBuilder.String(), port, port)

	kanikoArgs := []string{
		"--destination=" + imageURL,
		"--cache=true",
		"--cache-ttl=168h",
	}

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
			Name:       "ubuntu",
			Entrypoint: "bash",
			Args:       []string{"-c", nextjsSetupScript},
		},
		{
			Name:       "gcr.io/kaniko-project/executor:debug",
			Entrypoint: "/busybox/sh",
			Args:       []string{"-c", `/kaniko/executor ` + strings.Join(kanikoArgs, " ")},
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
			"_PROJECT_TYPE": "nextjs",
			"_BRANCH":       branch,
			"_REPO_NAME":    config.RepoName,
			"_GH_TOKEN":     config.GithubToken,
		},
		Steps: steps,
	}

	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild (Next.js): %w", err)
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
