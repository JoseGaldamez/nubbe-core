package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"cloud.google.com/go/storage"
	"github.com/JoseGaldamez/nubbe-core/internal/builders"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/cloudbuild/v1"
)

// GetBuildLogs actúa como un proxy para leer los logs de Cloud Build desde GCS.
func GetBuildLogs(c *gin.Context) {
	buildID := c.Param("buildId")
	if buildID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "buildId is required"})
		return
	}

	bucketName := os.Getenv("LOGS_BUCKET")
	if bucketName == "" {
		bucketName = "nubbe-build-logs" // Fallback por si no está en env
	}

	if StorageClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage client not initialized"})
		return
	}

	objectName := fmt.Sprintf("log-%s.txt", buildID)
	rc, err := StorageClient.Bucket(bucketName).Object(objectName).NewReader(c.Request.Context())
	if err != nil {
		if err == storage.ErrObjectNotExist {
			// Manejo de error amigable solicitado
			c.String(http.StatusOK, "Iniciando entorno de compilación. Esperando logs...")
			return
		}
		log.Printf("Error leyendo log de GCS: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read logs from storage"})
		return
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		log.Printf("Error leyendo contenido del log: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process log content"})
		return
	}

	c.String(http.StatusOK, string(content))
}

// CreateProjectRequest defines the structure for the incoming project creation payload.
type CreateProjectRequest struct {
	ProjectType string `json:"project_type" binding:"required,oneof=static react node astro go"`
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

	// 2. Get Builder Strategy
	builder, err := builders.GetBuilder(req.ProjectType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	dynamicDockerfile := builder.GetDockerfile()

	imageURL := fmt.Sprintf("us-central1-docker.pkg.dev/%s/nubbe-repo/%s", projectID, req.SubDomain)

	buildObj := &cloudbuild.Build{
		LogsBucket: "gs://nubbe-build-logs",
		Substitutions: map[string]string{
			"_PROJECT_ID":    req.SubDomain,
			"_USER_ID":       userID,
			"_GITHUB_TOKEN":  userData.GithubAccessToken,
			"_REPO_NAME":    req.RepoName,
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
