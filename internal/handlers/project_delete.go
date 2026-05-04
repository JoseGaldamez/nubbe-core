package handlers

import (
	"encoding/json"
	"net/http"

	"cloud.google.com/go/pubsub/v2"
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

	// 2. Conectar a Pub/Sub y publicar la orden
	ctx := c.Request.Context() // Es mejor usar el contexto de la petición HTTP
	client, errClient := pubsub.NewClient(ctx, "nubbe-run")
	if errClient != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error conectando a infraestructura de colas"})
		return
	}
	// ¡CRÍTICO! Prevenir fugas de memoria cerrando el cliente al terminar
	defer client.Close()

	publisher := client.Publisher("nubbe-jobs")

	payload, _ := json.Marshal(JobPayload{
		Action:   "delete_project",
		AppID:    appID,
		RepoName: projectDetails.RepoName,
		UserID:   userID,
	})

	// Publicar asíncronamente
	result := publisher.Publish(ctx, &pubsub.Message{Data: payload})

	_, errPubSub := result.Get(ctx)
	if errPubSub != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Fallo al encolar la tarea de destrucción", "data": errPubSub.Error()})
		return
	}

	// 3. Responder al frontend INMEDIATAMENTE
	c.JSON(http.StatusOK, gin.H{
		"status":  "processing",
		"message": "Proyecto encolado para destrucción segura en segundo plano.",
	})
}
