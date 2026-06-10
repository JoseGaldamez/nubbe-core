package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/JoseGaldamez/nubbe-core/internal/builders"
	"github.com/JoseGaldamez/nubbe-core/internal/pkg/crypto"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
)

// --- Estructuras JSON para Paddle Billing V2 Webhooks ---

// PaddleCustomData contiene los metadatos personalizados que enviamos durante el checkout.
type PaddleCustomData struct {
	UserID string `json:"user_id"`
}

// PaddleSubscriptionItem representa un ítem dentro de la suscripción de Paddle.
type PaddleSubscriptionItem struct {
	Price struct {
		ID string `json:"id"`
	} `json:"price"`
}

// PaddleSubscriptionData contiene los campos relevantes del objeto de suscripción de Paddle.
type PaddleSubscriptionData struct {
	ID           string                   `json:"id"`
	Status       string                   `json:"status"`
	CustomerID   string                   `json:"customer_id"`
	CustomData   PaddleCustomData         `json:"custom_data"`
	Items        []PaddleSubscriptionItem `json:"items"`
	NextBilledAt string                   `json:"next_billed_at"`
}

// PaddleWebhookEvent es la estructura raíz del evento de webhook de Paddle.
type PaddleWebhookEvent struct {
	EventID   string                 `json:"event_id"`
	EventType string                 `json:"event_type"`
	Data      PaddleSubscriptionData `json:"data"`
}

