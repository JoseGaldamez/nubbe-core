package handlers

import (
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	firebaseAuth "firebase.google.com/go/v4/auth"
	"github.com/JoseGaldamez/nubbe-core/internal/middleware"
	"github.com/JoseGaldamez/nubbe-core/internal/pkg/pubsub"
	"github.com/JoseGaldamez/nubbe-core/internal/repository"
	"github.com/JoseGaldamez/nubbe-core/internal/service"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// App holds the dependencies for the application handlers.
type App struct {
	Firestore                *firestore.Client
	Storage                  *storage.Client
	Auth                     *firebaseAuth.Client
	ProjectService           *service.ProjectService
	PubSub                   *pubsub.Client
	PaymentRepo              repository.PaymentRepository
	WG                       sync.WaitGroup
	PaddleWebhookSecret      string
	PaddleAPIKey             string
	PaddleProductID          string
	PaddlePriceHobbyMonthly  string
	PaddlePriceProMonthly    string
	PaddlePriceHobbyAnnually string
	PaddlePriceProAnnually   string
	PaddleEnvironment        string
}

// NewApp creates a new App instance with the provided dependencies.
func NewApp(fs *firestore.Client, storage *storage.Client, auth *firebaseAuth.Client, projectService *service.ProjectService, pubsub *pubsub.Client, paymentRepo repository.PaymentRepository, paddleWebhookSecret, paddleAPIKey, paddleProductID, paddlePriceHobbyMonthly, paddlePriceProMonthly, paddlePriceHobbyAnnually, paddlePriceProAnnually, paddleEnvironment string) *App {
	return &App{
		Firestore:                fs,
		Storage:                  storage,
		Auth:                     auth,
		ProjectService:           projectService,
		PubSub:                   pubsub,
		PaymentRepo:              paymentRepo,
		PaddleWebhookSecret:      paddleWebhookSecret,
		PaddleAPIKey:             paddleAPIKey,
		PaddleProductID:          paddleProductID,
		PaddlePriceHobbyMonthly:  paddlePriceHobbyMonthly,
		PaddlePriceProMonthly:    paddlePriceProMonthly,
		PaddlePriceHobbyAnnually: paddlePriceHobbyAnnually,
		PaddlePriceProAnnually:   paddlePriceProAnnually,
		PaddleEnvironment:        paddleEnvironment,
	}
}

// InitRouter sets up the gin router with all routes and middlewares.
func (app *App) InitRouter(webhookAudience, saEmail, jobsAudience string) *gin.Engine {
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

	// Ruta de Health Check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "operational", "system": "nubbe-core"})
	})

	router.GET("/debug/pubsub", app.DebugPubSub)

	// Grupo de rutas para la API interna
	api := router.Group("/api/v1")
	api.Use(middleware.FirebaseAuthMiddleware(app.Auth))
	{
		// create proyect
		api.POST("/projects/initialize", middleware.SubscriptionLimitMiddleware(app.Firestore), app.HandleCreateProject)

		// delete proyect
		api.DELETE("/projects/:id", app.DeleteProjectAsync)

		// Logs
		api.GET("/projects/:projectId/builds/:buildId/logs", app.GetBuildLogs)
		api.GET("/logs/stream", app.StreamLogs)

		// Environment Variables
		api.GET("/projects/:projectId/env_vars", app.GetProjectEnvVars)
		api.POST("/projects/:projectId/env_vars", app.SaveProjectEnvVars)

		// Payments & Subscription Management
		api.GET("/user/payments", app.GetUserPayments)
		api.POST("/subscription/cancel", app.CancelUserSubscription)
	}


	// Grupo de rutas para Webhooks externos
	webhooks := router.Group("/webhooks")
	{
		webhooks.POST("/github", app.HandleGitHubWebhook)
		webhooks.POST("/cloudbuild", middleware.GoogleOIDCMiddleware(webhookAudience, saEmail), app.HandleCloudBuildWebhook)
		webhooks.POST("/paddle", app.HandlePaddleWebhook)
	}

	// Grupo de rutas para Workers internos (Jobs)
	workers := router.Group("/api/internal/jobs/worker")
	workers.Use(middleware.GoogleOIDCMiddleware(jobsAudience, saEmail))
	{
		workers.POST("/delete", app.JobWorkerDeleteProject)
		workers.POST("/build", app.JobWorkerBuildProject)
	}

	return router
}
