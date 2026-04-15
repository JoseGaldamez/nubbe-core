package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"cloud.google.com/go/storage"
	"github.com/gin-gonic/gin"
)

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
