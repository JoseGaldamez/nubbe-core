package handlers

import (
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"google.golang.org/api/option"
	pubsub "google.golang.org/api/pubsub/v1"
)

// DebugPubSub lists all topics and subscriptions to diagnose push notification delivery issues.
func (app *App) DebugPubSub(c *gin.Context) {
	ctx := c.Request.Context()
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = "nubbe-run"
	}

	// Initialize the Google API client for Pub/Sub (ADC handles this implicitly in Cloud Run, local fallback if credentials exist)
	var service *pubsub.Service
	var err error
	if _, statErr := os.Stat("./firebase-credentials.json"); statErr == nil {
		service, err = pubsub.NewService(ctx, option.WithCredentialsFile("./firebase-credentials.json"))
	} else {
		service, err = pubsub.NewService(ctx)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create pubsub service client", "details": err.Error()})
		return
	}

	// 1. List Topics
	topicsList, err := service.Projects.Topics.List(fmt.Sprintf("projects/%s", projectID)).Do()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list topics", "details": err.Error()})
		return
	}

	var topics []string
	for _, t := range topicsList.Topics {
		topics = append(topics, t.Name)
	}

	// 2. List Subscriptions
	subsList, err := service.Projects.Subscriptions.List(fmt.Sprintf("projects/%s", projectID)).Do()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list subscriptions", "details": err.Error()})
		return
	}

	type SubDetails struct {
		Name         string `json:"name"`
		Topic        string `json:"topic"`
		PushEndpoint string `json:"push_endpoint"`
		Audience     string `json:"audience"`
		ServiceEmail string `json:"service_email"`
	}

	var subscriptions []SubDetails
	for _, s := range subsList.Subscriptions {
		details := SubDetails{
			Name:  s.Name,
			Topic: s.Topic,
		}
		if s.PushConfig != nil {
			details.PushEndpoint = s.PushConfig.PushEndpoint
			if s.PushConfig.OidcToken != nil {
				details.Audience = s.PushConfig.OidcToken.Audience
				details.ServiceEmail = s.PushConfig.OidcToken.ServiceAccountEmail
			}
		}
		subscriptions = append(subscriptions, details)
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id":    projectID,
		"topics":        topics,
		"subscriptions": subscriptions,
	})
}
