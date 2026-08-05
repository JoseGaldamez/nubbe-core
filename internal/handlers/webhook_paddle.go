package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/JoseGaldamez/nubbe-core/internal/builders"
	"github.com/JoseGaldamez/nubbe-core/internal/pkg/crypto"
	"github.com/JoseGaldamez/nubbe-core/internal/repository"
	"github.com/PaddleHQ/paddle-go-sdk/v3"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
)

// TransactionCompletedData representa los datos payload del evento transaction.completed de Paddle Billing.
type TransactionCompletedData struct {
	ID             string                 `json:"id"`
	Status         string                 `json:"status"`
	CustomerID     *string                `json:"customer_id"`
	SubscriptionID *string                `json:"subscription_id"`
	CustomData     map[string]interface{} `json:"custom_data"`
	Items          []struct {
		PriceID string `json:"price_id"`
		Price   struct {
			ID string `json:"id"`
		} `json:"price"`
	} `json:"items"`
	BilledAt  *string `json:"billed_at"`
	CreatedAt string  `json:"created_at"`
}

// HandlePaddleWebhook procesa las notificaciones de webhook de Paddle Billing (Sandbox/Production).
func (app *App) HandlePaddleWebhook(c *gin.Context) {
	// 1. Lectura Raw del Body ( slice []byte directamente sin aplicar parser JSON ni alterarlo )
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("[Paddle Webhook] Error leyendo raw body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	// Reasignar c.Request.Body para permitir lecturas posteriores si es necesario
	c.Request.Body = io.NopCloser(bytes.NewBuffer(rawBody))

	// 2. Verificación de firma criptográfica usando Paddle-Signature y PADDLE_WEBHOOK_SECRET
	signatureHeader := c.GetHeader("Paddle-Signature")

	if app.PaddleWebhookSecret != "" {
		if signatureHeader == "" {
			log.Println("[Paddle Webhook] Cabecera Paddle-Signature no proporcionada")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing Paddle-Signature header"})
			return
		}

		verifier := paddle.NewWebhookVerifier(app.PaddleWebhookSecret)
		valid, err := verifier.Verify(c.Request)

		if err != nil || !valid {
			// Fallback a verificación local HMAC
			fallbackValid, fallbackErr := crypto.VerifyPaddleSignature(signatureHeader, rawBody, app.PaddleWebhookSecret)
			if fallbackErr != nil || !fallbackValid {
				log.Printf("[Paddle Webhook] Firma inválida en Paddle-Signature: verifierErr=%v, fallbackErr=%v", err, fallbackErr)
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook signature"})
				return
			}
		}
	} else {
		log.Println("[Paddle Webhook] ADVERTENCIA: PADDLE_WEBHOOK_SECRET no configurado, omitiendo verificación")
	}




	// 3. Parsear el evento base
	var baseEvent struct {
		EventID    string               `json:"event_id"`
		EventType  paddle.EventTypeName `json:"event_type"`
		OccurredAt string               `json:"occurred_at"`
		Data       json.RawMessage      `json:"data"`
	}

	if err := json.Unmarshal(rawBody, &baseEvent); err != nil {
		log.Printf("[Paddle Webhook] Error parseando JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	log.Printf("[Paddle Webhook] Evento recibido: %s (ID: %s)", baseEvent.EventType, baseEvent.EventID)

	// 4. Manejo de Eventos según su tipo
	switch baseEvent.EventType {
	case paddle.EventTypeNameTransactionCompleted:
		app.handleTransactionCompleted(c, baseEvent.EventID, baseEvent.Data)
	case paddle.EventTypeNameSubscriptionCreated, paddle.EventTypeNameSubscriptionUpdated, paddle.EventTypeNameSubscriptionCanceled:
		app.handleSubscriptionEventLegacy(c, baseEvent.EventID, string(baseEvent.EventType), baseEvent.Data)
	default:
		log.Printf("[Paddle Webhook] Evento '%s' no requiere acción", baseEvent.EventType)
		c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Event ignored"})
	}

}

// handleTransactionCompleted maneja el evento transaction.completed e incrementa los límites del usuario.
func (app *App) handleTransactionCompleted(c *gin.Context, eventID string, dataRaw json.RawMessage) {
	ctx := c.Request.Context()

	var data TransactionCompletedData
	if err := json.Unmarshal(dataRaw, &data); err != nil {
		log.Printf("[Paddle Webhook] Error parseando data de transacción: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid transaction payload"})
		return
	}

	// Extraer userId desde custom_data
	userID := extractUserID(data.CustomData)
	if userID == "" {
		log.Printf("[Paddle Webhook] Error: userId no especificado en custom_data para la transacción %s", data.ID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id missing in custom_data"})
		return
	}

	// Resolver nivel de suscripción e ID de precio
	subLevel := extractSubscriptionLevel(data.CustomData)
	priceID := ""
	if len(data.Items) > 0 {
		if data.Items[0].PriceID != "" {
			priceID = data.Items[0].PriceID
		} else {
			priceID = data.Items[0].Price.ID
		}
	}

	if subLevel == "" {
		subLevel = app.resolveSubscriptionLevelFromPriceID(priceID)
	}

	// Determinar el Plan ID y Límites de la suscripción
	planID, limits := resolvePlanAndLimits(subLevel)

	customerID := ""
	if data.CustomerID != nil {
		customerID = *data.CustomerID
	}
	subscriptionID := ""
	if data.SubscriptionID != nil {
		subscriptionID = *data.SubscriptionID
	}

	log.Printf("[Paddle Webhook] Transacción recibida: user=%s, level=%s, plan=%s, priceID=%s, txId=%s",
		userID, subLevel, planID, priceID, data.ID)

	// Idempotencia: Verificar si el evento o la transacción ya fue procesada anteriormente
	if app.PaymentRepo != nil {
		isDup, err := app.PaymentRepo.IsEventProcessed(ctx, eventID, data.ID)
		if err != nil {
			log.Printf("[Paddle Webhook] Error verificando idempotencia: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error checking idempotency"})
			return
		}
		if isDup {
			log.Printf("[Paddle Webhook] Transacción %s / Evento %s ya fue procesada previamente (Idempotente)", data.ID, eventID)
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Already processed"})
			return
		}
	}

	// Preparar modelos para repositorio
	txRecord := repository.TransactionRecord{
		EventID:           eventID,
		TransactionID:     data.ID,
		UserID:            userID,
		SubscriptionLevel: subLevel,
		PlanID:            planID,
		Status:            data.Status,
		PaddleCustomerID:  customerID,
		PaddleSubID:       subscriptionID,
		ProcessedAt:       time.Now(),
	}

	subData := repository.UserSubscription{
		PlanID:               planID,
		PlanIDSnake:          planID,
		Level:                subLevel,
		Status:               "active",
		PaddleCustomerID:     customerID,
		PaddleSubscriptionID: subscriptionID,
		PaddleTransactionID:  data.ID,
		CurrentPeriodEnd:     time.Now().AddDate(0, 1, 0).Format(time.RFC3339),
		CancelAtPeriodEnd:    false,
	}

	// Actualizar en base de datos de manera idempotente
	if app.PaymentRepo != nil {
		if err := app.PaymentRepo.ProcessTransactionCompleted(ctx, eventID, txRecord, subData, limits); err != nil {
			log.Printf("[Paddle Webhook] ERROR en base de datos actualizando user %s: %v", userID, err)
			// Retorna 500 solo si falla la base de datos (para forzar a Paddle a reintentar)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update database"})
			return
		}
	}

	log.Printf("[Paddle Webhook] Éxito actualizando usuario %s a plan %s (%s)", userID, planID, subLevel)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "transaction_id": data.ID})
}

