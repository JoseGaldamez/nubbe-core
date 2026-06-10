package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
)

type Subscription struct {
	PlanID      string `firestore:"plan_id"`
	PlanIDCamel string `firestore:"planId"`
	Status      string `firestore:"status"`
}

type UserDocument struct {
	Subscription Subscription `firestore:"subscription"`
}

// SubscriptionLimitMiddleware intercepta la creación de proyectos y verifica los límites del plan del usuario en Firestore.
func SubscriptionLimitMiddleware(client *firestore.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in context"})
			c.Abort()
			return
		}

		ctx := c.Request.Context()

		// 1. Obtener la suscripción del usuario desde Firestore
		dsnap, err := client.Collection("users").Doc(userID).Get(ctx)
		var planID string = "free" // Default
		if err == nil && dsnap.Exists() {
			var userDoc UserDocument
			if err := dsnap.DataTo(&userDoc); err == nil {
				if userDoc.Subscription.PlanIDCamel != "" {
					planID = userDoc.Subscription.PlanIDCamel
				} else if userDoc.Subscription.PlanID != "" {
					planID = userDoc.Subscription.PlanID
				}
			}
		}

		// 2. Determinar el límite de proyectos para el plan
		maxProjects := 5 // Default Free limit
		planIDNormalized := strings.ToLower(planID)

		if strings.Contains(planIDNormalized, "pro") {
			maxProjects = -1 // Ilimitado
		} else if strings.Contains(planIDNormalized, "hobby") {
			maxProjects = 20 // Cambiado de 10 a 20 para alinear con la oferta comercial
		}

		// Si el plan es ilimitado, podemos continuar inmediatamente
		if maxProjects == -1 {
			c.Next()
			return
		}

		// 3. Contar los proyectos actuales del usuario
		iter := client.Collection("users").Doc(userID).Collection("projects").Documents(ctx)
		defer iter.Stop()

		projectCount := 0
		for {
			_, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "Failed to count existing projects",
					"details": err.Error(),
				})
				c.Abort()
				return
			}
			projectCount++
		}

		// 4. Verificar límite
		if projectCount >= maxProjects {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "Límite de proyectos alcanzado",
				"details": fmt.Sprintf("Tu plan actual (%s) permite un máximo de %d proyectos. Por favor, actualiza tu plan para crear más.", planID, maxProjects),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
