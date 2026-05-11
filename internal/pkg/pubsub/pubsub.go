package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub/v2"
)

// Topic names
const (
	BuildTopic = "nubbe-build-topic"
	JobsTopic  = "nubbe-jobs"
)

type Client struct {
	client    *pubsub.Client
	projectID string
}

func NewClient(ctx context.Context, projectID string) (*Client, error) {
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub client: %w", err)
	}
	return &Client{
		client:    client,
		projectID: projectID,
	}, nil
}

func (c *Client) Publish(ctx context.Context, topicName string, data interface{}) (string, error) {
	publisher := c.client.Publisher(topicName)
	
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal event for topic %s: %w", topicName, err)
	}

	result := publisher.Publish(ctx, &pubsub.Message{
		Data: jsonData,
	})

	id, err := result.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to publish message to %s: %w", topicName, err)
	}

	return id, nil
}

func (c *Client) PublishBuildEvent(ctx context.Context, data interface{}) (string, error) {
	return c.Publish(ctx, BuildTopic, data)
}

func (c *Client) Close() error {
	return c.client.Close()
}
