# Configuración y Reglas para el Sistema de Pagos (Paddle Billing v3) en nubbe-core

Este documento describe la arquitectura, modelos de datos, webhooks y endpoints del sistema de pagos y suscripciones en **nubbe-core**.

---

## 1. Modelo de Datos en Firestore (`users/{userID}`)

Cada usuario en Firestore cuenta con un subdocumento de `subscription` y un mapa de `limits`:

```json
{
  "subscription": {
    "planId": "hobby",
    "plan_id": "hobby",
    "level": "Hobby_Monthly",
    "status": "active",
    "paddleCustomerId": "ctm_01h83...",
    "paddleSubscriptionId": "sub_01h83...",
    "paddleTransactionId": "txn_01h83...",
    "currentPeriodStartsAt": "2026-09-09T20:00:00Z",
    "currentPeriodEnd": "2027-09-09T20:00:00Z",
    "cancelAtPeriodEnd": false
  },
  "limits": {
    "maxProjects": 20,
    "maxBandwidthGB": 25,
    "buildMinutesLimit": 150,
    "features": {
      "customDomains": true,
      "emailSupport": true,
      "whatsappSupport": false
    }
  }
}
```

### Historial de Pagos (`users/{userID}/payments/{transactionID}`)
```json
{
  "eventId": "evt_01h83...",
  "transactionId": "txn_01h83...",
  "userId": "user-uuid",
  "subscriptionLevel": "Pro_Monthly",
  "planId": "pro",
  "status": "completed",
  "paddleCustomerId": "ctm_01h83...",
  "paddleSubscriptionId": "sub_01h83...",
  "receiptUrl": "https://buy.paddle.com/receipt/txn_01h83...",
  "processedAt": "2026-09-09T20:00:00Z"
}
```

---

## 2. Eventos de Webhook Soportados (`POST /webhooks/paddle`)

La cabecera `Paddle-Signature` se valida criptográficamente usando el cliente de Paddle en Go con un fallback HMAC.

1. **`transaction.completed`:**
   * Procesa cobros nuevos y renovaciones con transacciones atómicas en Firestore (`webhook_events` y `webhook_transactions`).
   * Extrae la fecha real de fin de ciclo desde `billing_period.ends_at` (compatible con planes Mensuales y Anuales).
   * Almacena `receipt_url` para descarga directa de facturas/recibos en PDF.
2. **`transaction.payment_failed` / `subscription.past_due`:**
   * Actualiza inmediatamente el estado de la suscripción a `"past_due"` en Firestore.
3. **`subscription.updated`:**
   * Evalúa el `price_id` recibido con prioridad para efectuar upgrades de plan en tiempo real (ej. Hobby a Pro).
   * Sincroniza `cancelAtPeriodEnd` leyendo el objeto `scheduled_change`.
4. **`subscription.canceled`:**
   * Degrada los límites a `free` e inicia la rutina en segundo plano `handleDowngrade` (suspende proyectos excedentes a 5 y elimina rutas en Cloudflare KV).

---

## 3. Endpoints HTTP de Pagos (`/api/v1`)

Todos los endpoints están protegidos por `FirebaseAuthMiddleware`:

| Método | Ruta | Descripción |
| :--- | :--- | :--- |
| `GET` | `/api/v1/user/payments` | Devuelve el historial de transacciones del usuario autenticado incluyendo enlaces de recibos/facturas (`receiptUrl`). |
| `POST` | `/api/v1/subscription/cancel` | Programa la cancelación al final del período en Paddle y Firestore (`cancelAtPeriodEnd = true`). |
| `POST` | `/api/v1/subscription/resume` | Revierte la cancelación programada en Paddle y Firestore (`cancelAtPeriodEnd = false`). |
| `GET` | `/api/v1/subscription/portal` | Genera una URL de acceso seguro al Portal de Cliente de Paddle para cambiar tarjeta de crédito o datos de facturación. |
| `GET` | `/api/v1/subscription/sync` | Consulta la API de Paddle en tiempo real para verificar y sincronizar el estado post-checkout de forma síncrona. |

---

## 4. Reglas de Negocio en Middleware (`SubscriptionLimitMiddleware`)

* **Cobros en Mora (`past_due`):** Intercepta peticiones de creación de proyectos respondiendo con `HTTP 402 Payment Required` si la suscripción del usuario está en estado `past_due`.
* **Límites por Plan:**
  * `free`: Máximo 5 proyectos.
  * `hobby`: Máximo 20 proyectos.
  * `pro`: Proyectos ilimitados (`maxProjects = -1`).
