package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
	cfAccountID, cfAPIToken, kvNamespaceID, err := GetCloudflareCredentials()
	if err != nil {
		return nil, err
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

	// 1. Generar nombre de proyecto para Cloudflare Pages
	cfProjectName := GenerateCFProjectName(config.SubDomain, config.UserID)

	// 2. Asegurar la existencia del proyecto en CF
	if err := EnsureCloudflareProject(ctx, cfAccountID, cfAPIToken, cfProjectName, branch); err != nil {
		return nil, fmt.Errorf("error asegurando proyecto en Cloudflare: %w", err)
	}

	// 3. Registrar en KV
	if kvNamespaceID != "" {
		errKV := RegisterRouteInKV(ctx, cfAccountID, cfAPIToken, kvNamespaceID, config.SubDomain, cfProjectName)
		if errKV != nil {
			fmt.Printf("[Advertencia KV] No se pudo registrar la ruta: %v\n", errKV)
		}
	}

	// 4. Preparar el script Bash para manejar el 404
	bashScript404 := fmt.Sprintf(`
	if [ -n "%[1]s" ] && [ -f "%[1]s" ]; then
		if [ ! "%[1]s" -ef "%[2]s/404.html" ]; then
			echo "Moviendo error_page personalizado a 404.html..."
			mv "%[1]s" "%[2]s/404.html"
		fi
	elif [ ! -f "%[2]s/404.html" ]; then
		echo "Generando 404.html de Nubbe.run..."
		cat << 'EOF' > "%[2]s/404.html"
%[3]s
EOF
	fi
	`, errorPage, entryPoint, Nubbe404HTML())

	// 5. Orquestar el Job en Cloud Build
	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("fallo al inicializar servicio de cloudbuild: %w", err)
	}

	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs",
		Substitutions: map[string]string{
			"_PROJECT_ID":   config.SubDomain,
			"_USER_ID":      config.UserID,
			"_PROJECT_TYPE": "static",
			"_SUB_DOMAIN":   cfProjectName,
			"_REPO_NAME":    config.RepoName,
			"_BRANCH":       branch,
			"_ENTRY_PT":     entryPoint,
			"_GH_TOKEN":     config.GithubToken,
		},
		Steps: []*cloudbuild.BuildStep{
			{
				Name:       "gcr.io/cloud-builders/git",
				Entrypoint: "bash",
				Args:       []string{"-c", "git clone --branch $_BRANCH https://x-access-token:$_GH_TOKEN@github.com/$_REPO_NAME.git ."},
			},
			{
				Name: "ubuntu",
				Args: []string{"bash", "-c", bashScript404},
				Env: []string{
					"IGNORE_PROJECT=$_PROJECT_ID",
					"IGNORE_USER=$_USER_ID",
					"IGNORE_TYPE=$_PROJECT_TYPE",
				},
			},
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

	// 6. Disparar el Build
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
