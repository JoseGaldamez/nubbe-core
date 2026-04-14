package middleware

import (
	"log"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/idtoken"
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

// GoogleOIDCMiddleware verifica que la petición venga de Google Pub/Sub usando un token OIDC.
func GoogleOIDCMiddleware(expectedAudience string, expectedEmail string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization header format"})
			c.Abort()
			return
		}

		token := parts[1]
		ctx := c.Request.Context()

		// Validar el token OIDC
		payload, err := idtoken.Validate(ctx, token, expectedAudience)
		if err != nil {
			log.Printf("Error validando OIDC token: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid OIDC token"})
			c.Abort()
			return
		}

		// Verificar que el emisor sea Google
		if payload.Issuer != "https://accounts.google.com" && payload.Issuer != "accounts.google.com" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token issuer"})
			c.Abort()
			return
		}

		// Si se especifica un email, verificarlo
		if expectedEmail != "" {
			email, ok := payload.Claims["email"].(string)
			if !ok || email != expectedEmail {
				log.Printf("Error: Email del token (%s) no coincide con el esperado (%s)", email, expectedEmail)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized service account"})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}
