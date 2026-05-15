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
// Next.js requiere un servidor Node.js para funcionar en modo SSR,
// por lo que se despliega como contenedor en Cloud Run.
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

	// Script que genera un Dockerfile dinámico para Next.js si no existe uno
	nextjsSetupScript := fmt.Sprintf(`
echo "=== Preparando Next.js para Cloud Run ==="

if [ -f "Dockerfile" ]; then
    echo "✅ Dockerfile detectado. Se usará tal cual."
else
    echo "⚠️ Dockerfile no encontrado. Generando Dockerfile optimizado para Next.js..."

    # Detectar gestor de paquetes para el Dockerfile
    PKG_MANAGER="npm"
    INSTALL_CMD="npm ci"
    if [ -f "pnpm-lock.yaml" ]; then
        PKG_MANAGER="pnpm"
        INSTALL_CMD="corepack enable pnpm && pnpm install --frozen-lockfile"
    elif [ -f "yarn.lock" ]; then
        PKG_MANAGER="yarn"
        INSTALL_CMD="corepack enable yarn && yarn install --frozen-lockfile"
    elif [ -f "bun.lockb" ]; then
        PKG_MANAGER="bun"
        INSTALL_CMD="npm install -g bun && bun install"
    fi

    cat << DOCKERFILE > Dockerfile
FROM node:22-alpine AS deps
WORKDIR /app
COPY package*.json pnpm-lock.yaml* yarn.lock* bun.lockb* ./
RUN $INSTALL_CMD

FROM node:22-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN $PKG_MANAGER run build 2>/dev/null || npx next build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=%s
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
COPY --from=builder /app/public ./public
EXPOSE %s
CMD ["node", "server.js"]
DOCKERFILE
    echo "✅ Dockerfile generado para Next.js standalone."
fi
`, port, port)

	// Argumentos para Kaniko (Build con Caché)
	kanikoArgs := []string{
		"--destination=" + imageURL,
		"--cache=true",
		"--cache-ttl=168h",
	}

	// Argumentos para Cloud Run (Deploy)
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
			Name:       "node:22",
			Entrypoint: "bash",
			Args:       []string{"-c", nextjsSetupScript},
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
