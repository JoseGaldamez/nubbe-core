package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/storage"
	"github.com/gin-gonic/gin"
)

// LogResponse define la estructura limpia para el frontend
type LogResponse struct {
	Timestamp string `json:"timestamp"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
}

// ---------------------------------------------------------
// 1. EL LOG HUB (Sin cambios, lógica agnóstica a GCP)
// ---------------------------------------------------------
type LogHub struct {
	clients map[string][]chan LogResponse
	mu      sync.RWMutex
}

var Hub = &LogHub{
	clients: make(map[string][]chan LogResponse),
}

func (h *LogHub) Register(serviceName string) chan LogResponse {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan LogResponse, 100)
	h.clients[serviceName] = append(h.clients[serviceName], ch)
	return ch
}

func (h *LogHub) Unregister(serviceName string, ch chan LogResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	channels := h.clients[serviceName]
	for i, c := range channels {
		if c == ch {
			h.clients[serviceName] = append(channels[:i], channels[i+1:]...)
			close(c)
			break
		}
	}
}

// ---------------------------------------------------------
// 2. EL SUSCRIPTOR V2 (Corre en segundo plano)
// ---------------------------------------------------------

type PubSubLogEntry struct {
	TextPayload string          `json:"textPayload"`
	JSONPayload json.RawMessage `json:"jsonPayload"`
	Severity    string          `json:"severity"`
	Timestamp   string          `json:"timestamp"`
	Resource    struct {
		Labels map[string]string `json:"labels"`
	} `json:"resource"`
}

func StartPubSubSubscriber(projectID string) {
	ctx := context.Background()
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Error creando cliente PubSub v2: %v", err)
	}

	// [ACTUALIZACIÓN V2]: Ahora usamos Subscriber en lugar de Subscription
	sub := client.Subscriber("nubbe-logs-sub")

	log.Printf("[PubSub] Suscriptor v2 iniciado. Escuchando logs en tiempo real...")

	err = sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		msg.Ack() // Confirmar a GCP

		var entry PubSubLogEntry
		if err := json.Unmarshal(msg.Data, &entry); err != nil {
			log.Printf("[PubSub] Error parseando log: %v", err)
			return
		}

		serviceName := entry.Resource.Labels["service_name"]

		Hub.mu.RLock()
		channels, ok := Hub.clients[serviceName]
		Hub.mu.RUnlock()

		if ok && len(channels) > 0 {
			var message string
			if entry.TextPayload != "" {
				message = entry.TextPayload
			} else if len(entry.JSONPayload) > 0 {
				message = string(entry.JSONPayload)
			} else {
				message = "Log sin payload visible"
			}

			res := LogResponse{
				Timestamp: entry.Timestamp,
				Severity:  entry.Severity,
				Message:   message,
			}

			Hub.mu.RLock()
			for _, ch := range channels {
				select {
				case ch <- res:
				default:
				}
			}
			Hub.mu.RUnlock()
		}
	})

	if err != nil {
		log.Printf("[PubSub] Error en la recepción de mensajes v2: %v", err)
	}
}

// ---------------------------------------------------------
// 3. EL HANDLER (Sin cambios)
// ---------------------------------------------------------

func StreamLogs(c *gin.Context) {
	serviceName := c.Query("serviceName")

	if serviceName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "serviceName is required"})
		return
	}

	w := c.Writer
	r := c.Request
	ctx := r.Context()

	// 1. Configuración de Headers (Incluyendo el salvavidas para Cloud Run)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // CRÍTICO para que Cloud Run no bloquee el stream

	flusher, ok := w.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
		return
	}

	// 2. Forzamos el envío de los encabezados al navegador INMEDIATAMENTE
	// Esto inicializa la conexión en el frontend antes de enviar datos
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// 3. Registramos este cliente en el Hub
	logChan := Hub.Register(serviceName)
	log.Printf("[SSE] Cliente conectado a %s", serviceName)

	defer func() {
		Hub.Unregister(serviceName, logChan)
		log.Printf("[SSE] Cliente desconectado de %s", serviceName)
	}()

	// 4. Inyectamos el mensaje directamente AL CANAL
	// Al mandarlo por aquí, el bucle de abajo lo procesará como un log real
	logChan <- LogResponse{
		Timestamp: time.Now().Format(time.RFC3339),
		Severity:  "INFO",
		Message:   "Conexión ultra-rápida establecida. Esperando actividad...",
	}

	// 5. Bucle pasivo de transmisión
	for {
		select {
		case <-ctx.Done():
			return
		case logMsg := <-logChan:
			// Todos los mensajes (incluyendo la bienvenida) pasan por aquí
			respJSON, _ := json.Marshal(logMsg)
			fmt.Fprintf(w, "data: %s\n\n", respJSON)
			flusher.Flush()
		}
	}
}

// GetBuildLogs actúa como un proxy para leer los logs de Cloud Build desde GCS.
func GetBuildLogs(c *gin.Context) {
	buildID := c.Param("buildId")
	if buildID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "buildId is required"})
		return
	}

	bucketName := os.Getenv("LOGS_BUCKET")
	if bucketName == "" {
		bucketName = "nubbe-build-logs" // Fallback por si no está en env
	}

	if StorageClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage client not initialized"})
		return
	}

	objectName := fmt.Sprintf("log-%s.txt", buildID)
	rc, err := StorageClient.Bucket(bucketName).Object(objectName).NewReader(c.Request.Context())
	if err != nil {
		if err == storage.ErrObjectNotExist {
			// Manejo de error amigable solicitado
			c.String(http.StatusOK, "Iniciando entorno de compilación. Esperando logs...")
			return
		}
		log.Printf("Error leyendo log de GCS: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read logs from storage"})
		return
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		log.Printf("Error leyendo contenido del log: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process log content"})
		return
	}

	c.String(http.StatusOK, string(content))
}
