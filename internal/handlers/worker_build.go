package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// BuildEventPayload represents the data sent to the build worker via Pub/Sub.
type BuildEventPayload struct {
	UserID         string            `json:"user_id"`
	ProjectID      string            `json:"project_id"`
	RepoName       string            `json:"repo_name"`
	ProjectType    string            `json:"project_type"`
	GithubToken    string            `json:"github_token"`
	EntryPoint     string            `json:"entry_point"`
	EnvVars        map[string]string `json:"env_vars"`
	AdvancedConfig map[string]string `json:"advanced_config"`
	Action         string            `json:"action"` // INITIAL_BUILD or PUSH_BUILD
	Branch         string            `json:"branch"`
	AuditUpdate    bool              `json:"audit_update"`
}

// JobWorkerBuildProject handles build events from Pub/Sub.
func (app *App) JobWorkerBuildProject(c *gin.Context) {
	var pushReq PubSubPushRequest
	if err := c.ShouldBindJSON(&pushReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
		return
	}

	// Decode Base64 data from Pub/Sub
	decodedData, err := base64.StdEncoding.DecodeString(pushReq.Message.Data)
	if err != nil {
		log.Printf("[BuildWorker] Error decoding Base64: %v", err)
		c.Status(http.StatusOK)
		return
	}

	var payload BuildEventPayload
	if err := json.Unmarshal(decodedData, &payload); err != nil {
		log.Printf("[BuildWorker] Error decoding JSON: %v. Payload: %s", err, string(decodedData))
		c.Status(http.StatusOK)
		return
	}

	log.Printf("[BuildWorker] Starting %s for project %s (User: %s)", payload.Action, payload.ProjectID, payload.UserID)

	// Trigger the build via Service
	// We use the existing TriggerBuild which uses the Strategy pattern with builders
	info, err := app.ProjectService.TriggerBuild(
		c.Request.Context(),
		payload.UserID,
		payload.ProjectID,
		payload.RepoName,
		payload.ProjectType,
		payload.GithubToken,
		payload.EntryPoint,
		payload.EnvVars,
		payload.AdvancedConfig,
	)

	if err != nil {
		log.Printf("[Error Crítico][BuildWorker] Fallo al disparar build para %s: %v", payload.ProjectID, err)
		// Update status to FAILED in Firestore
		if updateErr := app.ProjectService.UpdateProjectStatus(c.Request.Context(), payload.UserID, payload.ProjectID, "FAILED"); updateErr != nil {
			log.Printf("[BuildWorker] No se pudo actualizar estado a FAILED para %s: %v", payload.ProjectID, updateErr)
		}

		// Enviar un log amigable al Hub para feedback en tiempo real si el usuario está escuchando
		Hub.mu.RLock()
		channels, ok := Hub.clients[payload.ProjectID]
		Hub.mu.RUnlock()
		if ok {
			res := LogResponse{
				Timestamp: time.Now().Format(time.RFC3339),
				Severity:  "ERROR",
				Message:   fmt.Sprintf("Error al iniciar el despliegue: %v", err),
			}
			for _, ch := range channels {
				select {
				case ch <- res:
				default:
				}
			}
		}

		c.Status(http.StatusOK) // ACK para evitar bucles de reintento en Pub/Sub
		return
	}

	log.Printf("[BuildWorker] Build triggered successfully for %s. BuildID: %s", payload.ProjectID, info.BuildID)

	// Inicializar el documento del build en Firestore para que la UI pueda suscribirse inmediatamente
	if info.BuildID != "" {
		initialMsg := "Compilación en cola"
		if err := app.ProjectService.UpdateStatus(c.Request.Context(), payload.UserID, payload.ProjectID, info.BuildID, "QUEUED", "", initialMsg); err != nil {
			log.Printf("[BuildWorker] Advertencia: No se pudo crear el registro inicial de build en Firestore para %s: %v", payload.ProjectID, err)
		}
	} else {
		log.Printf("[BuildWorker] Advertencia: BuildID vacío tras iniciar compilación para %s", payload.ProjectID)
	}

	c.Status(http.StatusOK)
}
