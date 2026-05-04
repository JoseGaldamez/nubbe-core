package repository

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
)

type ProjectRepository interface {
	UpdateBuildStatus(ctx context.Context, userID, projectID, buildID, status, logURL, message string) error
	UpdateProjectStatus(ctx context.Context, userID, projectID, status string) error
	GetProjectData(ctx context.Context, userID, projectID string) (map[string]interface{}, error)
	GetProjectDetails(ctx context.Context, userID, projectID string) (*ProjectDetails, error)
	SaveProjectVars(ctx context.Context, userID, projectID string, encryptedVars interface{}) error
	GetProjectVars(ctx context.Context, userID, projectID string) (string, error)
}

func (r *firestoreProjectRepo) SaveProjectVars(ctx context.Context, userID, projectID string, encryptedVars interface{}) error {
	_, err := r.client.Collection("users").Doc(userID).Collection("projects").Doc(projectID).Set(ctx, map[string]interface{}{
		"encryptedVars": encryptedVars,
	}, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed to save project vars: %w", err)
	}
	return nil
}

func (r *firestoreProjectRepo) GetProjectVars(ctx context.Context, userID, projectID string) (string, error) {
	dsnap, err := r.client.Collection("users").Doc(userID).Collection("projects").Doc(projectID).Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get project vars: %w", err)
	}

	val, err := dsnap.DataAt("encryptedVars")
	if err != nil {
		return "", nil // Podría no existir el campo
	}

	if val == nil {
		return "", nil
	}

	str, ok := val.(string)
	if !ok {
		return "", nil
	}

	return str, nil
}

type ProjectDetails struct {
	Branch         string            `firestore:"branch"`
	RepoName       string            `firestore:"repo_name"`
	ProjectType    string            `firestore:"project_type"`
	EntryPoint     string            `firestore:"entry_point"`
	AdvancedConfig map[string]string `firestore:"advanced_config"`
}

type firestoreProjectRepo struct {
	client *firestore.Client
}

func NewProjectRepository(client *firestore.Client) ProjectRepository {
	return &firestoreProjectRepo{client: client}
}

func (r *firestoreProjectRepo) UpdateProjectStatus(ctx context.Context, userID, projectID, status string) error {
	_, err := r.client.Collection("users").Doc(userID).Collection("projects").Doc(projectID).Update(ctx, []firestore.Update{
		{Path: "status", Value: status},
	})
	if err != nil {
		return fmt.Errorf("failed to update project status: %w", err)
	}
	return nil
}

func (r *firestoreProjectRepo) GetProjectDetails(ctx context.Context, userID, projectID string) (*ProjectDetails, error) {
	doc, err := r.client.Collection("users").Doc(userID).Collection("projects").Doc(projectID).Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get project details: %w", err)
	}
	var details ProjectDetails
	if err := doc.DataTo(&details); err != nil {
		return nil, fmt.Errorf("failed to parse project details: %w", err)
	}
	return &details, nil
}

func (r *firestoreProjectRepo) UpdateBuildStatus(ctx context.Context, userID, projectID, buildID, status, logURL, message string) error {
	batch := r.client.Batch()
	now := firestore.ServerTimestamp

	// 1. Referencia y datos para el BUILD (sub-colección)
	buildRef := r.client.Collection("users").Doc(userID).
		Collection("projects").Doc(projectID).
		Collection("builds").Doc(buildID)

	historyEntry := map[string]interface{}{
		"status":    status,
		"message":   message,
		"timestamp": now,
	}

	batch.Set(buildRef, map[string]interface{}{
		"buildId":   buildID,
		"status":    status,
		"logUrl":    logURL,
		"updatedAt": now,
		"history":   firestore.ArrayUnion(historyEntry),
	}, firestore.MergeAll)

	// 2. Referencia para el PROYECTO (documento padre)
	projectRef := r.client.Collection("users").Doc(userID).
		Collection("projects").Doc(projectID)

	batch.Update(projectRef, []firestore.Update{
		{Path: "status", Value: status},
		{Path: "updatedAt", Value: now},
	})

	// Ejecutar ambas operaciones de forma atómica
	if _, err := batch.Commit(ctx); err != nil {
		return fmt.Errorf("failed to update build and project status in batch: %w", err)
	}

	return nil
}

func (r *firestoreProjectRepo) GetProjectData(ctx context.Context, userID, projectID string) (map[string]interface{}, error) {
	doc, err := r.client.Collection("users").Doc(userID).Collection("projects").Doc(projectID).Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get project data: %w", err)
	}
	return doc.Data(), nil
}
