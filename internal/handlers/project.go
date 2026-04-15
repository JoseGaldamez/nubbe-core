package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/JoseGaldamez/nubbe-core/internal/builders"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/cloudbuild/v1"
)

// CreateProjectRequest defines the structure for the incoming project creation payload.
type CreateProjectRequest struct {
	ProjectType string `json:"project_type" binding:"required,oneof=static react node astro go"`
	EntryPoint  string `json:"entry_point" binding:"required"`
	RepoName    string `json:"repo_name" binding:"required"`
	Title       string `json:"title" binding:"required"`
	SubDomain   string `json:"sub_domine" binding:"required"`
	Branch      string `json:"branch" binding:"required"`
}

// HandleCreateProject handles the project creation request and submits a build to Cloud Build.
func HandleCreateProject(c *gin.Context) {
	var req CreateProjectRequest

	// Bind and validate incoming JSON
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request payload",
			"details": err.Error(),
		})
		return
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "GCP_PROJECT_ID environment variable is not set",
		})
		return
	}

	// Obtener el ID del usuario desde el contexto (inyectado por el middleware de Firebase)
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "User ID not found in context",
		})
		return
	}

	// 1. Fetch GitHub Token from Firestore
	if FsClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Firestore client not initialized"})
		return
	}

	dsnap, err := FsClient.Collection("users").Doc(userID).Get(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user data", "details": err.Error()})
		return
	}

	var userData struct {
		GithubAccessToken string `firestore:"githubAccessToken"`
	}
	if err := dsnap.DataTo(&userData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user data", "details": err.Error()})
		return
	}

	if userData.GithubAccessToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "GitHub Access Token not found for user"})
		return
	}

	// // 2. Save Project to Firestore
	// projectData := map[string]interface{}{
	// 	"project_type": req.ProjectType,
	// 	"entry_point":  req.EntryPoint,
	// 	"repo_name":    req.RepoName,
	// 	"title":        req.Title,
	// 	"sub_domine":   req.SubDomain,
	// 	"branch":       req.Branch,
	// 	"createdAt":    time.Now().Format(time.RFC3339), // Use real timestamp if available
	// }

	// _, err = FsClient.Collection("users").Doc(userID).Collection("projects").Doc(req.SubDomain).Set(c.Request.Context(), projectData)
	// if err != nil {
	// 	c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save project data", "details": err.Error()})
	// 	return
	// }

	// 3. Register GitHub Webhook
	err = RegisterGitHubWebhook(userID, req.SubDomain, req.RepoName, userData.GithubAccessToken)
	if err != nil {
		log.Printf("Warning: Failed to register GitHub Webhook: %v", err)
		// We continue even if webhook fails, but in a real app you might want to handle this better
	}

	// 4. Trigger Initial Cloud Build
	buildID, buildStatus, operationName, err := triggerCloudBuild(c.Request.Context(), userID, req.SubDomain, req.RepoName, req.ProjectType, userData.GithubAccessToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to trigger build", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Project created and deployment started successfully",
		"build_id":     buildID,
		"build_status": buildStatus,
		"operation_id": operationName,
	})
}

// triggerCloudBuild starts a Cloud Build process and returns build info.
func triggerCloudBuild(ctx context.Context, userID, subDomain, repoName, projectType, githubToken string) (string, string, string, error) {
	projectID := os.Getenv("GCP_PROJECT_ID")

	builder, err := builders.GetBuilder(projectType)
	if err != nil {
		return "", "", "", err
	}
	dynamicDockerfile := builder.GetDockerfile()

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", projectID, subDomain)

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
				Args: []string{"build", "-t", imageURL, "."},
			},
			{
				Name: "gcr.io/cloud-builders/docker",
				Args: []string{"push", imageURL},
			},
			{
				Name: "gcr.io/cloud-builders/gcloud",
				Args: []string{"run", "deploy", subDomain, "--image", imageURL, "--platform", "managed", "--region", "us-central1", "--allow-unauthenticated", "--port", "80"},
			},
		},
	}

	cbService, err := cloudbuild.NewService(ctx)
	if err != nil {
		return "", "", "", err
	}

	resp, err := cbService.Projects.Builds.Create(projectID, buildObj).Do()
	if err != nil {
		return "", "", "", err
	}

	var buildMeta cloudbuild.BuildOperationMetadata
	if err := json.Unmarshal(resp.Metadata, &buildMeta); err != nil {
		return "", "", resp.Name, nil
	}

	var buildID, buildStatus string
	if buildMeta.Build != nil {
		buildID = buildMeta.Build.Id
		buildStatus = buildMeta.Build.Status
	}

	return buildID, buildStatus, resp.Name, nil
}
