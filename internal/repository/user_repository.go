package repository

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
)

type UserRepository interface {
	GetGithubToken(ctx context.Context, userID string) (string, error)
}

type firestoreUserRepo struct {
	client *firestore.Client
}

func NewUserRepository(client *firestore.Client) UserRepository {
	return &firestoreUserRepo{client: client}
}

func (r *firestoreUserRepo) GetGithubToken(ctx context.Context, userID string) (string, error) {
	dsnap, err := r.client.Collection("users").Doc(userID).Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get user document: %w", err)
	}

	var userData struct {
		GithubAccessToken string `firestore:"githubAccessToken"`
	}
	if err := dsnap.DataTo(&userData); err != nil {
		return "", fmt.Errorf("failed to parse user data: %w", err)
	}

	return userData.GithubAccessToken, nil
}
