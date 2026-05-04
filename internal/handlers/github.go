package handlers

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// GitHubPushPayload represents the relevant parts of GitHub's push event payload.
type GitHubPushPayload struct {
	Ref string `json:"ref"`
}

// HandleGitHubWebhook handles the GitHub webhook requests.
func (app *App) HandleGitHubWebhook(ctx *gin.Context) {
	uid := ctx.Query("uid")
	pid := ctx.Query("pid")

	if uid == "" || pid == "" {
		log.Println("Webhook error: missing uid or pid in query params")
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Missing uid or pid"})
		return
	}

	var payload GitHubPushPayload
	if err := ctx.ShouldBindJSON(&payload); err != nil {
		log.Printf("Webhook error: failed to bind JSON: %v", err)
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	// Extract branch name from ref (e.g., "refs/heads/main" -> "main")
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	// Fetch project data via Service
	projectData, err := app.ProjectService.GetProjectDetails(ctx.Request.Context(), uid, pid)
	if err != nil {
		log.Printf("Webhook error: project not found: %v", err)
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Compare branches
	if branch != projectData.Branch {
		log.Printf("Webhook: push to branch %s ignored (expected %s)", branch, projectData.Branch)
		ctx.JSON(http.StatusOK, gin.H{"message": "Branch mismatch, skipping build"})
		return
	}

	// Fetch user's GitHub token via Service
	token, err := app.ProjectService.GetUserToken(ctx.Request.Context(), uid)
	if err != nil {
		log.Printf("Webhook error: user not found: %v", err)
		ctx.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	if token == "" {
		log.Printf("Webhook error: GitHub Access Token not found for user")
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "GitHub Access Token not found"})
		return
	}

	// Fetch and Decrypt Env Vars via Service
	envVars, err := app.ProjectService.GetProjectVars(ctx.Request.Context(), uid, pid)
	if err != nil {
		log.Printf("Webhook warning: failed to fetch env vars: %v", err)
		// No detenemos el flujo, solo logueamos el error
	}

	// Trigger Cloud Build via Service
	info, err := app.ProjectService.TriggerBuild(ctx.Request.Context(), uid, pid, projectData.RepoName, projectData.ProjectType, token, projectData.EntryPoint, envVars, projectData.AdvancedConfig)
	if err != nil {
		log.Printf("Webhook error: failed to trigger build: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to trigger build"})
		return
	}

	log.Printf("Webhook: Build triggered for project %s (buildID: %s)", pid, info.BuildID)
	ctx.JSON(http.StatusOK, gin.H{
		"message":      "Build triggered successfully",
		"build_id":     info.BuildID,
		"build_status": info.Status,
	})
}
