package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// GitHubPushPayload represents the relevant parts of GitHub's push event payload.
type GitHubPushPayload struct {
	Ref     string `json:"ref"`
	Commits []struct {
		Modified []string `json:"modified"`
		Added    []string `json:"added"`
		Removed  []string `json:"removed"`
	} `json:"commits"`
}

// auditDependencies checks if any dependency files were modified in the push.
func auditDependencies(payload GitHubPushPayload) bool {
	depFiles := []string{"package.json", "go.mod", "requirements.txt", "pom.xml", "build.gradle"}
	for _, commit := range payload.Commits {
		allFiles := append(commit.Modified, commit.Added...)
		allFiles = append(allFiles, commit.Removed...)
		for _, file := range allFiles {
			for _, depFile := range depFiles {
				if strings.Contains(file, depFile) {
					return true
				}
			}
		}
	}
	return false
}

// validateGitHubSignature validates the HMAC hex digest of the payload.
func validateGitHubSignature(payload []byte, signature string, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	signature = strings.TrimPrefix(signature, "sha256=")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
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

	// 1. Read body for signature validation
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		log.Printf("Webhook error: failed to read body: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}
	// Restore body for Gin binding
	ctx.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	// 2. Validate Signature
	signature := ctx.GetHeader("X-Hub-Signature-256")
	secret := app.ProjectService.GetAESKey()
	if !validateGitHubSignature(body, signature, secret) {
		log.Printf("Webhook error: invalid signature for project %s", pid)
		ctx.JSON(http.StatusForbidden, gin.H{"error": "Invalid signature"})
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
	if branch != projectData.Repository.Branch {
		log.Printf("Webhook: push to branch %s ignored (expected %s)", branch, projectData.Repository.Branch)
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

	// Update status to BUILDING via Service
	if err := app.ProjectService.UpdateProjectStatus(ctx.Request.Context(), uid, pid, "BUILDING"); err != nil {
		log.Printf("Webhook warning: failed to update status to BUILDING: %v", err)
	}

	// Audit for dependency changes
	needsAuditUpdate := auditDependencies(payload)

	// Publish Build Event to PubSub
	buildEvent := map[string]interface{}{
		"user_id":         uid,
		"project_id":      pid,
		"repo_name":       projectData.RepoName,
		"project_type":    projectData.ProjectType,
		"github_token":    token,
		"entry_point":     projectData.BuildConfig.EntryPoint,
		"env_vars":        envVars,
		"advanced_config": projectData.AdvancedConfig,
		"action":          "PUSH_BUILD",
		"branch":          branch,
		"audit_update":    needsAuditUpdate,
	}

	msgID, err := app.PubSub.PublishBuildEvent(ctx.Request.Context(), buildEvent)
	if err != nil {
		log.Printf("Webhook error: failed to queue build event: %v", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue build event"})
		return
	}

	log.Printf("Webhook: Build queued for project %s (msgID: %s)", pid, msgID)
	ctx.JSON(http.StatusOK, gin.H{
		"message":       "Build queued successfully",
		"pubsub_msg_id": msgID,
	})
}
