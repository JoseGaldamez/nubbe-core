package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	firebase "firebase.google.com/go/v4"
	"github.com/JoseGaldamez/nubbe-core/internal/handlers"
	"github.com/JoseGaldamez/nubbe-core/internal/middleware"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {

	// -------------------------------------------------- Load Environment Variables -------------------------------------------------------------

	// Intentar cargar .env. Si falla, solo logueamos (útil para producción en Cloud Run)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// Validación estricta de variables de entorno requeridas
	requiredVars := []string{
		"GCP_PROJECT_ID",
		"WEBHOOK_AUDIENCE",
		"PUBSUB_SERVICE_ACCOUNT_EMAIL",
	}

	for _, v := range requiredVars {
		if os.Getenv(v) == "" {
			log.Fatalf("CRITICAL: La variable de entorno %s es obligatoria y no está definida", v)
		}
	}

	gcpProjectID := os.Getenv("GCP_PROJECT_ID")

	// Initialize Firestore
	ctx := context.Background()
	if err := handlers.InitFirestore(ctx, gcpProjectID); err != nil {
		log.Fatalf("Error inicializando Firestore: %v", err)
	}

	// Initialize Storage
	if err := handlers.InitStorage(ctx); err != nil {
		log.Fatalf("Error inicializando Storage: %v", err)
	}

	// Initialize Firebase
	app, err := firebase.NewApp(ctx, nil)
	if err != nil {
		log.Fatalf("Error inicializando Firebase: %v", err)
	}

	authClient, err := app.Auth(ctx)
	if err != nil {
		log.Fatalf("Error obteniendo cliente Auth: %v", err)
	}

	log.Println("Firebase inicializado correctamente")

	// ---------------------------------------------------- Initialize Server --------------------------------------------------------------------

	// En producción (Cloud Run), queremos el modo release
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.Default()

	// 1. Configuración de CORS
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "https://app.nubbe.run"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// ---------------------------------------------------- Initialize Routes --------------------------------------------------------------------

	// Ruta de Health Check (esencial para que GCP sepa que tu contenedor está vivo)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "operational", "system": "nubbe-core"})
	})

	// Grupo de rutas para la API interna (llamadas desde tu frontend de React)
	api := router.Group("/api/v1")

	// Middleware de Firebase
	api.Use(middleware.FirebaseAuthMiddleware(authClient))
	{
		// Aquí irán las rutas protegidas, ej:
		// api.Use(auth.FirebaseMiddleware())
		api.POST("/projects/initialize", handlers.HandleCreateProject)
		api.GET("/projects/:projectId/builds/:buildId/logs", handlers.GetBuildLogs)
		api.GET("/logs/stream", handlers.StreamLogs)
	}

	// Grupo de rutas para Webhooks externos (llamadas desde GitHub)
	webhooks := router.Group("/webhooks")
	{
		webhooks.POST("/github", handlers.HandleGitHubWebhook)

		// Webhook de Cloud Build (vía Pub/Sub) con seguridad OIDC
		audience := os.Getenv("WEBHOOK_AUDIENCE")
		saEmail := os.Getenv("PUBSUB_SERVICE_ACCOUNT_EMAIL")
		webhooks.POST("/cloudbuild", middleware.GoogleOIDCMiddleware(audience, saEmail), handlers.HandleCloudBuildWebhook)
	}

	// ---------------------------------------------------- Run Server ---------------------------------------------------------------------------

	// Configuración del puerto inyectado por Cloud Run
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Nubbe Core inicializado. Escuchando en el puerto %s", port)
	go handlers.StartPubSubSubscriber(gcpProjectID)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Error al iniciar el servidor: %v", err)
	}
}
