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

	log.Println("Infraestructura inicializada correctamente")

	// 3. Initialize Repositories and Services
	userRepo := repository.NewUserRepository(fsClient)
	projectRepo := repository.NewProjectRepository(fsClient)
	projectService := service.NewProjectService(userRepo, projectRepo, cfg.AESEncryptionKey)

	// 4. Setup Application and Router
	appDeps := handlers.NewApp(fsClient, storageClient, authClient, projectService)
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

	// Start PubSub Subscriber
	go handlers.StartPubSubSubscriber(cfg.GCPProjectID)

	// 6. Signal Handling
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Iniciando apagado ordenado...")

	ctxShutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctxShutdown); err != nil {
		log.Fatalf("Apagado forzado del servidor: %v", err)
	}

	log.Println("Esperando tareas en segundo plano...")
	appDeps.WG.Wait()

	fsClient.Close()
	storageClient.Close()

	log.Println("Nubbe Core se ha detenido correctamente.")
}
