package controllers

import (
	"context"
	"log"
	"net/http"

	"time"

	"github.com/JoseGaldamez/nubbe-core/internal/handlers"
	"github.com/gin-gonic/gin"
)

// CreateProjectRequest defines the structure for the incoming project creation payload.
type CreateProjectRequest struct {
	ProjectType    string            `json:"project_type" binding:"required,oneof=static react nodejs astro go"`
	EntryPoint     string            `json:"entry_point" binding:"required"`
	RepoName       string            `json:"repo_name" binding:"required"`
	Title          string            `json:"title" binding:"required"`
	SubDomain      string            `json:"sub_domain" binding:"required"`
	Branch         string            `json:"branch" binding:"required"`
	EnvVars        map[string]string `json:"env_vars"`
	AdvancedConfig map[string]string `json:"advanced_config"`
}

// HandleCreateProject handles the project creation request and submits a build to Cloud Build.
func HandleCreateProject(ctx *gin.Context, app *handlers.App) {
	var req CreateProjectRequest

	// Bind and validate incoming JSON
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request payload",
			"details": err.Error(),
		})
		return
	}

	// Obtener el ID del usuario desde el contexto
	userID := ctx.GetString("user_id")
	if userID == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in context"})
		return
	}

	// 1. Fetch GitHub Token via Service
	token, err := app.ProjectService.GetUserToken(ctx.Request.Context(), userID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user data", "details": err.Error()})
		return
	}

	if token == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "GitHub Access Token not found for user"})
		return
	}

	// 2. Save Env Vars (Optimized: Null if empty)
	if err := app.ProjectService.SaveProjectVars(ctx.Request.Context(), userID, req.SubDomain, req.EnvVars); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save environment variables", "details": err.Error()})
		return
	}

	// 3. Register GitHub Webhook Async via Service
	app.WG.Add(1)
	go func(uID, pID, rName, t string) {
		defer app.WG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.ProjectService.RegisterGitHubWebhook(ctx, uID, pID, rName, t); err != nil {
			log.Printf("Warning: Failed to register GitHub Webhook for project %s: %v", pID, err)
		}
	}(userID, req.SubDomain, req.RepoName, token)

	// 4. Trigger Initial Cloud Build via Service
	info, err := app.ProjectService.TriggerBuild(ctx.Request.Context(), userID, req.SubDomain, req.RepoName, req.ProjectType, token, req.EntryPoint, req.EnvVars, req.AdvancedConfig)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to trigger build", "details": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message":      "Project created and deployment started successfully",
		"build_id":     info.BuildID,
		"build_status": info.Status,
		"operation_id": info.OperationName,
	})
}
