package handlers

import (
	"context"
	"log"
	"net/http"

	"time"

	"github.com/JoseGaldamez/nubbe-core/internal/repository"
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
func (app *App) HandleCreateProject(ctx *gin.Context) {
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

	// 1.1 Generate or use a secret for Webhook Signature Validation
	webhookSecret := app.ProjectService.GetAESKey()

	// 1.2 Create Project in Firestore (Capa de Datos)
	projectDetails := &repository.ProjectDetails{
		ProjectID: req.SubDomain, // Usando subdomain como ID por ahora
		Subdomain: req.SubDomain,
		OwnerID:   userID,
		Repository: repository.RepositoryConfig{
			URL:    "https://github.com/" + req.RepoName,
			Branch: req.Branch,
		},
		BuildConfig: repository.BuildConfig{
			Target:         "CLOUD_RUN", // Default o basado en type
			Runtime:        req.ProjectType,
			BuildCommand:   req.AdvancedConfig["build_command"],
			RunCommand:     req.AdvancedConfig["run_command"],
			DistDirectory:  req.AdvancedConfig["dist_directory"],
			EntryPoint:     req.EntryPoint,
		},
		Status: repository.ProjectStatus{
			State: "BUILDING",
		},
		// Compatibilidad
		RepoName:       req.RepoName,
		ProjectType:    req.ProjectType,
		AdvancedConfig: req.AdvancedConfig,
	}

	if req.ProjectType == "static" || req.ProjectType == "react" || req.ProjectType == "astro" {
		projectDetails.BuildConfig.Target = "CLOUDFLARE_PAGES"
	}

	if err := app.ProjectService.CreateProject(ctx.Request.Context(), userID, projectDetails); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create project record", "details": err.Error()})
		return
	}

	// 2. Save Env Vars (Optimized: Null if empty)
	if err := app.ProjectService.SaveProjectVars(ctx.Request.Context(), userID, req.SubDomain, req.EnvVars); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save environment variables", "details": err.Error()})
		return
	}

	// 3. Register GitHub Webhook Async via Service
	app.WG.Add(1)
	go func(uID, pID, rName, t, secret string) {
		defer app.WG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.ProjectService.RegisterGitHubWebhook(ctx, uID, pID, rName, t, secret); err != nil {
			log.Printf("Warning: Failed to register GitHub Webhook for project %s: %v", pID, err)
		}
	}(userID, req.SubDomain, req.RepoName, token, webhookSecret)

	// 4. Publish Build Event to PubSub
	buildEvent := map[string]interface{}{
		"user_id":         userID,
		"project_id":      req.SubDomain,
		"repo_name":       req.RepoName,
		"project_type":    req.ProjectType,
		"github_token":    token,
		"entry_point":     req.EntryPoint,
		"env_vars":        req.EnvVars,
		"advanced_config": req.AdvancedConfig,
		"action":          "INITIAL_BUILD",
		"webhook_secret":  webhookSecret,
		"branch":          req.Branch,
	}

	msgID, err := app.PubSub.PublishBuildEvent(ctx.Request.Context(), buildEvent)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue build event", "details": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message":      "Project created and deployment queued successfully",
		"subdomain":    req.SubDomain,
		"pubsub_msg_id": msgID,
	})
}
