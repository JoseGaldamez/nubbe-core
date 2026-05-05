package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/api/cloudbuild/v1"
)

type ReactBuilder struct {
	projectID string
}

func NewReactBuilder() *ReactBuilder {
	return &ReactBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *ReactBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	// Credenciales de Cloudflare
	cfAccountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	cfAPIToken := os.Getenv("CLOUDFLARE_API_TOKEN")

	if cfAccountID == "" || cfAPIToken == "" {
		return nil, fmt.Errorf("faltan credenciales de Cloudflare en las variables de entorno")
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	buildDir := config.AdvancedConfig["build_dir"]
	if buildDir == "" {
		buildDir = "dist" // Por defecto asumimos Vite. CRA usa 'build'
	}

	// 1. Generar nombre de proyecto indestructible para Cloudflare Pages
	userHash := strings.ToLower(config.UserID)
	if len(userHash) > 6 {
		userHash = userHash[:6]
	}
	safeSubDomain := config.SubDomain
	if len(safeSubDomain) > 35 {
		safeSubDomain = safeSubDomain[:35]
	}
	cfProjectName := fmt.Sprintf("nubbe-run-%s-%s", safeSubDomain, userHash)

	// 2. Asegurar la existencia del proyecto en CF (Soporta Upsert/409)
	if err := b.ensureCloudflareProject(ctx, cfAccountID, cfAPIToken, cfProjectName, branch); err != nil {
		return nil, fmt.Errorf("error asegurando proyecto en Cloudflare: %w", err)
	}

	// 3. Registrar en KV (URL pública del usuario -> URL interna de CF)
	kvNamespaceID := os.Getenv("CLOUDFLARE_KV_NAMESPACE_ID")
	if kvNamespaceID != "" {
		errKV := b.registerRouteInKV(ctx, cfAccountID, cfAPIToken, kvNamespaceID, config.SubDomain, cfProjectName)
		if errKV != nil {
			fmt.Printf("[Advertencia KV] No se pudo registrar la ruta: %v\n", errKV)
		}
	}

	// 4. El Script unificado con Auto-Detección de Gestor de Paquetes
	reactBuildScript := fmt.Sprintf(`
echo "=== Detectando Gestor de Paquetes ==="

if [ -f "bun.lockb" ]; then
    echo "Bun detectado (bun.lockb). Instalando entorno..."
    npm install -g bun
    bun install
    bun run build

elif [ -f "pnpm-lock.yaml" ]; then
    echo "pnpm detectado (pnpm-lock.yaml). Activando corepack..."
    corepack enable pnpm
    pnpm install
    pnpm run build

elif [ -f "yarn.lock" ]; then
    echo "Yarn detectado (yarn.lock). Activando corepack..."
    corepack enable yarn
    yarn install
    yarn build

else
    echo "NPM detectado por defecto."
    if [ -f "package-lock.json" ]; then
        npm ci
    else
        npm install
    fi
    npm run build
fi

echo "=== Configurando Enrutamiento SPA ==="
# Vital para que funcione React Router en Cloudflare Pages
echo "/* /index.html 200" > %s/_redirects

echo "=== Desplegando a la red Edge ==="
npx --yes wrangler pages deploy %s --project-name $_SUB_DOMAIN --branch $_BRANCH
`, buildDir, buildDir)

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
			// Webhook variables
			"_PROJECT_ID":   config.SubDomain, // Se mantiene el nombre original para tu BD
			"_USER_ID":      config.UserID,
			"_PROJECT_TYPE": "react",

			// Variables para el script Bash
			"_SUB_DOMAIN": cfProjectName, // Wrangler usa el nombre técnico
			"_REPO_NAME":  config.RepoName,
			"_BRANCH":     branch,
			"_GH_TOKEN":   config.GithubToken,
		},
		Steps: []*cloudbuild.BuildStep{
			// Paso 1: Clonar el repositorio
			{
				Name:       "gcr.io/cloud-builders/git",
				Entrypoint: "bash",
				Args:       []string{"-c", "git clone --branch $_BRANCH https://x-access-token:$_GH_TOKEN@github.com/$_REPO_NAME.git ."},
			},
			// Paso 2: Ejecutar el script dinámico
			{
				Name:       "node:20",
				Entrypoint: "bash",
				Args:       []string{"-c", reactBuildScript},
				Env: []string{
					"CLOUDFLARE_ACCOUNT_ID=" + cfAccountID,
					"CLOUDFLARE_API_TOKEN=" + cfAPIToken,
					"CI=true",
					"WRANGLER_SEND_METRICS=false",
					"IGNORE_PROJECT=$_PROJECT_ID",
					"IGNORE_USER=$_USER_ID",
					"IGNORE_TYPE=$_PROJECT_TYPE",
				},
			},
		},
	}

	// 6. Inyectar variables de entorno del usuario al proceso de Node
	if len(config.EnvVars) > 0 {
		var userEnvs []string
		for k, v := range config.EnvVars {
			userEnvs = append(userEnvs, fmt.Sprintf("%s=%s", k, v))
		}
		buildObj.Steps[1].Env = append(buildObj.Steps[1].Env, userEnvs...)
	}

	// 7. Disparar el Build en Google Cloud
	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild: %w", err)
	}

	var buildMeta cloudbuild.BuildOperationMetadata
	json.Unmarshal(resp.Metadata, &buildMeta)

	result := &BuildResult{
		OperationName: resp.Name,
		Platform:      "cloudflare_pages", // Identificador para tu frontend
	}
	if buildMeta.Build != nil {
		result.BuildID = buildMeta.Build.Id
		result.Status = buildMeta.Build.Status
	}

	return result, nil
}

// =====================================================================
// FUNCIONES AUXILIARES (Cloudflare)
// =====================================================================

// ensureCloudflareProject verifica y crea el proyecto en CF para evitar el error 404
func (b *ReactBuilder) ensureCloudflareProject(ctx context.Context, accountID, token, projectName, branch string) error {
	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects", accountID)

	payload := fmt.Sprintf(`{"name": "%s", "production_branch": "%s"}`, projectName, branch)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusConflict {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("respuesta inesperada API CF (%d): %s", resp.StatusCode, string(respBody))
}

// registerRouteInKV guarda el mapeo del subdominio hacia el proyecto en KV
func (b *ReactBuilder) registerRouteInKV(ctx context.Context, accountID, token, kvNamespaceID, subDomain, cfProjectName string) error {
	key := fmt.Sprintf("%s.nubbe.run", subDomain)
	value := fmt.Sprintf("https://%s.pages.dev", cfProjectName)

	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		accountID, kvNamespaceID, key)

	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(value))
	if err != nil {
		return fmt.Errorf("error creando petición KV: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error ejecutando petición KV: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Cloudflare KV API respondió con error (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}
