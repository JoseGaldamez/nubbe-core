package handlers

import (
	"log"
	"net/http"

	"github.com/PaddleHQ/paddle-go-sdk/v3"
	"github.com/gin-gonic/gin"
)

// GetUserPayments devuelve el historial de pagos del usuario autenticado.
func (app *App) GetUserPayments(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID missing in context"})
		return
	}

	if app.PaymentRepo == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Payment repository not initialized"})
		return
	}

	payments, err := app.PaymentRepo.GetUserPayments(c.Request.Context(), userID)
	if err != nil {
		log.Printf("[Payments API] Error obteniendo historial para user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch payment history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"payments": payments})
}

// CancelUserSubscription programa la cancelación de la suscripción del usuario al final del período.
func (app *App) CancelUserSubscription(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID missing in context"})
		return
	}

	// 1. Obtener documento del usuario de Firestore para extraer la suscripción activa
	userDocRef := app.Firestore.Collection("users").Doc(userID)
	dsnap, err := userDocRef.Get(c.Request.Context())
	if err != nil {
		log.Printf("[Cancel API] Error obteniendo usuario %s: %v", userID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "User profile not found"})
		return
	}

	var userData struct {
		Subscription struct {
			PlanID               string `firestore:"planId"`
			PaddleSubscriptionID string `firestore:"paddleSubscriptionId"`
			Status               string `firestore:"status"`
			CancelAtPeriodEnd    bool   `firestore:"cancelAtPeriodEnd"`
		} `firestore:"subscription"`
	}

	if err := dsnap.DataTo(&userData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user subscription"})
		return
	}

	paddleSubID := userData.Subscription.PaddleSubscriptionID
	if paddleSubID == "" || userData.Subscription.PlanID == "free" || userData.Subscription.PlanID == "gratis" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active subscription found to cancel"})
		return
	}

	if userData.Subscription.CancelAtPeriodEnd {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "Subscription is already scheduled for cancellation at the end of the billing period",
		})
		return
	}

	// 2. Si PADDLE_API_KEY está configurado, llamar a la API oficial de Paddle en Go para cancelar
	if app.PaddleAPIKey != "" {
		var client *paddle.SDK
		var clientErr error

		if app.PaddleEnvironment == "sandbox" {
			client, clientErr = paddle.NewSandbox(app.PaddleAPIKey)
		} else {
			client, clientErr = paddle.New(app.PaddleAPIKey)
		}

		if clientErr != nil {
			log.Printf("[Cancel API] Error inicializando cliente de Paddle: %v", clientErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initialize Paddle client"})
			return
		}

		effectiveFrom := paddle.EffectiveFromNextBillingPeriod
		req := &paddle.CancelSubscriptionRequest{
			SubscriptionID: paddleSubID,
			EffectiveFrom:  &effectiveFrom,
		}

		_, paddleErr := client.CancelSubscription(c.Request.Context(), req)
		if paddleErr != nil {
			log.Printf("[Cancel API] Error cancelando suscripción %s en Paddle: %v", paddleSubID, paddleErr)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "Failed to cancel subscription in Paddle",
				"details": paddleErr.Error(),
			})
			return
		}
	} else {
		log.Printf("[Cancel API] ADVERTENCIA: PADDLE_API_KEY no configurado, marcando cancelación solo localmente para user %s", userID)
	}

	// 3. Marcar cancelAtPeriodEnd en Firestore inmediatamente
	if app.PaymentRepo != nil {
		_ = app.PaymentRepo.MarkSubscriptionCancelAtPeriodEnd(c.Request.Context(), userID)
	}

	log.Printf("[Cancel API] Cancelación programada exitosamente para la suscripción %s del usuario %s", paddleSubID, userID)

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "Subscription cancellation scheduled successfully at the end of current billing period",
	})
}
