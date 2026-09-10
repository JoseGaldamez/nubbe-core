package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/PaddleHQ/paddle-go-sdk/v3"
	"github.com/gin-gonic/gin"
)

// GetUserPayments devuelve el historial de pagos del usuario autenticado incluyendo URLs de facturas/recibos.
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

// ResumeUserSubscription revierte la cancelación programada de una suscripción activa.
func (app *App) ResumeUserSubscription(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID missing in context"})
		return
	}

	userDocRef := app.Firestore.Collection("users").Doc(userID)
	dsnap, err := userDocRef.Get(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User profile not found"})
		return
	}

	var userData struct {
		Subscription struct {
			PlanID               string `firestore:"planId"`
			PaddleSubscriptionID string `firestore:"paddleSubscriptionId"`
			CancelAtPeriodEnd    bool   `firestore:"cancelAtPeriodEnd"`
		} `firestore:"subscription"`
	}

	if err := dsnap.DataTo(&userData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user subscription"})
		return
	}

	paddleSubID := userData.Subscription.PaddleSubscriptionID
	if paddleSubID == "" || !userData.Subscription.CancelAtPeriodEnd {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No subscription scheduled for cancellation found to resume"})
		return
	}

	// Si PADDLE_API_KEY está configurado, llamar a la API de Paddle para reanudar la suscripción (quitar scheduled_change)
	if app.PaddleAPIKey != "" {
		baseURL := "https://api.paddle.com"
		if app.PaddleEnvironment == "sandbox" {
			baseURL = "https://sandbox-api.paddle.com"
		}

		// En Paddle Billing v3, enviar scheduled_change: null desmarca la cancelación programada
		url := fmt.Sprintf("%s/subscriptions/%s", baseURL, paddleSubID)
		bodyPayload := map[string]interface{}{
			"scheduled_change": nil,
		}
		jsonBytes, _ := json.Marshal(bodyPayload)

		req, reqErr := http.NewRequestWithContext(c.Request.Context(), "PATCH", url, bytes.NewBuffer(jsonBytes))
		if reqErr == nil {
			req.Header.Set("Authorization", "Bearer "+app.PaddleAPIKey)
			req.Header.Set("Content-Type", "application/json")

			resp, httpErr := http.DefaultClient.Do(req)
			if httpErr != nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated) {
				respBody, _ := io.ReadAll(resp.Body)
				log.Printf("[Resume API] Error reanudando suscripción en Paddle (status %d): %s", resp.StatusCode, string(respBody))
				if resp != nil {
					resp.Body.Close()
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resume subscription in Paddle"})
				return
			}
			resp.Body.Close()
		}
	}

	// Actualizar Firestore desmarcando cancelAtPeriodEnd
	_, _ = userDocRef.Update(c.Request.Context(), []firestore.Update{
		{Path: "subscription.cancelAtPeriodEnd", Value: false},
		{Path: "subscription.status", Value: "active"},
		{Path: "updatedAt", Value: time.Now().Format(time.RFC3339)},
	})

	log.Printf("[Resume API] Suscripción %s reanudada con éxito para usuario %s", paddleSubID, userID)
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "Subscription resumed successfully",
	})
}

