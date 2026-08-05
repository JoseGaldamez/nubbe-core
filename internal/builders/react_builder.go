package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
	cfAccountID, cfAPIToken, kvNamespaceID, err := GetCloudflareCredentials()
	if err != nil {
		return nil, err
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	buildDir := config.AdvancedConfig["build_dir"]
	if buildDir == "" {
		buildDir = "dist" // Por defecto asumimos Vite. CRA usa 'build'
	}

	// 1. Generar nombre de proyecto para Cloudflare Pages
	cfProjectName := GenerateCFProjectName(config.SubDomain, config.UserID)

	// 2. Asegurar la existencia del proyecto en CF (Soporta Upsert/409)
	if err := EnsureCloudflareProject(ctx, cfAccountID, cfAPIToken, cfProjectName, branch); err != nil {
		return nil, fmt.Errorf("error asegurando proyecto en Cloudflare: %w", err)
	}

	// 3. Registrar en KV (URL pública del usuario -> URL interna de CF)
	if kvNamespaceID != "" {
		errKV := RegisterRouteInKV(ctx, cfAccountID, cfAPIToken, kvNamespaceID, config.SubDomain, cfProjectName)
		if errKV != nil {
			fmt.Printf("[Advertencia KV] No se pudo registrar la ruta: %v\n", errKV)
		}
	}

	// 4. El Script unificado con Auto-Detección de Gestor de Paquetes
	reactBuildScript := PackageManagerDetectionScript() + fmt.Sprintf(`
echo "=== Configurando Enrutamiento SPA ==="
# Vital para que funcione React Router en Cloudflare Pages
mkdir -p %s
echo "/* /index.html 200" > %s/_redirects

echo "=== Desplegando a la red Edge ==="
npx --yes wrangler pages deploy %s --project-name $_SUB_DOMAIN --branch $_BRANCH
`, buildDir, buildDir, buildDir)


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
			"_PROJECT_ID":   config.SubDomain,
			"_USER_ID":      config.UserID,
			"_PROJECT_TYPE": "react",
			"_SUB_DOMAIN":   cfProjectName,
			"_REPO_NAME":    config.RepoName,
			"_BRANCH":       branch,
			"_GH_TOKEN":     config.GithubToken,
		},
		Steps: []*cloudbuild.BuildStep{
			{
				Name:       "gcr.io/cloud-builders/git",
				Entrypoint: "bash",
				Args:       []string{"-c", "git clone --branch $_BRANCH https://x-access-token:$_GH_TOKEN@github.com/$_REPO_NAME.git ."},
			},
			{
				Name:       "node:lts",
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
		Platform:      "cloudflare_pages",
	}
	if buildMeta.Build != nil {
		result.BuildID = buildMeta.Build.Id
		result.Status = buildMeta.Build.Status
	}

	return result, nil
}
