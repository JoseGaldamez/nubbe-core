package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// TODO: Create a new type for SaveProjectEnvVarsRequest
type SaveProjectEnvVarsRequest struct {
	EnvVars map[string]string `json:"env_vars" binding:"required"`
}

// GetProjectEnvVars recupera y desencripta las variables de entorno de un proyecto
func (app *App) GetProjectEnvVars(c *gin.Context) {
	userID := c.GetString("user_id")
	projectID := c.Param("projectId")

	// 1. Obtener el proyecto de Firestore
	envVars, err := app.ProjectService.GetProjectVars(c.Request.Context(), userID, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// 5. Enviar de forma segura por HTTPS
	c.JSON(http.StatusOK, gin.H{"env_vars": envVars, "status": "success"})
}

func (app *App) SaveProjectEnvVars(c *gin.Context) {
	userID := c.GetString("user_id")
	projectID := c.Param("projectId")

	var req SaveProjectEnvVarsRequest

	// Bind and validate incoming JSON
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request payload",
			"details": err.Error(),
		})
		return
	}

	// Save project env vars
	err := app.ProjectService.SaveProjectVars(c.Request.Context(), userID, projectID, req.EnvVars)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
