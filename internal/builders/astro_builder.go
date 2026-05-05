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

type AstroBuilder struct {
	projectID string
}

func NewAstroBuilder() *AstroBuilder {
	return &AstroBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *AstroBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	cfAccountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	cfAPIToken := os.Getenv("CLOUDFLARE_API_TOKEN")

	if cfAccountID == "" || cfAPIToken == "" {
		return nil, fmt.Errorf("faltan credenciales de Cloudflare")
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	buildDir := config.AdvancedConfig["build_dir"]
	if buildDir == "" {
		buildDir = "dist" // Default de Astro
	}

	// 1. Generar nombre de proyecto para Cloudflare Pages
	userHash := strings.ToLower(config.UserID)
	if len(userHash) > 6 {
		userHash = userHash[:6]
	}
	safeSubDomain := config.SubDomain
	if len(safeSubDomain) > 35 {
		safeSubDomain = safeSubDomain[:35]
	}
	cfProjectName := fmt.Sprintf("nubbe-run-%s-%s", safeSubDomain, userHash)

	// 2. Asegurar existencia en CF (Upsert/409)
	if err := b.ensureCloudflareProject(ctx, cfAccountID, cfAPIToken, cfProjectName, branch); err != nil {
		return nil, fmt.Errorf("error asegurando proyecto en CF: %w", err)
	}

	// 3. Registrar en KV
	kvNamespaceID := os.Getenv("CLOUDFLARE_KV_NAMESPACE_ID")
	if kvNamespaceID != "" {
		errKV := b.registerRouteInKV(ctx, cfAccountID, cfAPIToken, kvNamespaceID, config.SubDomain, cfProjectName)
		if errKV != nil {
			fmt.Printf("[Advertencia KV] No se pudo registrar la ruta: %v\n", errKV)
		}
	}

	// 4. El Script de Construcción para Astro
	// Nótese que NO inyectamos _redirects. Astro genera sitios estáticos reales y su propio 404.html
	astroBuildScript := fmt.Sprintf(`
echo "=== Detectando Gestor de Paquetes ==="

if [ -f "bun.lockb" ]; then
    echo "Bun detectado. Instalando entorno..."
    npm install -g bun
    bun install
    bun run build

elif [ -f "pnpm-lock.yaml" ]; then
    echo "pnpm detectado. Activando corepack..."
    corepack enable pnpm
    pnpm install
    pnpm run build

elif [ -f "yarn.lock" ]; then
    echo "Yarn detectado. Activando corepack..."
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

echo "=== Verificando Archivos Esenciales ==="
# Astro compila por defecto en la carpeta configurada (ej. dist)
# Verificamos si el usuario generó su propio 404.html. Si no, Nubbe inyecta uno.
if [ ! -f "%s/404.html" ]; then
    echo "Archivo 404.html no encontrado en la salida de Astro."
    echo "Creando página 404 por defecto de Nubbe.run..."
    cat << 'EOF' > %s/404.html
<!DOCTYPE html><html><head><meta charset="UTF-8"><title>404 - No Encontrado</title><style>body{background-color:#111;color:#fff;font-family:system-ui,-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center;}h1{color:#67e8f9;margin-bottom:8px;}p{color:#9ca3af;}</style></head><body><div><h1>404</h1><p>Esta página no pudo ser encontrada.</p><p style="font-size:1rem;margin-top:24px;color:#4b5563;">Desplegado en <span style="color:#ffffff;font-weight:700;">nubbe<span style="color:#00e5ff;">.run</span></span></p></div></body></html>
EOF
fi

echo "=== Desplegando Astro a la red Edge ==="
npx --yes wrangler pages deploy %s --project-name $_SUB_DOMAIN --branch $_BRANCH
`, buildDir, buildDir, buildDir)

	// 5. Orquestar Cloud Build
	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("fallo inicializando cloudbuild: %w", err)
	}

	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs",
		Options: &cloudbuild.BuildOptions{
			SubstitutionOption: "ALLOW_LOOSE",
		},
		Substitutions: map[string]string{
			"_PROJECT_ID":   config.SubDomain,
			"_USER_ID":      config.UserID,
			"_PROJECT_TYPE": "astro", // Identificador clave para el webhook

			"_SUB_DOMAIN": cfProjectName,
			"_REPO_NAME":  config.RepoName,
			"_BRANCH":     branch,
			"_GH_TOKEN":   config.GithubToken,
		},
		Steps: []*cloudbuild.BuildStep{
			{
				Name:       "gcr.io/cloud-builders/git",
				Entrypoint: "bash",
				Args:       []string{"-c", "git clone --branch $_BRANCH https://x-access-token:$_GH_TOKEN@github.com/$_REPO_NAME.git ."},
			},
			{
				Name:       "node:20",
				Entrypoint: "bash",
				Args:       []string{"-c", astroBuildScript},
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

	// 6. Inyectar variables de entorno (Astro usa variables que empiezan con PUBLIC_ o astro:env)
	if len(config.EnvVars) > 0 {
		var userEnvs []string
		for k, v := range config.EnvVars {
			userEnvs = append(userEnvs, fmt.Sprintf("%s=%s", k, v))
		}
		buildObj.Steps[1].Env = append(buildObj.Steps[1].Env, userEnvs...)
	}

	// 7. Disparar Build
	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild (Astro): %w", err)
	}

	var buildMeta cloudbuild.BuildOperationMetadata
	json.Unmarshal(resp.Metadata, &buildMeta)

	result := &BuildResult{
		OperationName: resp.Name,
		Platform:      "cloudflare_pages",
	}
	if buildMeta.Build != nil {
		result.BuildID = buildMeta.Build.Id
		result.Status = buildMeta.Build.Status
	}

	return result, nil
}

// --------------------------------------------------------------------------------
// Funciones duplicadas (Temporalmente para compilación rápida).
// ¡Recuerda mover estas a un cloudflare_utils.go para compartir con React y Static!
// --------------------------------------------------------------------------------

func (b *AstroBuilder) ensureCloudflareProject(ctx context.Context, accountID, token, projectName, branch string) error {
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

func (b *AstroBuilder) registerRouteInKV(ctx context.Context, accountID, token, kvNamespaceID, subDomain, cfProjectName string) error {
	key := fmt.Sprintf("%s.nubbe.run", subDomain)
	value := fmt.Sprintf("https://%s.pages.dev", cfProjectName)
	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s", accountID, kvNamespaceID, key)

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
		return fmt.Errorf("KV API error (%d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}
