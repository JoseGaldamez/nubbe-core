package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"cloud.google.com/go/firestore"
	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PubSubPushRequest struct {
	Message struct {
		Data string `json:"data"`
	} `json:"message"`
}

func JobWorkerDeleteProject(c *gin.Context) {
	var pushReq PubSubPushRequest
	if err := c.ShouldBindJSON(&pushReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Payload inválido"})
		return
	}

	// Decodificar el Base64 que envía Google Pub/Sub
	decodedData, err := base64.StdEncoding.DecodeString(pushReq.Message.Data)
	if err != nil {
		log.Printf("[Worker] Error decodificando Base64: %v", err)
		c.Status(http.StatusOK) // ACK para no reintentar un mensaje corrupto
		return
	}

	// Decodificar los bytes limpios hacia tu struct de Go
	var job JobPayload
	if err := json.Unmarshal(decodedData, &job); err != nil {
		log.Printf("[Worker] Error crítico decodificando JSON: %v. Payload: %s", err, string(decodedData))
		c.Status(http.StatusOK)
		return
	}

	gcpProjectID := os.Getenv("GCP_PROJECT_ID")
	location := "us-central1"
	artifactRegistryRepoName := "nubbe-repo"

	if job.Action == "delete_project" {
		log.Printf("[Worker] Iniciando destrucción de %s", job.AppID)

		if FsClient == nil {
			log.Println("[Worker] Error: Firestore client not initialized")
			// Devolvemos 500 para que PubSub SÍ reintente, porque esto es un fallo de nuestro servidor, no del usuario.
			c.Status(http.StatusInternalServerError)
			return
		}

		dsnap, err := FsClient.Collection("users").Doc(job.UserID).Get(c.Request.Context())
		if err != nil {
			log.Printf("[Worker] Failed to fetch user data para %s: %v", job.UserID, err)
			c.Status(http.StatusOK) // ACK para descartar (error permanente)
			return
		}

		var userData struct {
			GithubAccessToken string `firestore:"githubAccessToken"`
		}
		if err := dsnap.DataTo(&userData); err != nil {
			log.Printf("[Worker] Failed to parse user data para %s: %v", job.UserID, err)
			c.Status(http.StatusOK)
			return
		}

		statusChanged, errorStatus := FsClient.Collection("users").Doc(job.UserID).Collection("projects").Doc(job.AppID).Update(c.Request.Context(), []firestore.Update{
			{Path: "status", Value: "deleting"},
		})
		if errorStatus != nil {
			log.Printf("[Worker] Failed to update status para %s: %v", job.UserID, errorStatus)
			c.Status(http.StatusOK)
			return
		}

		log.Printf("[Worker] Status changed para %s: %v", job.UserID, statusChanged)

		// ----------------------------------------- Ahora sí a borrar -----------------------------------------

		// 1. Borrar Webhook de GitHub
		if userData.GithubAccessToken != "" {
			errGH := DeleteGitHubWebhook(c.Request.Context(), job.UserID, job.AppID, job.RepoName, userData.GithubAccessToken)
			if errGH != nil {
				log.Printf("[Worker] Advertencia en GitHub Webhook: %v", errGH)
			}
		} else {
			log.Printf("[Worker] Advertencia: GitHub Access Token vacío. Omitiendo borrado de webhook.")
		}

		// 2. Borrar Servicio de Cloud Run
		errRun := deleteCloudRunService(c.Request.Context(), gcpProjectID, location, job.AppID)
		if errRun != nil {
			log.Printf("[Worker] Advertencia borrando Cloud Run: %v", errRun)
		}

		// 3. Borrar imágenes de Artifact Registry
		errAtr := cleanArtifactRegistry(c.Request.Context(), gcpProjectID, location, artifactRegistryRepoName, job.AppID)
		if errAtr != nil {
			log.Printf("[Worker] Advertencia limpiando Artifacts: %v", errAtr)
		}

		// 4. Borrar de Firebase (Firestore)
		errDb := deleteFirebaseDoc(c.Request.Context(), job.UserID, job.AppID)
		if errDb != nil {
			log.Printf("[Worker] Advertencia borrando registro en DB: %v", errDb)
			// No retornamos error aquí para permitir que el proceso termine con un 200 OK
		}

		log.Printf("[Worker] Destrucción de %s completada al 100%%", job.AppID)
	}

	c.Status(http.StatusOK)
}

// Función auxiliar para purgar Artifact Registry con manejo inteligente de 404
func cleanArtifactRegistry(ctx context.Context, projectID, location, artifactRegistryRepoName, packageName string) error {
	client, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("error inicializando cliente: %v", err)
	}
	defer client.Close()

	pkgPath := "projects/" + projectID + "/locations/" + location + "/repositories/" + artifactRegistryRepoName + "/packages/" + packageName

	req := &artifactregistrypb.DeletePackageRequest{
		Name: pkgPath,
	}

	op, err := client.DeletePackage(ctx, req)
	if err != nil {
		// TRATAMIENTO DE ERRORES INTELIGENTE: Ignorar si ya no existe
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			log.Printf("[ArtifactRegistry] El paquete %s ya no existe. Omitiendo...", packageName)
			return nil
		}
		return fmt.Errorf("error al enviar comando de destrucción: %v", err)
	}

	return op.Wait(ctx)
}

func deleteCloudRunService(ctx context.Context, projectID, location, serviceName string) error {
	client, err := run.NewServicesClient(ctx)
	if err != nil {
		return fmt.Errorf("error inicializando cliente de Cloud Run: %v", err)
	}
	defer client.Close()

	servicePath := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, location, serviceName)

	req := &runpb.DeleteServiceRequest{
		Name: servicePath,
	}

	log.Printf("[CloudRun] Solicitando destrucción del servicio: %s", serviceName)

	op, err := client.DeleteService(ctx, req)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			log.Printf("[CloudRun] El servicio %s ya no existe. Omitiendo...", serviceName)
			return nil
		}
		return fmt.Errorf("error al enviar comando de destrucción a Cloud Run: %v", err)
	}

	log.Printf("[CloudRun] Esperando apagado de contenedores para %s...", serviceName)
	_, err = op.Wait(ctx)
	if err != nil {
		return fmt.Errorf("error esperando la confirmación de borrado de Cloud Run: %v", err)
	}

	log.Printf("[CloudRun] Servicio %s apagado y destruido exitosamente.", serviceName)
	return nil
}

// deleteFirebaseDoc elimina el registro de la aplicación de Firestore.
func deleteFirebaseDoc(ctx context.Context, userId, appID string) error {
	// Usamos el cliente global FsClient que ya tienes inicializado
	log.Printf("[Firestore] Solicitando eliminación del documento: %s", appID)

	_, err := FsClient.Collection("users").Doc(userId).Collection("projects").Doc(appID).Delete(ctx)
	if err != nil {
		// Si el documento ya no existe, lo consideramos un éxito (Idempotencia)
		if status.Code(err) == codes.NotFound {
			log.Printf("[Firestore] El documento %s ya no existe. Omitiendo...", appID)
			return nil
		}
		return fmt.Errorf("error al eliminar documento de Firestore: %v", err)
	}

	log.Printf("[Firestore] Documento %s eliminado de la colección %s.", appID, userId)
	return nil
}