// HandlePaddleWebhook procesa las notificaciones de webhook de Paddle Billing V2.
func (app *App) HandlePaddleWebhook(c *gin.Context) {
	// 1. Leer body en crudo (indispensable para la verificación de firma)
	rawBody, err := c.GetRawData()
	if err != nil {
		log.Printf("[Paddle Webhook] Error leyendo body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	// 2. Verificar firma criptográfica del webhook
	signatureHeader := c.GetHeader("Paddle-Signature")
	if app.PaddleWebhookSecret != "" {
		valid, err := crypto.VerifyPaddleSignature(signatureHeader, rawBody, app.PaddleWebhookSecret)
		if err != nil {
			log.Printf("[Paddle Webhook] Error verificando firma: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Signature verification failed"})
			return
		}
		if !valid {
			log.Println("[Paddle Webhook] Firma inválida, rechazando petición")
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid signature"})
			return
		}
	} else {
		log.Println("[Paddle Webhook] ADVERTENCIA: PADDLE_WEBHOOK_SECRET no configurado, omitiendo verificación de firma")
	}

	// 3. Parsear el evento
	var event PaddleWebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		log.Printf("[Paddle Webhook] Error parseando JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	log.Printf("[Paddle Webhook] Evento recibido: %s (ID: %s)", event.EventType, event.EventID)

	// 4. Solo procesar eventos de suscripción relevantes
	switch event.EventType {
	case "subscription.created", "subscription.updated", "subscription.canceled":
		app.handleSubscriptionEvent(c.Request.Context(), event)
	default:
		log.Printf("[Paddle Webhook] Evento '%s' ignorado (no es de suscripción)", event.EventType)
	}

	// Paddle espera un 200 para confirmar la recepción
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleSubscriptionEvent procesa eventos de suscripción y actualiza Firestore.
func (app *App) handleSubscriptionEvent(ctx context.Context, event PaddleWebhookEvent) {
	data := event.Data

	// Extraer el user_id desde custom_data
	userID := data.CustomData.UserID
	if userID == "" {
		log.Printf("[Paddle Webhook] ADVERTENCIA: custom_data.user_id vacío en evento %s, imposible mapear usuario", event.EventID)
		return
	}

	// Resolver el plan basado en el price_id del primer ítem
	planID := app.resolvePlanFromPriceID(data)

	// Determinar el status normalizado
	status := data.Status // "active", "past_due", "canceled", "trialing", "paused"

	log.Printf("[Paddle Webhook] Procesando: user=%s, plan=%s, status=%s, subscription=%s",
		userID, planID, status, data.ID)

	// Actualizar Firestore con doble nomenclatura para compatibilidad frontend/backend
	updates := []firestore.Update{
		// snake_case (leído por el middleware de Go)
		{Path: "subscription.plan_id", Value: planID},
		// camelCase (leído por el frontend React)
		{Path: "subscription.planId", Value: planID},

		{Path: "subscription.status", Value: status},
		{Path: "subscription.paddleCustomerId", Value: data.CustomerID},
		{Path: "subscription.paddle_customer_id", Value: data.CustomerID},
		{Path: "subscription.paddleSubscriptionId", Value: data.ID},
		{Path: "subscription.paddle_subscription_id", Value: data.ID},
		{Path: "subscription.currentPeriodEnd", Value: data.NextBilledAt},
		{Path: "subscription.next_billed_at", Value: data.NextBilledAt},
		{Path: "updatedAt", Value: time.Now().Format(time.RFC3339)},
	}

	// Marcar cancelAtPeriodEnd si se canceló
	if event.EventType == "subscription.canceled" {
		updates = append(updates, firestore.Update{Path: "subscription.cancelAtPeriodEnd", Value: true})
	} else {
		updates = append(updates, firestore.Update{Path: "subscription.cancelAtPeriodEnd", Value: false})
	}

	userRef := app.Firestore.Collection("users").Doc(userID)
	if _, err := userRef.Update(ctx, updates); err != nil {
		log.Printf("[Paddle Webhook] Error actualizando suscripción en Firestore para user %s: %v", userID, err)
		return
	}

	log.Printf("[Paddle Webhook] Firestore actualizado exitosamente para user %s (plan: %s, status: %s)", userID, planID, status)

	// 5. Si el usuario se degrada a free (cancelación), ejecutar rutina de limpieza
	if event.EventType == "subscription.canceled" {
		go app.handleDowngrade(userID)
	}
}

// resolvePlanFromPriceID mapea el price_id de Paddle al plan interno de Nubbe.
func (app *App) resolvePlanFromPriceID(data PaddleSubscriptionData) string {
	if len(data.Items) == 0 {
		return "free"
	}

	priceID := data.Items[0].Price.ID

	switch {
	case app.PaddleHobbyPriceID != "" && priceID == app.PaddleHobbyPriceID:
		return "hobby"
	case app.PaddleProPriceID != "" && priceID == app.PaddleProPriceID:
		return "pro"
	default:
		// Intento heurístico por si los IDs de precio contienen el nombre del plan
		lowerID := strings.ToLower(priceID)
		if strings.Contains(lowerID, "hobby") {
			return "hobby"
		}
		if strings.Contains(lowerID, "pro") {
			return "pro"
		}
		log.Printf("[Paddle Webhook] ADVERTENCIA: price_id '%s' no mapeado, asignando 'free'", priceID)
		return "free"
	}
}

// projectForDowngrade es un struct ligero para ordenar proyectos durante la degradación.
type projectForDowngrade struct {
	ID        string
	Subdomain string
	CreatedAt time.Time
	State     string
}

// handleDowngrade suspende proyectos excedentes y elimina sus rutas de Cloudflare KV
// cuando un usuario se degrada al plan Free (máximo 5 proyectos activos).
func (app *App) handleDowngrade(userID string) {
	ctx := context.Background()
	maxFreeProjects := 5

	log.Printf("[Downgrade] Iniciando rutina de degradación para user %s", userID)

	// 1. Obtener todos los proyectos del usuario
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

		// Extraer subdomain
		if subdomain, ok := doc.Data()["subdomain"].(string); ok {
			p.Subdomain = subdomain
		}

		// Extraer estado actual
		if statusMap, ok := doc.Data()["status"].(map[string]interface{}); ok {
			if state, ok := statusMap["state"].(string); ok {
				p.State = state
			}
		}

		// Extraer fecha de creación (puede ser timestamp de Firestore o string)
		if createdAt, ok := doc.Data()["createdAt"].(time.Time); ok {
			p.CreatedAt = createdAt
		}

		projects = append(projects, p)
	}

	// Si no excede el límite, no hay nada que hacer
	if len(projects) <= maxFreeProjects {
		log.Printf("[Downgrade] User %s tiene %d proyectos, dentro del límite Free (%d). Sin acción.",
			userID, len(projects), maxFreeProjects)
		return
	}

	// 2. Ordenar por fecha de creación ascendente (los más antiguos primero)
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].CreatedAt.Before(projects[j].CreatedAt)
	})

	// 3. Los primeros maxFreeProjects se mantienen, el resto se suspende
	toSuspend := projects[maxFreeProjects:]
	log.Printf("[Downgrade] User %s: %d proyectos totales, suspendiendo %d excedentes",
		userID, len(projects), len(toSuspend))

	// Obtener credenciales de Cloudflare para eliminar rutas
	cfAccountID, cfToken, cfKVNamespace, cfErr := builders.GetCloudflareCredentials()
	if cfErr != nil {
		log.Printf("[Downgrade] ADVERTENCIA: No se pudieron obtener credenciales de Cloudflare: %v", cfErr)
	}

	for _, proj := range toSuspend {
		// Actualizar estado en Firestore a SUSPENDED
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

		log.Printf("[Downgrade] Proyecto '%s' (subdomain: %s) suspendido en Firestore", proj.ID, proj.Subdomain)

		// Eliminar ruta de Cloudflare KV si tenemos credenciales y subdomain
		if cfErr == nil && proj.Subdomain != "" {
			if err := builders.DeleteRouteInKV(ctx, cfAccountID, cfToken, cfKVNamespace, proj.Subdomain); err != nil {
				log.Printf("[Downgrade] Error eliminando ruta KV para %s: %v", proj.Subdomain, err)
			} else {
				log.Printf("[Downgrade] Ruta KV eliminada para %s.nubbe.run", proj.Subdomain)
			}
		}
	}

	log.Printf("[Downgrade] Rutina de degradación completada para user %s", userID)
}