// GetCustomerPortalURL genera la URL de acceso seguro al Portal de Cliente de Paddle para gestionar pagos/tarjetas.
func (app *App) GetCustomerPortalURL(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID missing in context"})
		return
	}

	userDocRef := app.Firestore.Collection("users").Doc(userID)
	dsnap, err := userDocRef.Get(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User profile not found"})
		return
	}

	var userData struct {
		Subscription struct {
			PaddleCustomerID string `firestore:"paddleCustomerId"`
		} `firestore:"subscription"`
	}

	if err := dsnap.DataTo(&userData); err != nil || userData.Subscription.PaddleCustomerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No Paddle customer account found for this user"})
		return
	}

	customerID := userData.Subscription.PaddleCustomerID
	if app.PaddleAPIKey == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Paddle API key not configured"})
		return
	}

	baseURL := "https://api.paddle.com"
	if app.PaddleEnvironment == "sandbox" {
		baseURL = "https://sandbox-api.paddle.com"
	}

	url := fmt.Sprintf("%s/customers/%s/portal-sessions", baseURL, customerID)
	req, reqErr := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewBuffer([]byte("{}")))
	if reqErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create portal session request"})
		return
	}

	req.Header.Set("Authorization", "Bearer "+app.PaddleAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, httpErr := http.DefaultClient.Do(req)
	if httpErr != nil {
		log.Printf("[Portal API] Error conectando a Paddle API: %v", httpErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Paddle connection error"})
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("[Portal API] Paddle retornó error %d: %s", resp.StatusCode, string(bodyBytes))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate portal session from Paddle"})
		return
	}

	var portalResp struct {
		Data struct {
			URLs struct {
				General struct {
					URL string `json:"url"`
				} `json:"general"`
			} `json:"urls"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &portalResp); err != nil || portalResp.Data.URLs.General.URL == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid portal session response from Paddle"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"url": portalResp.Data.URLs.General.URL,
	})
}

// SyncUserSubscription consulta Paddle en tiempo real para verificar y actualizar el estado tras el checkout.
func (app *App) SyncUserSubscription(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID missing in context"})
		return
	}

	userDocRef := app.Firestore.Collection("users").Doc(userID)
	dsnap, err := userDocRef.Get(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User profile not found"})
		return
	}

	var userData struct {
		Subscription struct {
			PlanID               string `firestore:"planId"`
			PaddleSubscriptionID string `firestore:"paddleSubscriptionId"`
			PaddleCustomerID     string `firestore:"paddleCustomerId"`
			Status               string `firestore:"status"`
			CurrentPeriodEnd     string `firestore:"currentPeriodEnd"`
		} `firestore:"subscription"`
	}

	if err := dsnap.DataTo(&userData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse user profile"})
		return
	}

	paddleSubID := userData.Subscription.PaddleSubscriptionID
	txIDParam := c.Query("transaction_id")

	// Si no hay API KEY o ID de suscripción/transacción, retornar el estado actual de Firestore
	if app.PaddleAPIKey == "" || (paddleSubID == "" && txIDParam == "") {
		c.JSON(http.StatusOK, gin.H{
			"status":       "synced_from_db",
			"subscription": userData.Subscription,
		})
		return
	}

	baseURL := "https://api.paddle.com"
	if app.PaddleEnvironment == "sandbox" {
		baseURL = "https://sandbox-api.paddle.com"
	}

	targetSubID := paddleSubID

	// Si viene un transaction_id, consultar la transacción primero para extraer la suscripción
	if txIDParam != "" {
		txURL := fmt.Sprintf("%s/transactions/%s", baseURL, txIDParam)
		req, _ := http.NewRequestWithContext(c.Request.Context(), "GET", txURL, nil)
		req.Header.Set("Authorization", "Bearer "+app.PaddleAPIKey)

		if resp, err := http.DefaultClient.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var txData struct {
					Data struct {
						SubscriptionID *string `json:"subscription_id"`
						CustomerID     *string `json:"customer_id"`
						Status         string  `json:"status"`
					} `json:"data"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&txData); err == nil && txData.Data.SubscriptionID != nil {
					targetSubID = *txData.Data.SubscriptionID
				}
			}
		}
	}

	if targetSubID == "" {
		c.JSON(http.StatusOK, gin.H{
			"status":       "pending_webhook",
			"subscription": userData.Subscription,
		})
		return
	}

	// Consultar suscripción directa en Paddle API
	subURL := fmt.Sprintf("%s/subscriptions/%s", baseURL, targetSubID)
	req, reqErr := http.NewRequestWithContext(c.Request.Context(), "GET", subURL, nil)
	if reqErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create sync request"})
		return
	}

	req.Header.Set("Authorization", "Bearer "+app.PaddleAPIKey)
	resp, httpErr := http.DefaultClient.Do(req)
	if httpErr != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		c.JSON(http.StatusOK, gin.H{
			"status":       "synced_from_db",
			"subscription": userData.Subscription,
		})
		return
	}
	defer resp.Body.Close()

	var paddleSub struct {
		Data struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			CustomerID string `json:"customer_id"`
			Items      []struct {
				PriceID string `json:"price_id"`
				Price   struct {
					ID string `json:"id"`
				} `json:"price"`
			} `json:"items"`
			CurrentPeriod *struct {
				EndsAt string `json:"ends_at"`
			} `json:"current_period"`
			NextBilledAt string `json:"next_billed_at"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&paddleSub); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse Paddle subscription response"})
		return
	}

	priceID := ""
	if len(paddleSub.Data.Items) > 0 {
		if paddleSub.Data.Items[0].PriceID != "" {
			priceID = paddleSub.Data.Items[0].PriceID
		} else {
			priceID = paddleSub.Data.Items[0].Price.ID
		}
	}

	subLevel := app.resolveSubscriptionLevelFromPriceID(priceID)
	planID, limits := resolvePlanAndLimits(subLevel)

	if paddleSub.Data.Status == "canceled" {
		planID, limits = resolvePlanAndLimits("free")
		subLevel = "free"
	}

	periodEnd := paddleSub.Data.NextBilledAt
	if paddleSub.Data.CurrentPeriod != nil && paddleSub.Data.CurrentPeriod.EndsAt != "" {
		periodEnd = paddleSub.Data.CurrentPeriod.EndsAt
	}

	// Actualizar Firestore con la información síncrona
	updates := []firestore.Update{
		{Path: "subscription.plan_id", Value: planID},
		{Path: "subscription.planId", Value: planID},
		{Path: "subscription.level", Value: subLevel},
		{Path: "subscription.status", Value: paddleSub.Data.Status},
		{Path: "subscription.paddleCustomerId", Value: paddleSub.Data.CustomerID},
		{Path: "subscription.paddle_customer_id", Value: paddleSub.Data.CustomerID},
		{Path: "subscription.paddleSubscriptionId", Value: paddleSub.Data.ID},
		{Path: "subscription.paddle_subscription_id", Value: paddleSub.Data.ID},
		{Path: "subscription.currentPeriodEnd", Value: periodEnd},
		{Path: "limits.maxProjects", Value: limits.MaxProjects},
		{Path: "limits.maxBandwidthGB", Value: limits.MaxBandwidthGB},
		{Path: "limits.buildMinutesLimit", Value: limits.BuildMinutesLimit},
		{Path: "limits.features.customDomains", Value: limits.Features.CustomDomains},
		{Path: "limits.features.emailSupport", Value: limits.Features.EmailSupport},
		{Path: "limits.features.whatsappSupport", Value: limits.Features.WhatsappSupport},
		{Path: "updatedAt", Value: time.Now().Format(time.RFC3339)},
	}

	_, _ = userDocRef.Update(c.Request.Context(), updates)

	log.Printf("[Sync API] Suscripción de usuario %s sincronizada en tiempo real con Paddle (plan=%s, status=%s)", userID, planID, paddleSub.Data.Status)

	c.JSON(http.StatusOK, gin.H{
		"status": "synced",
		"subscription": gin.H{
			"planId":               planID,
			"level":                subLevel,
			"status":               paddleSub.Data.Status,
			"paddleCustomerId":     paddleSub.Data.CustomerID,
			"paddleSubscriptionId": paddleSub.Data.ID,
			"currentPeriodEnd":     periodEnd,
		},
		"limits": limits,
	})
}
