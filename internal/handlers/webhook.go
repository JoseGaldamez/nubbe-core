package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/cloudbuild/v1"
)

var fsClient *firestore.Client

// InitFirestore inicializa el cliente global de Firestore.
func InitFirestore(ctx context.Context, gcpProjectID string) error {
	client, err := firestore.NewClient(ctx, gcpProjectID)
	if err != nil {
		return fmt.Errorf("error al crear el cliente de Firestore: %w", err)
	}
	fsClient = client
	return nil
}

// PubSubMessage representa el cuerpo de la petición que envía Pub/Sub.
type PubSubMessage struct {
	Message struct {
		Data       string            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
}

// HandleGitHubWebhook handles the GitHub webhook requests.
func HandleGitHubWebhook(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Webhook received"})
}

// HandleCloudBuildWebhook procesa las notificaciones de estado de Cloud Build.
func HandleCloudBuildWebhook(c *gin.Context) {
	var psMsg PubSubMessage
	if err := c.ShouldBindJSON(&psMsg); err != nil {
		log.Printf("Error decodificando mensaje Pub/Sub: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Pub/Sub message"})
		return
	}

	// Decodificar el campo data (Base64)
	data, err := base64.StdEncoding.DecodeString(psMsg.Message.Data)
	if err != nil {
		log.Printf("Error decodificando Base64: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid base64 data"})
		return
	}

	// Unmarshal a la estructura de Cloud Build
	var build cloudbuild.Build
	if err := json.Unmarshal(data, &build); err != nil {
		log.Printf("Error parseando JSON de Cloud Build: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Cloud Build JSON"})
		return
	}

	// Extraer información requerida
	buildID := build.Id
	status := build.Status

	// Extraer projectId directamente del objeto Cloud Build (Substitutions)
	projectId, ok := build.Substitutions["_PROJECT_ID"]
	if !ok {
		log.Println("Substitución _PROJECT_ID no encontrada en el objeto Cloud Build, ignorando...")
		c.JSON(http.StatusOK, gin.H{"message": "projectId missing in substitutions, ignoring"})
		return
	}

	log.Printf("Procesando Build: %s, Status: %s, ProjectId: %s", buildID, status, projectId)

	// Actualizar Firestore
	if err := updateProjectStatus(c.Request.Context(), projectId, status); err != nil {
		log.Printf("Error actualizando Firestore: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// updateProjectStatus actualiza el documento del proyecto en Firestore.
func updateProjectStatus(ctx context.Context, projectID string, status string) error {
	if fsClient == nil {
		return fmt.Errorf("firestore client no inicializado")
	}

	// Asumiendo que la colección se llama "projects" y el ID del documento es el projectID enviado
	_, err := fsClient.Collection("projects").Doc(projectID).Update(ctx, []firestore.Update{
		{Path: "status", Value: status},
		{Path: "updatedAt", Value: time.Now()},
	})

	return err
}
