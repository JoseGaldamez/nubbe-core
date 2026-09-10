# Guía de Integración de Pagos en React (`react_nubbe_payment.md`)

Esta guía explica cómo conectar un frontend en **React** con los endpoints y funcionalidades de suscripciones de **nubbe-core** usando **Paddle Billing v3**.

---

## 📌 Resumen de Endpoints Disponibles en Backend

Base URL de la API: `https://api.nubbe.run/api/v1` (o `http://localhost:8080/api/v1`)
Cabecera requerida: `Authorization: Bearer <FIREBASE_ID_TOKEN>`

| Acción | Método | Endpoint | Respuesta Esperada |
| :--- | :---: | :--- | :--- |
| **Historial de Pagos** | `GET` | `/user/payments` | `{ "payments": [{ "transactionId", "receiptUrl", "processedAt", ... }] }` |
| **Cancelar Suscripción** | `POST` | `/subscription/cancel` | `{ "status": "ok", "message": "..." }` |
| **Reactivar Suscripción** | `POST` | `/subscription/resume` | `{ "status": "ok", "message": "..." }` |
| **Portal de Cliente (Tarjeta)** | `GET` | `/subscription/portal` | `{ "url": "https://buy.paddle.com/portal/session/..." }` |
| **Sincronizar Post-Checkout** | `GET` | `/subscription/sync` | `{ "status": "synced", "subscription": {...}, "limits": {...} }` |

---

## 1. Sincronización Inmediata Post-Checkout (Sync)

Cuando el usuario completa la compra en el modal de Paddle, el webhook puede demorar unos segundos. Llama al endpoint `/subscription/sync` inmediatamente después de cerrar el checkout para actualizar el estado en tu aplicación al instante.

### Ejemplo en React (con Paddle.js):

