package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/JoseGaldamez/nubbe-core/internal/builders"
	"google.golang.org/api/cloudbuild/v1"
)

type BuildInfo struct {
	BuildID       string
	Status        string
	OperationName string
}

type BuildService interface {
	TriggerBuild(ctx context.Context, userID, subDomain, repoName, projectType, githubToken, entryPoint string, envVars map[string]string) (*BuildInfo, error)
}

type googleBuildService struct{}

func NewBuildService() BuildService {
	return &googleBuildService{}
}

func (s *googleBuildService) TriggerBuild(ctx context.Context, userID, subDomain, repoName, projectType, githubToken, entryPoint string, envVars map[string]string) (*BuildInfo, error) {
	projectID := os.Getenv("GCP_PROJECT_ID")

	builder, err := builders.GetBuilder(projectType)
	if err != nil {
		return nil, fmt.Errorf("failed to get builder for %s: %w", projectType, err)
	}
	dynamicDockerfile := builder.GetDockerfile(entryPoint)

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", projectID, subDomain)

	// Preparamos argumentos para Docker Build y Cloud Run
	dockerBuildArgs := []string{"build", "-t", imageURL}
	cloudRunDeployArgs := []string{"run", "deploy", subDomain, "--image", imageURL, "--platform", "managed", "--region", "us-central1", "--allow-unauthenticated", "--port", "80"}

	// Inyectamos variables de entorno si existen
	if len(envVars) > 0 {
		var runtimeVars []string
		for k, v := range envVars {
			// Para Docker (Build time)
			dockerBuildArgs = append(dockerBuildArgs, "--build-arg", fmt.Sprintf("%s=%s", k, v))
			// Para Cloud Run (Runtime)
			runtimeVars = append(runtimeVars, fmt.Sprintf("%s=%s", k, v))
		}
		dockerBuildArgs = append(dockerBuildArgs, ".")
		cloudRunDeployArgs = append(cloudRunDeployArgs, "--set-env-vars", strings.Join(runtimeVars, ","))
	} else {
		dockerBuildArgs = append(dockerBuildArgs, ".")
	}

	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs",
		Substitutions: map[string]string{
			"_PROJECT_ID":   subDomain,
			"_USER_ID":      userID,
			"_GITHUB_TOKEN": githubToken,
			"_REPO_NAME":    repoName,
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
				Args: []string{"bash", "-c", fmt.Sprintf("echo '%s' > Dockerfile", dynamicDockerfile)},
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

	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloudbuild service: %w", err)
	}

	resp, err := cbService.Projects.Builds.Create(projectID, buildObj).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to trigger cloudbuild: %w", err)
	}

	var buildMeta cloudbuild.BuildOperationMetadata
	if err := json.Unmarshal(resp.Metadata, &buildMeta); err != nil {
		return &BuildInfo{OperationName: resp.Name}, nil
	}

	info := &BuildInfo{
		OperationName: resp.Name,
	}
	if buildMeta.Build != nil {
		info.BuildID = buildMeta.Build.Id
		info.Status = buildMeta.Build.Status
	}

	return info, nil
}
