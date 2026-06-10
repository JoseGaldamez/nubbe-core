package middleware

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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

		// Extraer la audiencia real del token decodificando el payload JWT (sin verificar firma aún)
		partsJWT := strings.Split(token, ".")
		if len(partsJWT) == 3 {
			payloadBytes, errDec := base64.RawURLEncoding.DecodeString(partsJWT[1])
			if errDec == nil {
				var claims map[string]interface{}
				if errJSON := json.Unmarshal(payloadBytes, &claims); errJSON == nil {
					if aud, ok := claims["aud"].(string); ok {
						// Si la audiencia del token coincide con el patrón esperado o la ruta de la petición,
						// la usamos como la audiencia esperada para pasar a idtoken.Validate
						if aud == expectedAudience || strings.Contains(aud, c.Request.URL.Path) {
							log.Printf("[OIDC Middleware] Usando audiencia extraída del token: %s", aud)
							expectedAudience = aud
						}
					}
				}
			}
		}

		// Validar el token OIDC
		payload, err := idtoken.Validate(ctx, token, expectedAudience)
		if err != nil {
			// Intentar con la URL del endpoint real como audiencia fallback (ej: HTTPS en Cloud Run)
			proto := c.GetHeader("X-Forwarded-Proto")
			if proto == "" {
				proto = "https"
			}
			fallbackAudience := fmt.Sprintf("%s://%s%s", proto, c.Request.Host, c.Request.URL.Path)
			log.Printf("[OIDC Middleware] expectedAudience (%s) failed: %v. Retrying with fallbackAudience (%s)", expectedAudience, err, fallbackAudience)
			
			var errFallback error
			payload, errFallback = idtoken.Validate(ctx, token, fallbackAudience)
			if errFallback != nil {
				log.Printf("[OIDC Middleware] OIDC token validation failed: %v", errFallback)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid OIDC token", "details": errFallback.Error()})
				c.Abort()
				return
			}
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
