package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"google.golang.org/api/cloudbuild/v1"
)

type StaticBuilder struct {
	projectID string
}

func NewStaticBuilder() *StaticBuilder {
	return &StaticBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *StaticBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	cfAccountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	cfAPIToken := os.Getenv("CLOUDFLARE_API_TOKEN")

	if cfAccountID == "" || cfAPIToken == "" {
		return nil, fmt.Errorf("faltan credenciales de Cloudflare (ACCOUNT_ID/API_TOKEN)")
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	entryPoint := config.EntryPoint
	if entryPoint == "" {
		entryPoint = "./"
	}

	errorPage := config.AdvancedConfig["error_page"]

	// 1. Asegurar la existencia del proyecto en CF (Ligero y rápido en Go)
	if err := b.ensureCloudflareProject(ctx, cfAccountID, cfAPIToken, config.SubDomain, branch); err != nil {
		return nil, fmt.Errorf("error asegurando proyecto en Cloudflare: %w", err)
	}

	// 2. Preparar el script Bash que se ejecutará en Cloud Build para manejar el 404
	fallback404Html := `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>404 - No Encontrado</title><style>body{background-color:#111;color:#fff;font-family:system-ui,-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center;}h1{color:#67e8f9;margin-bottom:8px;}p{color:#9ca3af;}</style></head><body><div><h1>404</h1><p>Esta página no pudo ser encontrada.</p><p style="font-size:1rem;margin-top:24px;color:#4b5563;">Desplegado en <span style="color:#ffffff;font-weight:700;">nubbe<span style="color:#00e5ff;">.run</span></span></p></div></body></html>`

	bashScript404 := fmt.Sprintf(`
	if [ -n "%s" ] && [ -f "%s" ]; then
		echo "Moviendo error_page personalizado a 404.html..."
		mv "%s" "%s/404.html"
	elif [ ! -f "%s/404.html" ]; then
		echo "Generando 404.html de Nubbe.run..."
		cat << 'EOF' > "%s/404.html"
%s
EOF
	fi
	`, errorPage, errorPage, errorPage, entryPoint, entryPoint, entryPoint, fallback404Html)

	// 3. Orquestar el Job en Cloud Build
	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("fallo al inicializar servicio de cloudbuild: %w", err)
	}

	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs", // Asegúrate de que este bucket exista en tu GCP
		Substitutions: map[string]string{
			"_SUB_DOMAIN": config.SubDomain,
			"_REPO_NAME":  config.RepoName,
			"_BRANCH":     branch,
			"_ENTRY_PT":   entryPoint,
			"_GH_TOKEN":   config.GithubToken,
		},
		Steps: []*cloudbuild.BuildStep{
			// Paso 1: Clonar el repositorio
			{
				Name:       "gcr.io/cloud-builders/git",
				Entrypoint: "bash",
				Args:       []string{"-c", "git clone --branch $_BRANCH https://x-access-token:$_GH_TOKEN@github.com/$_REPO_NAME.git ."},
			},
			// Paso 2: Preparar la estructura y el archivo 404.html
			{
				Name: "ubuntu",
				Args: []string{"bash", "-c", bashScript404},
			},
			// Paso 3: Desplegar usando Wrangler en una imagen Node oficial
			{
				Name:       "node:20-slim",
				Entrypoint: "bash",
				Args:       []string{"-c", "npx --yes wrangler pages deploy $_ENTRY_PT --project-name $_SUB_DOMAIN --branch $_BRANCH"},
				Env: []string{
					"CLOUDFLARE_ACCOUNT_ID=" + cfAccountID,
					"CLOUDFLARE_API_TOKEN=" + cfAPIToken,
					"CI=true",
					"WRANGLER_SEND_METRICS=false",
				},
			},
		},
	}

	// 4. Disparar el Build
	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild: %w", err)
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

// ensureCloudflareProject verifica y crea el proyecto en CF para evitar el error 404 de Wrangler
func (b *StaticBuilder) ensureCloudflareProject(ctx context.Context, accountID, token, projectName, branch string) error {
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

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("respuesta inesperada API CF (%d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}
