package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// GitHubPushPayload represents the relevant parts of GitHub's push event payload.
type GitHubPushPayload struct {
	Ref string `json:"ref"`
}

// RegisterGitHubWebhook registers a webhook in the specified GitHub repository.
func RegisterGitHubWebhook(ctx context.Context, userID, projectID, repoName, token string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/hooks", repoName)
	payloadURL := fmt.Sprintf("https://api.nubbe.run/webhooks/github?uid=%s&pid=%s", userID, projectID)

	hookConfig := map[string]interface{}{
		"name":   "web",
		"active": true,
		"events": []string{"push"},
		"config": map[string]interface{}{
			"url":          payloadURL,
			"content_type": "json",
			"insecure_ssl": "0",
		},
	}

	payloadBytes, err := json.Marshal(hookConfig)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// HandleGitHubWebhook handles the GitHub webhook requests.
func HandleGitHubWebhook(c *gin.Context) {
	uid := c.Query("uid")
	pid := c.Query("pid")

	if uid == "" || pid == "" {
		log.Println("Webhook error: missing uid or pid in query params")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing uid or pid"})
		return
	}

	var payload GitHubPushPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		log.Printf("Webhook error: failed to bind JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	// Extract branch name from ref (e.g., "refs/heads/main" -> "main")
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	// Fetch project data from Firestore
	if FsClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Firestore client not initialized"})
		return
	}

	projectDoc, err := FsClient.Collection("users").Doc(uid).Collection("projects").Doc(pid).Get(c.Request.Context())
	if err != nil {
		log.Printf("Webhook error: project not found: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var projectData struct {
		Branch      string `firestore:"branch"`
		RepoName    string `firestore:"repo_name"`
		ProjectType string `firestore:"project_type"`
	}
	if err := projectDoc.DataTo(&projectData); err != nil {
		log.Printf("Webhook error: failed to parse project data: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse project data"})
		return
	}

	// Compare branches
	if branch != projectData.Branch {
		log.Printf("Webhook: push to branch %s ignored (expected %s)", branch, projectData.Branch)
		c.JSON(http.StatusOK, gin.H{"message": "Branch mismatch, skipping build"})
		return
	}

	// Fetch user's GitHub token
	userDoc, err := FsClient.Collection("users").Doc(uid).Get(c.Request.Context())
	if err != nil {
		log.Printf("Webhook error: user not found: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	var userData struct {
		GithubAccessToken string `firestore:"githubAccessToken"`
	}
	if err := userDoc.DataTo(&userData); err != nil {
		log.Printf("Webhook error: failed to parse user data: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user data"})
		return
	}

	// Trigger Cloud Build
	buildID, buildStatus, _, err := triggerCloudBuild(c.Request.Context(), uid, pid, projectData.RepoName, projectData.ProjectType, userData.GithubAccessToken)
	if err != nil {
		log.Printf("Webhook error: failed to trigger build: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to trigger build"})
		return
	}

	log.Printf("Webhook: Build triggered for project %s (buildID: %s)", pid, buildID)
	c.JSON(http.StatusOK, gin.H{
		"message":      "Build triggered successfully",
		"build_id":     buildID,
		"build_status": buildStatus,
	})
}
