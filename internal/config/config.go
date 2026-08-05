package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	GCPProjectID              string
	WebhookAudience           string
	PubSubServiceAccountEmail string
	LogsBucket                string
	JobsAudience              string
	Port                      string
	GinMode                   string
	AESEncryptionKey          string
	PaddleWebhookSecret       string
	PaddleAPIKey              string
	PaddleProductID           string
	PaddlePriceHobbyMonthly   string
	PaddlePriceProMonthly     string
	PaddlePriceHobbyAnnually  string
	PaddlePriceProAnnually    string
	PaddleEnvironment         string
}

func Load() (*Config, error) {
	// Load .env if it exists
	_ = godotenv.Load()

	cfg := &Config{
		GCPProjectID:              os.Getenv("GCP_PROJECT_ID"),
		WebhookAudience:           os.Getenv("WEBHOOK_AUDIENCE"),
		PubSubServiceAccountEmail: os.Getenv("PUBSUB_SERVICE_ACCOUNT_EMAIL"),
		LogsBucket:                os.Getenv("LOGS_BUCKET"),
		JobsAudience:              os.Getenv("JOBS_AUDIENCE"),
		Port:                      os.Getenv("PORT"),
		GinMode:                   os.Getenv("GIN_MODE"),
		AESEncryptionKey:          os.Getenv("AES_ENCRYPTION_KEY"),
		PaddleWebhookSecret:       os.Getenv("PADDLE_WEBHOOK_SECRET"),
		PaddleAPIKey:              os.Getenv("PADDLE_API_KEY"),
		PaddleProductID:           os.Getenv("PADDLE_PRODUCT_ID"),
		PaddlePriceHobbyMonthly:   os.Getenv("PADDLE_PRICE_HOBBY_MONTHLY"),
		PaddlePriceProMonthly:     os.Getenv("PADDLE_PRICE_PRO_MONTHLY"),
		PaddlePriceHobbyAnnually:  os.Getenv("PADDLE_PRICE_HOBBY_ANNUALLY"),
		PaddlePriceProAnnually:   os.Getenv("PADDLE_PRICE_PRO_ANNUALLY"),
		PaddleEnvironment:         os.Getenv("PADDLE_ENVIRONMENT"),
	}


	if cfg.Port == "" {
		cfg.Port = "8080"
	}

	if cfg.LogsBucket == "" {
		cfg.LogsBucket = "nubbe-build-logs"
	}

	if cfg.AESEncryptionKey == "" {
		// Llave de ejemplo de 32 bytes para AES-256 (En prod usar una real via ENV)
		cfg.AESEncryptionKey = "12345678901234567890123456789012"
	}

	if cfg.PaddleEnvironment == "" {
		cfg.PaddleEnvironment = "sandbox"
	}

	// Validate required variables
	if cfg.GCPProjectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID is required")
	}
	if cfg.WebhookAudience == "" {
		return nil, fmt.Errorf("WEBHOOK_AUDIENCE is required")
	}
	if cfg.PubSubServiceAccountEmail == "" {
		return nil, fmt.Errorf("PUBSUB_SERVICE_ACCOUNT_EMAIL is required")
	}

	return cfg, nil
}
