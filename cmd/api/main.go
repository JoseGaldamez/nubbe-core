package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	firebase "firebase.google.com/go/v4"
	"github.com/JoseGaldamez/nubbe-core/internal/config"
	"github.com/JoseGaldamez/nubbe-core/internal/handlers"
	"github.com/JoseGaldamez/nubbe-core/internal/pkg/pubsub"
	"github.com/JoseGaldamez/nubbe-core/internal/repository"
	"github.com/JoseGaldamez/nubbe-core/internal/service"
)

func main() {
	// 1. Load Configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("CRITICAL: Error al cargar configuración: %v", err)
	}

	// 2. Initialize Infrastructure Clients
	ctx := context.Background()
	fsClient, err := handlers.InitFirestore(ctx, cfg.GCPProjectID)
	if err != nil {
		log.Fatalf("Error inicializando Firestore: %v", err)
	}

	storageClient, err := handlers.InitStorage(ctx)
	if err != nil {
		log.Fatalf("Error inicializando Storage: %v", err)
	}

	firebaseApp, err := firebase.NewApp(ctx, nil)
	if err != nil {
		log.Fatalf("Error inicializando Firebase: %v", err)
	}

	authClient, err := firebaseApp.Auth(ctx)
	if err != nil {
		log.Fatalf("Error obteniendo cliente Auth: %v", err)
	}

	psClient, err := pubsub.NewClient(ctx, cfg.GCPProjectID)
	if err != nil {
		log.Fatalf("Error inicializando PubSub: %v", err)
	}

	log.Println("Infraestructura inicializada correctamente")

	// 3. Initialize Repositories and Services
	userRepo := repository.NewUserRepository(fsClient)
	projectRepo := repository.NewProjectRepository(fsClient)
	projectService := service.NewProjectService(userRepo, projectRepo, cfg.AESEncryptionKey)

	// 4. Setup Application and Router
	appDeps := handlers.NewApp(fsClient, storageClient, authClient, projectService, psClient, cfg.PaddleWebhookSecret, cfg.PaddleHobbyPriceID, cfg.PaddleProPriceID, cfg.PaddleEnvironment)
	router := appDeps.InitRouter(cfg.WebhookAudience, cfg.PubSubServiceAccountEmail, cfg.JobsAudience)

	// 5. Start Server with Graceful Shutdown support
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		log.Printf("Nubbe Core escuchando en el puerto %s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error al iniciar el servidor: %v", err)
		}
	}()

	// 6. Signal Handling
	quit := make(chan os.Signal, 1)
	// SIGINT (Ctrl+C), SIGTERM (Docker/K8s termination)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	
	sig := <-quit
	log.Printf("Señal recibida: %v. Iniciando apagado ordenado...", sig)

	// Contexto con timeout para el apagado (darle tiempo a peticiones activas)
	ctxShutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctxShutdown); err != nil {
		log.Printf("Error durante el apagado del servidor: %v", err)
	}

	log.Println("Servidor HTTP detenido. Esperando finalización de tareas asíncronas (WaitGroup)...")
	
	// Canal para esperar el WaitGroup con timeout
	waitFinished := make(chan struct{})
	go func() {
		appDeps.WG.Wait()
		close(waitFinished)
	}()

	select {
	case <-waitFinished:
		log.Println("Todas las tareas asíncronas han finalizado.")
	case <-ctxShutdown.Done():
		log.Println("Timeout alcanzado esperando tareas asíncronas. Forzando cierre.")
	}

	// Cerrar clientes de infraestructura
	if err := fsClient.Close(); err != nil {
		log.Printf("Error cerrando Firestore: %v", err)
	}
	if err := storageClient.Close(); err != nil {
		log.Printf("Error cerrando Storage: %v", err)
	}
	if err := psClient.Close(); err != nil {
		log.Printf("Error cerrando PubSub: %v", err)
	}

	log.Println("Nubbe Core se ha detenido correctamente.")
}
