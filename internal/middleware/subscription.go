package middleware

import (
	"context"
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

// SubscriptionRetriever define las operaciones de obtención de suscripciones y recuento de proyectos para un usuario.
type SubscriptionRetriever interface {
	GetSubscription(ctx context.Context, userID string) (Subscription, error)
	GetProjectCount(ctx context.Context, userID string) (int, error)
}

type firestoreRetriever struct {
	client *firestore.Client
}

func (r *firestoreRetriever) GetSubscription(ctx context.Context, userID string) (Subscription, error) {
	dsnap, err := r.client.Collection("users").Doc(userID).Get(ctx)
	if err != nil {
		return Subscription{}, err
	}
	var userDoc UserDocument
	if err := dsnap.DataTo(&userDoc); err != nil {
		return Subscription{}, err
	}
	return userDoc.Subscription, nil
}

func (r *firestoreRetriever) GetProjectCount(ctx context.Context, userID string) (int, error) {
	iter := r.client.Collection("users").Doc(userID).Collection("projects").Documents(ctx)
	defer iter.Stop()

	projectCount := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, err
		}
		projectCount++
	}
	return projectCount, nil
}

// SubscriptionLimitMiddleware intercepta la creación de proyectos y verifica los límites del plan del usuario en Firestore.
func SubscriptionLimitMiddleware(client *firestore.Client) gin.HandlerFunc {
	return SubscriptionLimitMiddlewareExt(&firestoreRetriever{client: client})
}

// SubscriptionLimitMiddlewareExt intercepta la creación de proyectos y verifica los límites del plan del usuario usando cualquier retriever.
func SubscriptionLimitMiddlewareExt(retriever SubscriptionRetriever) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in context"})
			c.Abort()
			return
		}

		ctx := c.Request.Context()

		// 1. Obtener la suscripción del usuario desde Firestore
		subscription, err := retriever.GetSubscription(ctx, userID)
		if err == nil && strings.EqualFold(subscription.Status, "past_due") {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "Pago en mora (Past Due)",
				"details": "Tu último cobro recurrente ha fallado. Por favor, actualiza tu método de pago para continuar utilizando el servicio.",
			})
			c.Abort()
			return
		}

		var planID string = "free" // Default
		if err == nil {
			if subscription.PlanIDCamel != "" {
				planID = subscription.PlanIDCamel
			} else if subscription.PlanID != "" {
				planID = subscription.PlanID
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
		projectCount, err := retriever.GetProjectCount(ctx, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "Failed to count existing projects",
				"details": err.Error(),
			})
			c.Abort()
			return
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
