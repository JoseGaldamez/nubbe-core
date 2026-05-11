package handlers

import (
	"log"
	"net/http"

	"github.com/JoseGaldamez/nubbe-core/internal/pkg/pubsub"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Estructura del trabajo a realizar
type JobPayload struct {
	Action   string `json:"action"`
	AppID    string `json:"appId"`
	RepoName string `json:"repoName"`
	UserID   string `json:"userId"`
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

	// 2. Publish deletion job to PubSub via pre-initialized client
	job := JobPayload{
		Action:   "delete_project",
		AppID:    appID,
		RepoName: projectDetails.RepoName,
		UserID:   userID,
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
