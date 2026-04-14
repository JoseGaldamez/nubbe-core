package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"google.golang.org/api/cloudbuild/v1"
)

// CreateProjectRequest defines the structure for the incoming project creation payload.
type CreateProjectRequest struct {
	ProjectType string `json:"project_type" binding:"required,oneof=static react node"`
	EntryPoint  string `json:"entry_point" binding:"required"`
	RepoName    string `json:"repo_name" binding:"required"`
	Title       string `json:"title" binding:"required"`
	SubDomain   string `json:"sub_domine" binding:"required"`
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

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", projectID, req.SubDomain)

	// Create a dynamic Dockerfile string based on the framework
	var dynamicDockerfile string
	switch req.ProjectType {
	case "static":
		dynamicDockerfile = "FROM nginx:alpine\nCOPY . /usr/share/nginx/html/\nEXPOSE 80\nCMD [\"nginx\", \"-g\", \"daemon off;\"]"
	case "react":
		dynamicDockerfile = "FROM node:18-alpine\nWORKDIR /app\nCOPY . .\nRUN npm install && npm run build\nRUN npm install -g serve\nEXPOSE 3000\nCMD [\"serve\", \"-s\", \"build\", \"-l\", \"3000\"]"
	case "node":
		dynamicDockerfile = "FROM node:18-alpine\nWORKDIR /app\nCOPY . .\nRUN npm install\nEXPOSE 8080\nCMD [\"npm\", \"start\"]"
	default:
		dynamicDockerfile = "FROM nginx:alpine\nCOPY . /usr/share/nginx/html/\nEXPOSE 80"
	}

	buildObj := &cloudbuild.Build{
		Substitutions: map[string]string{
			"_PROJECT_ID": req.SubDomain,
			"_USER_ID":    userID,
		},
		Options: &cloudbuild.BuildOptions{
			SubstitutionOption: "ALLOW_LOOSE",
		},
		Steps: []*cloudbuild.BuildStep{
			{
				Name: "ubuntu",
				Args: []string{"bash", "-c", fmt.Sprintf("echo '%s' > Dockerfile", dynamicDockerfile)},
			},
			{
				Name: "ubuntu",
				Args: []string{"bash", "-c", "echo '<h1>Deploying from Nubbe PaaS!</h1>' > index.html"},
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
				Args: []string{"run", "deploy", req.SubDomain, "--image", imageURL, "--platform", "managed", "--region", "us-central1", "--allow-unauthenticated", "--port", "80"},
			},
		},
	}

	cbService, err := cloudbuild.NewService(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to initialize Cloud Build service",
			"details": err.Error(),
		})
		return
	}

	resp, err := cbService.Projects.Builds.Create(projectID, buildObj).Do()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to submit build",
			"details": err.Error(),
		})
		return
	}

	var buildMeta cloudbuild.BuildOperationMetadata
	if err := json.Unmarshal(resp.Metadata, &buildMeta); err != nil {
		// Fallback in case metadata parsing fails
		c.JSON(http.StatusOK, gin.H{
			"message":      "Deployment started successfully",
			"operation_id": resp.Name,
		})
		return
	}

	var buildID, buildStatus string
	if buildMeta.Build != nil {
		buildID = buildMeta.Build.Id
		buildStatus = buildMeta.Build.Status
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Deployment started successfully",
		"build_id":     buildID,
		"build_status": buildStatus,
		"operation_id": resp.Name,
	})
}
