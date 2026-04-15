package handlers

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
)

var (
	FsClient      *firestore.Client
	StorageClient *storage.Client
)

// InitFirestore inicializa el cliente global de Firestore.
func InitFirestore(ctx context.Context, gcpProjectID string) error {
	client, err := firestore.NewClient(ctx, gcpProjectID)
	if err != nil {
		return fmt.Errorf("error al crear el cliente de Firestore: %w", err)
	}
	FsClient = client
	return nil
}

// InitStorage inicializa el cliente global de Google Cloud Storage.
func InitStorage(ctx context.Context) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("error al crear el cliente de Storage: %w", err)
	}
	StorageClient = client
	return nil
}

// PubSubMessage representa el cuerpo de la petición que envía Pub/Sub.
type PubSubMessage struct {
	Message struct {
		Data       string            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
}