```tsx
import { initializePaddle, Paddle } from '@paddle/paddle-js';
import { useEffect, useState } from 'react';

export function CheckoutButton({ priceId, token }: { priceId: string; token: string }) {
  const [paddle, setPaddle] = useState<Paddle>();

  useEffect(() => {
    initializePaddle({ 
      environment: 'sandbox', // o 'production'
      token: 'PADDLE_CLIENT_SIDE_TOKEN' 
    }).then((paddleInstance) => setPaddle(paddleInstance));
  }, []);

  const handleCheckout = () => {
    if (!paddle) return;

    paddle.Checkout.open({
      items: [{ priceId, quantity: 1 }],
      settings: {
        displayMode: 'overlay',
      },
      eventCallback: async (event) => {
        if (event.name === 'checkout.completed') {
          const transactionId = event.data.transaction_id;

          // 🔄 Sincronización instantánea con el backend de nubbe-core
          const res = await fetch(`http://localhost:8080/api/v1/subscription/sync?transaction_id=${transactionId}`, {
            headers: {
              'Authorization': `Bearer ${token}`
            }
          });
          const data = await res.json();

          if (data.status === 'synced') {
            alert('¡Plan actualizado exitosamente!');
            // Refrescar tu estado global (Zustand, React Query, Context, etc.)
            window.location.reload();
          }
        }
      }
    });
  };

  return <button onClick={handleCheckout}>Suscribirme</button>;
}
```

---

## 2. Manejo del Estado en Mora (`past_due`) y Banner Alerta

Si un cobro recurrente falla, la propiedad `user.subscription.status` será `"past_due"`.
Además, si el usuario intenta crear un proyecto cuando está en mora, el backend devolverá **`HTTP 402 Payment Required`**.

### Banner de Advertencia en React:

```tsx
export function SubscriptionStatusBanner({ user, token }: { user: any; token: string }) {
  const isPastDue = user?.subscription?.status === 'past_due';

  if (!isPastDue) return null;

  const handleOpenPortal = async () => {
    const res = await fetch('http://localhost:8080/api/v1/subscription/portal', {
      headers: { 'Authorization': `Bearer ${token}` }
    });
    const data = await res.json();
    if (data.url) {
      window.open(data.url, '_blank');
    }
  };

  return (
    <div style={{ backgroundColor: '#fee2e2', border: '1px solid #ef4444', padding: '12px', borderRadius: '8px', color: '#991b1b', marginBottom: '16px' }}>
      <strong>⚠️ Tu último cobro ha fallado.</strong> Tu cuenta está en mora. 
      <button onClick={handleOpenPortal} style={{ marginLeft: '12px', padding: '6px 12px', cursor: 'pointer' }}>
        Actualizar Método de Pago 💳
      </button>
    </div>
  );
}
```

---

## 3. Botón "Actualizar Tarjeta / Datos de Facturación" (Customer Portal)

Para permitir al usuario cambiar su tarjeta o datos fiscales en cualquier momento sin cancelar:

```tsx
export function ManageBillingButton({ token }: { token: string }) {
  const [loading, setLoading] = useState(false);

  const handleManageBilling = async () => {
    setLoading(true);
    try {
      const res = await fetch('http://localhost:8080/api/v1/subscription/portal', {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      const data = await res.json();

      if (data.url) {
        window.open(data.url, '_blank');
      } else {
        alert(data.error || 'No se pudo abrir el portal de facturación.');
      }
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  return (
    <button onClick={handleManageBilling} disabled={loading}>
      {loading ? 'Cargando portal...' : '💳 Gestionar Métodos de Pago y Facturación'}
    </button>
  );
}
```

---

## 4. Cancelar y Reactivar Suscripción

Maneja los botones según la propiedad `user.subscription.cancelAtPeriodEnd`:

```tsx
export function SubscriptionManagement({ subscription, token, onUpdate }: any) {
  const isCanceled = subscription?.cancelAtPeriodEnd;

  const handleCancel = async () => {
    if (!confirm('¿Seguro que deseas cancelar tu suscripción al final del período?')) return;

    const res = await fetch('http://localhost:8080/api/v1/subscription/cancel', {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${token}` }
    });
    if (res.ok) {
      alert('Cancelación programada para el final del ciclo.');
      onUpdate();
    }
  };

  const handleResume = async () => {
    const res = await fetch('http://localhost:8080/api/v1/subscription/resume', {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${token}` }
    });
    if (res.ok) {
      alert('¡Suscripción reactivada exitosamente!');
      onUpdate();
    }
  };

  if (subscription?.planId === 'free') {
    return <p>Estás en el plan Gratuito.</p>;
  }

  return (
    <div>
      <p>Estado del Plan: <strong>{subscription.status}</strong></p>
      <p>Vence el: {new Date(subscription.currentPeriodEnd).toLocaleDateString()}</p>

      {isCanceled ? (
        <div>
          <p style={{ color: '#d97706' }}>⚠️ Tu suscripción se cancelará al finalizar el período actual.</p>
          <button onClick={handleResume} style={{ backgroundColor: '#16a34a', color: 'white', padding: '8px 16px' }}>
            🔄 Reactivar Suscripción
          </button>
        </div>
      ) : (
        <button onClick={handleCancel} style={{ backgroundColor: '#dc2626', color: 'white', padding: '8px 16px' }}>
          Cancelar Suscripción
        </button>
      )}
    </div>
  );
}
```

---

## 5. Historial de Pagos y Descarga de Facturas PDF

Renderiza la lista de facturas devueltas por `GET /api/v1/user/payments`:

```tsx
export function PaymentHistory({ token }: { token: string }) {
  const [payments, setPayments] = useState<any[]>([]);

  useEffect(() => {
    fetch('http://localhost:8080/api/v1/user/payments', {
      headers: { 'Authorization': `Bearer ${token}` }
    })
      .then(res => res.json())
      .then(data => setPayments(data.payments || []));
  }, [token]);

  return (
    <table>
      <thead>
        <tr>
          <th>Fecha</th>
          <th>Plan</th>
          <th>Estado</th>
          <th>Factura / Recibo</th>
        </tr>
      </thead>
      <tbody>
        {payments.map((tx) => (
          <tr key={tx.transactionId}>
            <td>{new Date(tx.processedAt).toLocaleDateString()}</td>
            <td>{tx.subscriptionLevel || tx.planId}</td>
            <td>{tx.status}</td>
            <td>
              {tx.receiptUrl ? (
                <a href={tx.receiptUrl} target="_blank" rel="noreferrer">
                  📄 Descargar Recibo PDF
                </a>
              ) : (
                'Sin recibo'
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
```
