package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	// En producción (Cloud Run), queremos el modo release
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.Default()

	// Ruta de Health Check (esencial para que GCP sepa que tu contenedor está vivo)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "operational", "system": "nubbe-core"})
	})

	// Grupo de rutas para la API interna (llamadas desde tu frontend de React)
	api := router.Group("/api/v1")
	{
		// Aquí irán las rutas protegidas, ej:
		// api.Use(auth.FirebaseMiddleware())
		api.POST("/projects/initialize", handleInitializeProject)
	}

	// Grupo de rutas para Webhooks externos (llamadas desde GitHub)
	webhooks := router.Group("/webhooks")
	{
		webhooks.POST("/github", handleGitHubWebhook)
	}

	// Configuración del puerto inyectado por Cloud Run
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Nubbe Core inicializado. Escuchando en el puerto %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Error al iniciar el servidor: %v", err)
	}
}

// Stubs temporales para las funciones de los handlers
func handleInitializeProject(c *gin.Context) {
	c.JSON(http.StatusAccepted, gin.H{"message": "Project initialization queued"})
}

func handleGitHubWebhook(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Webhook received"})
}
