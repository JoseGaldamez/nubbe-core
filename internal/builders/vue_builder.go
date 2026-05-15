package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"google.golang.org/api/cloudbuild/v1"
)

// VueBuilder maneja el despliegue de proyectos Vue.js a Cloudflare Pages.
type VueBuilder struct {
	projectID string
}

func NewVueBuilder() *VueBuilder {
	return &VueBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *VueBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
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
		buildDir = "dist" // Vue CLI y Vite generan dist por defecto
	}

	cfProjectName := GenerateCFProjectName(config.SubDomain, config.UserID)

	if err := EnsureCloudflareProject(ctx, cfAccountID, cfAPIToken, cfProjectName, branch); err != nil {
		return nil, fmt.Errorf("error asegurando proyecto en Cloudflare: %w", err)
	}

	if kvNamespaceID != "" {
		if errKV := RegisterRouteInKV(ctx, cfAccountID, cfAPIToken, kvNamespaceID, config.SubDomain, cfProjectName); errKV != nil {
			fmt.Printf("[Advertencia KV] No se pudo registrar la ruta: %v\n", errKV)
		}
	}

	// Script: detectar gestor de paquetes, compilar, inyectar SPA redirect y desplegar
	vueBuildScript := PackageManagerDetectionScript() + fmt.Sprintf(`
echo "=== Configurando Enrutamiento SPA para Vue Router ==="
echo "/* /index.html 200" > %s/_redirects

echo "=== Desplegando Vue a la red Edge ==="
npx --yes wrangler pages deploy %s --project-name $_SUB_DOMAIN --branch $_BRANCH
`, buildDir, buildDir)

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
			"_PROJECT_TYPE": "vue",
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
				Name:       "node:22",
				Entrypoint: "bash",
				Args:       []string{"-c", vueBuildScript},
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

	if len(config.EnvVars) > 0 {
		var userEnvs []string
		for k, v := range config.EnvVars {
			userEnvs = append(userEnvs, fmt.Sprintf("%s=%s", k, v))
		}
		buildObj.Steps[1].Env = append(buildObj.Steps[1].Env, userEnvs...)
	}

	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild (Vue): %w", err)
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
