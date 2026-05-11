package builders

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/api/cloudbuild/v1"
)

type NodejsBuilder struct {
	projectID string
}

func NewNodejsBuilder() *NodejsBuilder {
	return &NodejsBuilder{
		projectID: os.Getenv("GCP_PROJECT_ID"),
	}
}

func (b *NodejsBuilder) Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error) {
	if b.projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID no está configurado")
	}

	branch := config.AdvancedConfig["branch"]
	if branch == "" {
		branch = "main"
	}

	startCmd := config.AdvancedConfig["start_command"]
	if startCmd == "" {
		startCmd = "npm start" // Default estándar de Node
	}

	port := config.AdvancedConfig["port"]
	if port == "" {
		port = "8080" // Default de Cloud Run
	}

	// 1. Identificadores y URLs para GCP
	serviceName := strings.ToLower(config.SubDomain)
	if len(serviceName) > 40 {
		serviceName = serviceName[:40]
	}

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", b.projectID, serviceName)

	// 2. Argumentos para Kaniko (Build con Caché)
	kanikoArgs := []string{
		"--destination=" + imageURL,
		"--cache=true",
		"--cache-ttl=168h",
	}

	// 3. Argumentos para Cloud Run (Deploy)
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

	// 4. Orquestar el Job en Cloud Build
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
			Args:       []string{"-c", `if [ -f "Dockerfile" ]; then echo "✅ Dockerfile detectado."; else echo "⚠️ Dockerfile no encontrado. Usando Buildpacks."; fi`},
		},
		{
			Name:       "gcr.io/kaniko-project/executor:latest",
			Entrypoint: "bash",
			Args:       []string{"-c", `if [ -f "Dockerfile" ]; then /kaniko/executor ` + strings.Join(kanikoArgs, " ") + `; else echo "Skipping Kaniko"; fi`},
		},
		{
			Name:       "gcr.io/google.com/cloudsdktool/cloud-sdk:latest",
			Entrypoint: "bash",
			Args:       []string{"-c", `if [ ! -f "Dockerfile" ]; then gcloud alpha builds submit --pack image=` + imageURL + ` --location=us-central1 --quiet; else echo "Skipping Buildpacks"; fi`},
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
			"_PROJECT_TYPE": "nodejs",
			"_BRANCH":       branch,
			"_REPO_NAME":    config.RepoName,
			"_GH_TOKEN":     config.GithubToken,
		},
		Steps: steps,
	}

	// 5. Disparar el Build
	resp, err := cbService.Projects.Builds.Create(b.projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("fallo al disparar cloudbuild (Node.js): %w", err)
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
