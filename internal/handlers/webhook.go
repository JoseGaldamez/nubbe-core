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

// getFriendlyMessage mapea el estado de Cloud Build a un mensaje amigable para el usuario.
func getFriendlyMessage(status string) string {
	switch status {
	case "QUEUED":
		return "Preparando entorno de compilación..."
	case "WORKING":
		return "Compilando código y generando imagen..."
	case "SUCCESS":
		return "Imagen generada y desplegada en Cloud Run."
	case "FAILURE", "INTERNAL_ERROR", "TIMEOUT":
		return "Error en la compilación. Revisa el log adjunto."
	default:
		return "Estado del build actualizado: " + status
	}
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
	logURL := build.LogUrl

	// Extraer projectId directamente del objeto Cloud Build (Substitutions)
	projectId, ok := build.Substitutions["_PROJECT_ID"]
	if !ok {
		log.Println("Substitución _PROJECT_ID no encontrada en el objeto Cloud Build, ignorando...")
		c.JSON(http.StatusOK, gin.H{"message": "projectId missing in substitutions, ignoring"})
		return
	}

	// Extraer userId directamente del objeto Cloud Build (Substitutions)
	userID, ok := build.Substitutions["_USER_ID"]
	if !ok {
		log.Println("Substitución _USER_ID no encontrada en el objeto Cloud Build, ignorando...")
		c.JSON(http.StatusOK, gin.H{"message": "_USER_ID missing in substitutions, ignoring"})
		return
	}

	log.Printf("Procesando Build: %s, Status: %s, ProjectId: %s, UserId: %s", buildID, status, projectId, userID)

	// Actualizar Firestore (incluyendo sub-colección de builds e historial)
	if err := updateProjectStatus(c.Request.Context(), userID, projectId, buildID, status, logURL); err != nil {
		log.Printf("Error actualizando Firestore: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// updateProjectStatus actualiza el historial y estado de un build en la sub-colección del proyecto.
func updateProjectStatus(ctx context.Context, userID, projectID, buildID, status, logURL string) error {
	if FsClient == nil {
		return fmt.Errorf("firestore client no inicializado")
	}

	now := time.Now()
	friendlyMsg := getFriendlyMessage(status)

	// Crear entrada de historial para ArrayUnion
	historyEntry := map[string]interface{}{
		"status":    status,
		"message":   friendlyMsg,
		"timestamp": now,
	}

	// Referencia al documento del build dentro de la ruta jerárquica:
	// users/{userId}/projects/{projectID}/builds/{buildID}
	buildRef := FsClient.Collection("users").Doc(userID).
		Collection("projects").Doc(projectID).
		Collection("builds").Doc(buildID)

	// Usamos Set con MergeAll para crear el documento si no existe o actualizar campos específicos
	_, err := buildRef.Set(ctx, map[string]interface{}{
		"buildId":   buildID,
		"status":    status,
		"logUrl":    logURL,
		"updatedAt": now,
		"history":   firestore.ArrayUnion(historyEntry),
	}, firestore.MergeAll)

	return err
}
