package middleware // <-- Esto es vital

import (
	"log"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
)

func FirebaseAuthMiddleware(authClient *auth.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Extraer el token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		// Esperamos formato: "Bearer <token>"
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header format"})
			c.Abort()
			return
		}

		idToken := parts[1]

		// 2. Verificar el token con Firebase
		ctx := c.Request.Context()
		token, err := authClient.VerifyIDToken(ctx, idToken)
		if err != nil {
			log.Printf("Error verificando token: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Firebase token"})
			c.Abort()
			return
		}

		// 3. Pasar el UID al contexto para que los handlers lo usen
		c.Set("user_id", token.UID)

		// Continuar con la petición
		c.Next()
	}
}
