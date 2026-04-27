package handlers

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
)

// InitFirestore inicializa el cliente de Firestore y lo devuelve.
func InitFirestore(ctx context.Context, gcpProjectID string) (*firestore.Client, error) {
	client, err := firestore.NewClient(ctx, gcpProjectID)
	if err != nil {
		return nil, fmt.Errorf("error al crear el cliente de Firestore: %w", err)
	}
	return client, nil
}

// InitStorage inicializa el cliente de Google Cloud Storage y lo devuelve.
func InitStorage(ctx context.Context) (*storage.Client, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("error al crear el cliente de Storage: %w", err)
	}
	return client, nil
}

// PubSubMessage representa el cuerpo de la petición que envía Pub/Sub.
type PubSubMessage struct {
	Message struct {
		Data       string            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
}
