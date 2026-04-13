package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Stubs temporales para las funciones de los handlers
func HandleGitHubWebhook(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Webhook received"})
}
