package handlers

import (
	"log"
	"net/http"

	"github.com/JoseGaldamez/nubbe-core/internal/builders"
	"github.com/JoseGaldamez/nubbe-core/internal/pkg/pubsub"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Estructura del trabajo a realizar
type JobPayload struct {
	Action           string `json:"action"`
	AppID            string `json:"appId"`
	RepoName         string `json:"repoName"`
	UserID           string `json:"userId"`
	PagesProjectName string `json:"pagesProjectName,omitempty"`
}

func (app *App) DeleteProjectAsync(c *gin.Context) {
	appID := c.Param("id")
	userID := c.GetString("user_id")

	// 0. Check si hay un usuario logueado
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// 1. Obtener detalles del proyecto vía Service
	projectDetails, err := app.ProjectService.GetProjectDetails(c.Request.Context(), userID, appID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "El proyecto no existe o no te pertenece"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error consultando la base de datos", "details": err.Error()})
		return
	}

	if projectDetails.RepoName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Repo Name not found in project"})
		return
	}

	// 1.1. Actualizar estado a DELETING en Firestore inmediatamente
	log.Printf("[Delete] Actualizando estado a DELETING para el proyecto %s en Firestore", appID)
	if errStatus := app.ProjectService.UpdateProjectStatus(c.Request.Context(), userID, appID, "DELETING"); errStatus != nil {
		log.Printf("[Delete] Warning: No se pudo cambiar el estado a DELETING para el proyecto %s: %v", appID, errStatus)
	}

	var pagesProjectName string

	// 1.2. Corte de tráfico instantáneo: Borrar ruta de Cloudflare KV de forma síncrona
	cfAccountID, cfToken, cfKVNamespace, cfErr := builders.GetCloudflareCredentials()
	if cfErr == nil && cfAccountID != "" && cfToken != "" && cfKVNamespace != "" {
		// Obtener valor de KV para extraer el nombre real de Cloudflare Pages
		kvValue, errKVGet := builders.GetRouteInKV(c.Request.Context(), cfAccountID, cfToken, cfKVNamespace, appID)
		if errKVGet == nil && kvValue != "" {
			pagesProjectName = builders.ExtractPagesProjectName(kvValue)
			log.Printf("[Delete] Nombre de proyecto Pages obtenido desde KV: %s", pagesProjectName)
		} else {
			log.Printf("[Delete] No se pudo obtener ruta de KV (%v) o estaba vacía. Se usará nombre autogenerado.", errKVGet)
		}

		log.Printf("[Delete] Borrando ruta de Cloudflare KV para subdominio: %s", appID)
		if errKV := builders.DeleteRouteInKV(c.Request.Context(), cfAccountID, cfToken, cfKVNamespace, appID); errKV != nil {
			log.Printf("[Delete] Warning: No se pudo borrar la ruta en Cloudflare KV para %s: %v", appID, errKV)
		} else {
			log.Printf("[Delete] Ruta KV para %s.nubbe.run eliminada con éxito", appID)
		}
	} else {
		log.Printf("[Delete] Warning: Omitiendo borrado de KV, credenciales de Cloudflare incompletas o erróneas: %v", cfErr)
	}

	// Si no pudimos leer de KV, usar el nombre generado como fallback
	if pagesProjectName == "" {
		pagesProjectName = builders.GenerateCFProjectName(appID, userID)
		log.Printf("[Delete] Fallback: Usando nombre de proyecto Pages generado: %s", pagesProjectName)
	}

	// 1.3. Liberar subdominio instantáneamente en Firestore (colección global subdomains)
	log.Printf("[Delete] Liberando subdominio %s en Firestore (colección subdomains)", appID)
	if _, errSub := app.Firestore.Collection("subdomains").Doc(appID).Delete(c.Request.Context()); errSub != nil {
		log.Printf("[Delete] Warning: No se pudo eliminar la reserva del subdominio %s en Firestore: %v", appID, errSub)
	} else {
		log.Printf("[Delete] Reserva del subdominio %s eliminada de Firestore", appID)
	}


	// 2. Publish deletion job to PubSub via pre-initialized client
	job := JobPayload{
		Action:           "delete_project",
		AppID:            appID,
		RepoName:         projectDetails.RepoName,
		UserID:           userID,
		PagesProjectName: pagesProjectName,
	}

	msgID, err := app.PubSub.Publish(c.Request.Context(), pubsub.JobsTopic, job)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Fallo al encolar la tarea de destrucción", "details": err.Error()})
		return
	}

	log.Printf("[Delete] Proyecto %s encolado para destrucción (msgID: %s)", appID, msgID)

	// 3. Responder al frontend INMEDIATAMENTE
	c.JSON(http.StatusOK, gin.H{
		"status":  "processing",
		"message": "Proyecto encolado para destrucción segura en segundo plano.",
		"msg_id":  msgID,
	})
}

