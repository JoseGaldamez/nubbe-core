package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"

	"github.com/JoseGaldamez/nubbe-core/internal/pkg/utils"
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
func (app *App) HandleCloudBuildWebhook(c *gin.Context) {
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

	projectType, ok := build.Substitutions["_PROJECT_TYPE"]
	if !ok {
		projectType = "unknown"
	}

	log.Printf("Procesando Build: %s, Status: %s, ProjectId: %s, UserId: %s, ProjectType: %s", buildID, status, projectId, userID, projectType)

	// Actualizar estado vía Service
	friendlyMsg := getFriendlyMessage(status)
	if err := app.ProjectService.UpdateStatus(c.Request.Context(), userID, projectId, buildID, status, logURL, friendlyMsg); err != nil {
		log.Printf("Error actualizando Firestore vía Service: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
		return
	}

	if status == "SUCCESS" && projectType == "nodejs" {
		// Lo hacemos asíncrono usando context.Background() para no bloquear la respuesta HTTP
		// y evitar que el contexto de Gin se cancele al terminar la petición.
		go func(subDomain string) {
			ctxBg := context.Background()

			// Obtenemos la URL del contenedor de Google
			cloudRunURL, err := utils.FetchCloudRunURL(ctxBg, subDomain)
			if err != nil {
				log.Printf("[Error Crítico] No se pudo obtener la URL de Cloud Run para %s: %v", subDomain, err)
				return
			}

			// Lo registramos en Cloudflare
			if err := utils.RegisterInCloudflareKV(ctxBg, subDomain, cloudRunURL); err != nil {
				log.Printf("[Error Crítico] No se pudo registrar %s en KV: %v", subDomain, err)
				return
			}

			log.Printf("[Éxito] Enrutamiento KV configurado: %s.nubbe.run -> %s", subDomain, cloudRunURL)
		}(projectId) // Pasamos projectId (que es el subDomain)
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
