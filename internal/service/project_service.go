package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/JoseGaldamez/nubbe-core/internal/pkg/crypto"
	"github.com/JoseGaldamez/nubbe-core/internal/repository"
	"github.com/JoseGaldamez/nubbe-core/internal/service/build"
)

type ProjectService struct {
	userRepo     repository.UserRepository
	projectRepo  repository.ProjectRepository
	buildService build.BuildService
	aesKey       string
}

func NewProjectService(ur repository.UserRepository, pr repository.ProjectRepository, bs build.BuildService, aesKey string) *ProjectService {
	return &ProjectService{
		userRepo:     ur,
		projectRepo:  pr,
		buildService: bs,
		aesKey:       aesKey,
	}
}

func (service *ProjectService) TriggerBuild(ctx context.Context, userID, subDomain, repoName, projectType, githubToken, entryPoint string, envVars map[string]string, advancedConfig map[string]string) (*build.BuildInfo, error) {
	info, err := service.buildService.TriggerBuild(ctx, userID, subDomain, repoName, projectType, githubToken, entryPoint, envVars, advancedConfig)
	if err != nil {
		return nil, fmt.Errorf("project service failed to trigger build: %w", err)
	}
	return info, nil
}

func (service *ProjectService) GetUserToken(ctx context.Context, userID string) (string, error) {
	token, err := service.userRepo.GetGithubToken(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("service failed to get user token: %w", err)
	}
	return token, nil
}

func (service *ProjectService) UpdateStatus(ctx context.Context, userID, projectID, buildID, status, logURL, message string) error {
	if err := service.projectRepo.UpdateBuildStatus(ctx, userID, projectID, buildID, status, logURL, message); err != nil {
		return fmt.Errorf("service failed to update build status: %w", err)
	}
	return nil
}

func (service *ProjectService) UpdateProjectStatus(ctx context.Context, userID, projectID, status string) error {
	if err := service.projectRepo.UpdateProjectStatus(ctx, userID, projectID, status); err != nil {
		return fmt.Errorf("service failed to update project status: %w", err)
	}
	return nil
}

func (service *ProjectService) GetProjectDetails(ctx context.Context, userID, projectID string) (*repository.ProjectDetails, error) {
	details, err := service.projectRepo.GetProjectDetails(ctx, userID, projectID)
	if err != nil {
		return nil, fmt.Errorf("service failed to get project details: %w", err)
	}
	return details, nil
}

func (service *ProjectService) SaveProjectVars(ctx context.Context, userID, projectID string, vars map[string]string) error {
	if len(vars) == 0 {
		return service.projectRepo.SaveProjectVars(ctx, userID, projectID, nil)
	}

	jsonData, err := json.Marshal(vars)
	if err != nil {
		return fmt.Errorf("failed to marshal env vars: %w", err)
	}

	encrypted, err := crypto.EncryptAES(jsonData, service.aesKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt env vars: %w", err)
	}

	return service.projectRepo.SaveProjectVars(ctx, userID, projectID, encrypted)
}

func (service *ProjectService) GetProjectVars(ctx context.Context, userID, projectID string) (map[string]string, error) {
	encrypted, err := service.projectRepo.GetProjectVars(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}

	if encrypted == "" {
		return make(map[string]string), nil
	}

	decrypted, err := crypto.DecryptAES(encrypted, service.aesKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt env vars: %w", err)
	}

	var vars map[string]string
	if err := json.Unmarshal(decrypted, &vars); err != nil {
		return nil, fmt.Errorf("failed to unmarshal env vars: %w", err)
	}

	return vars, nil
}

func (service *ProjectService) RegisterGitHubWebhook(ctx context.Context, userID, projectID, repoName, token string) error {
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
		return fmt.Errorf("failed to marshal github hook config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create github request: %w", err)
	}

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (service *ProjectService) DeleteGitHubWebhook(ctx context.Context, userID, projectID, repoName, token string) error {
	payloadURL := fmt.Sprintf("https://api.nubbe.run/webhooks/github?uid=%s&pid=%s", userID, projectID)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/hooks", repoName)

	client := &http.Client{}

	reqGet, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create github get hooks request: %w", err)
	}

	reqGet.Header.Set("Authorization", "token "+token)
	reqGet.Header.Set("Accept", "application/vnd.github.v3+json")

	respGet, err := client.Do(reqGet)
	if err != nil {
		return fmt.Errorf("error ejecutando request GET a GitHub: %v", err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respGet.Body)
		return fmt.Errorf("github get hooks failed with status %d: %s", respGet.StatusCode, string(body))
	}

	var hooks []struct {
		ID     int `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := json.NewDecoder(respGet.Body).Decode(&hooks); err != nil {
		return fmt.Errorf("failed to decode github hooks response: %w", err)
	}

	var hookIDToDelete int
	for _, hook := range hooks {
		if hook.Config.URL == payloadURL {
			hookIDToDelete = hook.ID
			break
		}
	}

	if hookIDToDelete == 0 {
		return nil
	}

	deleteURL := fmt.Sprintf("%s/%d", apiURL, hookIDToDelete)
	reqDel, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create github delete hook request: %w", err)
	}

	reqDel.Header.Set("Authorization", "token "+token)
	reqDel.Header.Set("Accept", "application/vnd.github.v3+json")

	respDel, err := client.Do(reqDel)
	if err != nil {
		return fmt.Errorf("failed to execute github delete hook request: %w", err)
	}
	defer respDel.Body.Close()

	if respDel.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(respDel.Body)
		return fmt.Errorf("github delete hook failed with status %d: %s", respDel.StatusCode, string(body))
	}

	return nil
}