// handleSubscriptionEventLegacy maneja los eventos de ciclo de vida de la suscripción.
func (app *App) handleSubscriptionEventLegacy(c *gin.Context, eventID, eventType string, dataRaw json.RawMessage) {
	ctx := c.Request.Context()

	var data struct {
		ID         string                 `json:"id"`
		Status     string                 `json:"status"`
		CustomerID string                 `json:"customer_id"`
		CustomData map[string]interface{} `json:"custom_data"`
		Items      []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"items"`
		NextBilledAt string `json:"next_billed_at"`
	}

	if err := json.Unmarshal(dataRaw, &data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subscription payload"})
		return
	}

	userID := extractUserID(data.CustomData)
	if userID == "" {
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "No user_id in custom_data"})
		return
	}

	priceID := ""
	if len(data.Items) > 0 {
		priceID = data.Items[0].Price.ID
	}
	subLevel := extractSubscriptionLevel(data.CustomData)
	if subLevel == "" {
		subLevel = app.resolveSubscriptionLevelFromPriceID(priceID)
	}
	planID, limits := resolvePlanAndLimits(subLevel)

	if eventType == "subscription.canceled" || data.Status == "canceled" {
		planID, limits = resolvePlanAndLimits("free")
		subLevel = "free"
	}

	updates := []firestore.Update{
		{Path: "subscription.plan_id", Value: planID},
		{Path: "subscription.planId", Value: planID},
		{Path: "subscription.level", Value: subLevel},
		{Path: "subscription.status", Value: data.Status},
		{Path: "subscription.paddleCustomerId", Value: data.CustomerID},
		{Path: "subscription.paddle_customer_id", Value: data.CustomerID},
		{Path: "subscription.paddleSubscriptionId", Value: data.ID},
		{Path: "subscription.paddle_subscription_id", Value: data.ID},
		{Path: "subscription.currentPeriodEnd", Value: data.NextBilledAt},
		{Path: "limits.maxProjects", Value: limits.MaxProjects},
		{Path: "limits.maxBandwidthGB", Value: limits.MaxBandwidthGB},
		{Path: "limits.buildMinutesLimit", Value: limits.BuildMinutesLimit},
		{Path: "limits.features.customDomains", Value: limits.Features.CustomDomains},
		{Path: "limits.features.emailSupport", Value: limits.Features.EmailSupport},
		{Path: "limits.features.whatsappSupport", Value: limits.Features.WhatsappSupport},
		{Path: "updatedAt", Value: time.Now().Format(time.RFC3339)},
	}

	userRef := app.Firestore.Collection("users").Doc(userID)
	if _, err := userRef.Update(ctx, updates); err != nil {
		log.Printf("[Paddle Webhook] Error actualizando suscripción legacy para user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database update failed"})
		return
	}

	if eventType == "subscription.canceled" || data.Status == "canceled" {
		go app.handleDowngrade(userID)
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// extractUserID extrae el ID de usuario desde los metadatos de custom_data.
func extractUserID(customData map[string]interface{}) string {
	if customData == nil {
		return ""
	}
	for _, key := range []string{"user_id", "userId", "user_ID"} {
		if val, ok := customData[key].(string); ok && val != "" {
			return val
		}
	}
	return ""
}

// extractSubscriptionLevel extrae el nivel de suscripción desde custom_data.
func extractSubscriptionLevel(customData map[string]interface{}) string {
	if customData == nil {
		return ""
	}
	for _, key := range []string{"subscription_level", "subscriptionLevel", "level", "plan"} {
		if val, ok := customData[key].(string); ok && val != "" {
			return val
		}
	}
	return ""
}

// resolveSubscriptionLevelFromPriceID mapea un price_id a los niveles PADDLE_SUSCRIPTION_LEVEL.
func (app *App) resolveSubscriptionLevelFromPriceID(priceID string) string {
	if priceID == "" {
		return "free"
	}

	switch priceID {
	case app.PaddlePriceHobbyMonthly, "pri_01kz9hf40ams7nwtmc6rbzddpe":
		return "Hobby_Monthly"
	case app.PaddlePriceProMonthly, "pri_01kz9hha2svvb7m6qqddrqq7b9":
		return "Pro_Monthly"
	case app.PaddlePriceHobbyAnnually, "pri_01kz9hjq5p37a62axg46merg52":
		return "Hobby_Annually"
	case app.PaddlePriceProAnnually, "pri_01kz9hmbrjbrt37f89nv1pvspr":
		return "Pro_Annually"
	}

	lowerID := strings.ToLower(priceID)
	if strings.Contains(lowerID, "pro") {
		if strings.Contains(lowerID, "annu") || strings.Contains(lowerID, "year") {
			return "Pro_Annually"
		}
		return "Pro_Monthly"
	}
	if strings.Contains(lowerID, "hobby") {
		if strings.Contains(lowerID, "annu") || strings.Contains(lowerID, "year") {
			return "Hobby_Annually"
		}
		return "Hobby_Monthly"
	}

	return "free"
}

// resolvePlanAndLimits retorna el nombre del plan y los límites correspondientes.
func resolvePlanAndLimits(subLevel string) (string, repository.UserLimits) {
	switch subLevel {
	case "Hobby_Monthly", "Hobby_Annually", "hobby":
		return "hobby", repository.UserLimits{
			MaxProjects:       20,
			MaxBandwidthGB:    25,
			BuildMinutesLimit: 150,
			Features: repository.Features{
				CustomDomains:   true,
				EmailSupport:    true,
				WhatsappSupport: false,
			},
		}
	case "Pro_Monthly", "Pro_Annually", "pro":
		return "pro", repository.UserLimits{
			MaxProjects:       -1,
			MaxBandwidthGB:    100,
			BuildMinutesLimit: 500,
			Features: repository.Features{
				CustomDomains:   true,
				EmailSupport:    true,
				WhatsappSupport: true,
			},
		}
	default:
		return "free", repository.UserLimits{
			MaxProjects:       5,
			MaxBandwidthGB:    2,
			BuildMinutesLimit: 30,
			Features: repository.Features{
				CustomDomains:   false,
				EmailSupport:    false,
				WhatsappSupport: false,
			},
		}
	}
}

// projectForDowngrade es un struct para ordenar proyectos durante la degradación.
type projectForDowngrade struct {
	ID        string
	Subdomain string
	CreatedAt time.Time
	State     string
}

// handleDowngrade suspende proyectos excedentes cuando un usuario vuelve a Free.
func (app *App) handleDowngrade(userID string) {
	ctx := context.Background()
	maxFreeProjects := 5

	log.Printf("[Downgrade] Rutina de degradación iniciada para user %s", userID)

	iter := app.Firestore.Collection("users").Doc(userID).Collection("projects").Documents(ctx)
	defer iter.Stop()

	var projects []projectForDowngrade
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("[Downgrade] Error iterando proyectos de user %s: %v", userID, err)
			return
		}

		p := projectForDowngrade{
			ID: doc.Ref.ID,
		}

		if subdomain, ok := doc.Data()["subdomain"].(string); ok {
			p.Subdomain = subdomain
		}

		if statusMap, ok := doc.Data()["status"].(map[string]interface{}); ok {
			if state, ok := statusMap["state"].(string); ok {
				p.State = state
			}
		}

		if createdAt, ok := doc.Data()["createdAt"].(time.Time); ok {
			p.CreatedAt = createdAt
		}

		projects = append(projects, p)
	}

	if len(projects) <= maxFreeProjects {
		log.Printf("[Downgrade] User %s dentro del límite Free (%d proyectos).", userID, len(projects))
		return
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].CreatedAt.Before(projects[j].CreatedAt)
	})

	toSuspend := projects[maxFreeProjects:]
	cfAccountID, cfToken, cfKVNamespace, cfErr := builders.GetCloudflareCredentials()

	for _, proj := range toSuspend {
		_, err := app.Firestore.Collection("users").Doc(userID).
			Collection("projects").Doc(proj.ID).
			Update(ctx, []firestore.Update{
				{Path: "status.state", Value: "SUSPENDED"},
				{Path: "updatedAt", Value: time.Now().Format(time.RFC3339)},
			})
		if err != nil {
			log.Printf("[Downgrade] Error suspendiendo proyecto %s: %v", proj.ID, err)
			continue
		}

		if cfErr == nil && proj.Subdomain != "" {
			if err := builders.DeleteRouteInKV(ctx, cfAccountID, cfToken, cfKVNamespace, proj.Subdomain); err != nil {
				log.Printf("[Downgrade] Error eliminando ruta KV para %s: %v", proj.Subdomain, err)
			}
		}
	}

	log.Printf("[Downgrade] Rutina completada para user %s", userID)
}
