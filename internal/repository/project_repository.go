package repository

import (
	"context"
	"fmt"
	"log"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

type ProjectRepository interface {
	CreateProject(ctx context.Context, userID string, details *ProjectDetails) error
	UpdateBuildStatus(ctx context.Context, userID, projectID, buildID, status, logURL, message string) error
	UpdateProjectStatus(ctx context.Context, userID, projectID, status string) error
	GetProjectData(ctx context.Context, userID, projectID string) (map[string]interface{}, error)
	GetProjectDetails(ctx context.Context, userID, projectID string) (*ProjectDetails, error)
	SaveProjectVars(ctx context.Context, userID, projectID string, encryptedVars interface{}) error
	GetProjectVars(ctx context.Context, userID, projectID string) (string, error)
	DeleteProject(ctx context.Context, userID, projectID string) error
	CheckSubdomainExistsGlobal(ctx context.Context, subdomain string) (bool, error)
}

func (r *firestoreProjectRepo) CreateProject(ctx context.Context, userID string, details *ProjectDetails) error {
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// 1. Verificar si el subdominio ya está tomado en la colección global "subdomains"
		subdomainRef := r.client.Collection("subdomains").Doc(details.Subdomain)
		subdomainDoc, err := tx.Get(subdomainRef)
		if err == nil && subdomainDoc.Exists() {
			return fmt.Errorf("subdomain_taken")
		}

		// 2. Crear el documento en "subdomains"
		err = tx.Set(subdomainRef, map[string]interface{}{
			"projectId": details.Subdomain,
			"ownerId":   userID,
			"createdAt": firestore.ServerTimestamp,
		})
		if err != nil {
			return err
		}

		// 3. Crear el proyecto en "users/{userID}/projects/{subdomain}"
		projectRef := r.client.Collection("users").Doc(userID).Collection("projects").Doc(details.Subdomain)
		return tx.Set(projectRef, details)
	})

	if err != nil {
		return fmt.Errorf("failed to create project transaction: %w", err)
	}
	return nil
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

type RepositoryConfig struct {
	URL    string `firestore:"url" json:"url"`
	Branch string `firestore:"branch" json:"branch"`
}

type BuildConfig struct {
	Target         string            `firestore:"target" json:"target"`
	Runtime        string            `firestore:"runtime" json:"runtime"`
	RuntimeVersion string            `firestore:"runtime_version" json:"runtime_version"`
	BuildCommand   string            `firestore:"build_command" json:"build_command"`
	RunCommand     string            `firestore:"run_command" json:"run_command"`
	DistDirectory  string            `firestore:"dist_directory" json:"dist_directory"`
	EntryPoint     string            `firestore:"entry_point" json:"entry_point"` // Manteniendo compatibilidad
}

type ProjectStatus struct {
	State             string `firestore:"state" json:"state"`
	CurrentURL        string `firestore:"current_url" json:"current_url"`
	LastDeploymentSHA string `firestore:"last_deployment_sha" json:"last_deployment_sha"`
}

type ProjectDetails struct {
	ProjectID   string           `firestore:"project_id" json:"project_id"`
	Subdomain   string           `firestore:"subdomain" json:"subdomain"`
	OwnerID     string           `firestore:"owner_id" json:"owner_id"`
	Repository  RepositoryConfig `firestore:"repository" json:"repository"`
	BuildConfig BuildConfig      `firestore:"build_config" json:"build_config"`
	Status      ProjectStatus    `firestore:"status" json:"status"`
	CreatedAt   string           `firestore:"createdAt,omitempty" json:"createdAt,omitempty"`

	// Campos para compatibilidad con código existente durante la migración
	RepoName       string            `firestore:"repo_name" json:"repo_name"`
	ProjectType    string            `firestore:"project_type" json:"project_type"`
	AdvancedConfig map[string]string `firestore:"advanced_config" json:"advanced_config"`
}

type firestoreProjectRepo struct {
	client *firestore.Client
}

func NewProjectRepository(client *firestore.Client) ProjectRepository {
	return &firestoreProjectRepo{client: client}
}

func (r *firestoreProjectRepo) UpdateProjectStatus(ctx context.Context, userID, projectID, status string) error {
	_, err := r.client.Collection("users").Doc(userID).Collection("projects").Doc(projectID).Update(ctx, []firestore.Update{
		{Path: "status.state", Value: status},
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
		{Path: "status.state", Value: status},
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

func (r *firestoreProjectRepo) DeleteProject(ctx context.Context, userID, projectID string) error {
	projectRef := r.client.Collection("users").Doc(userID).Collection("projects").Doc(projectID)

	// 1. Borrar sub-colección de builds
	buildsIter := projectRef.Collection("builds").Documents(ctx)
	for {
		doc, err := buildsIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to iterate over builds: %w", err)
		}
		if _, err := doc.Ref.Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete build doc %s: %w", doc.Ref.ID, err)
		}
	}

	// 2. Borrar documento del proyecto
	if _, err := projectRef.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete project doc: %w", err)
	}

	// 3. Borrar el subdominio de la colección global "subdomains" si existe
	if _, err := r.client.Collection("subdomains").Doc(projectID).Delete(ctx); err != nil {
		log.Printf("Warning: failed to delete subdomain reservation for %s: %v", projectID, err)
	}

	return nil
}

func (r *firestoreProjectRepo) CheckSubdomainExistsGlobal(ctx context.Context, subdomain string) (bool, error) {
	// Realiza una consulta global en todas las subcolecciones "projects" para ver si el subdominio está tomado
	query := r.client.CollectionGroup("projects").Where("subdomain", "==", subdomain).Limit(1)
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return false, fmt.Errorf("failed to query global projects collection group: %w", err)
	}
	return len(docs) > 0, nil
}

