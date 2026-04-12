package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CreateProjectRequest defines the structure for the incoming project creation payload.
type CreateProjectRequest struct {
	ProjectType string `json:"project_type" binding:"required,oneof=static react node"`
	EntryPoint  string `json:"entry_point" binding:"required"`
	RepoName    string `json:"repo_name" binding:"required"`
	Title       string `json:"title" binding:"required"`
	SubDomain   string `json:"sub_domine" binding:"required"`
}

// HandleCreateProject handles the project creation request and returns a mock GCP payload.
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

	var mockGCPPayload gin.H
	// Migrated from GCR to Artifact Registry
	imageName := "us-central1-docker.pkg.dev/nubbe-prod/nubbe-repo/" + req.SubDomain
	region := "us-central1"

	// Mock logic based on project type
	switch req.ProjectType {
	case "static":
		// Dynamic Injection: Using Nginx to serve static files
		mockGCPPayload = gin.H{
			"service_name": req.SubDomain,
			"region":       region,
			"image":        imageName,
			"build_steps": []gin.H{
				{
					"name": "gcr.io/cloud-builders/docker",
					"args": []string{"build", "-t", imageName, ".", "--build-arg", "ENTRY_POINT=" + req.EntryPoint},
				},
				{
					"name": "gcr.io/cloud-builders/gcloud",
					"args": []string{"run", "deploy", req.SubDomain, "--image", imageName, "--platform", "managed", "--region", region, "--allow-unauthenticated"},
				},
			},
			"dynamic_injection": gin.H{
				"dockerfile":  "FROM nginx:alpine\nCOPY . /usr/share/nginx/html/\nEXPOSE 80",
				"description": "Nginx container with all content injected at build time",
			},
		}
	case "react", "node":
		// Cloud Native Buildpacks logic
		mockGCPPayload = gin.H{
			"service_name": req.SubDomain,
			"region":       region,
			"image":        imageName,
			"build_type":   "Cloud Native Buildpacks",
			"builder":      "gcr.io/buildpacks/builder:v1",
			"build_steps": []gin.H{
				{
					"name": "gcr.io/k8s-skaffold/pack",
					"args": []string{"build", imageName, "--builder", "gcr.io/buildpacks/builder:v1", "--publish"},
				},
				{
					"name": "gcr.io/cloud-builders/gcloud",
					"args": []string{"run", "deploy", req.SubDomain, "--image", imageName, "--platform", "managed", "--region", region, "--allow-unauthenticated"},
				},
			},
		}
	default:
		// This should be caught by validation, but added for safety
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported project type"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "Mock deployment triggered successfully",
		"mock_gcp_payload": mockGCPPayload,
	})
}
