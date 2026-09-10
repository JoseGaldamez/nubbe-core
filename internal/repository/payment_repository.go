package repository

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)


type Features struct {
	CustomDomains   bool `firestore:"customDomains" json:"customDomains"`
	EmailSupport    bool `firestore:"emailSupport" json:"emailSupport"`
	WhatsappSupport bool `firestore:"whatsappSupport" json:"whatsappSupport"`
}

type UserLimits struct {
	MaxProjects       int      `firestore:"maxProjects" json:"maxProjects"`
	MaxBandwidthGB    int      `firestore:"maxBandwidthGB" json:"maxBandwidthGB"`
	BuildMinutesLimit int      `firestore:"buildMinutesLimit" json:"buildMinutesLimit"`
	Features          Features `firestore:"features" json:"features"`
}

type UserSubscription struct {
	PlanID               string `firestore:"planId" json:"planId"`
	PlanIDSnake          string `firestore:"plan_id" json:"plan_id"`
	Level                string `firestore:"level" json:"level"`
	Status               string `firestore:"status" json:"status"`
	PaddleCustomerID     string `firestore:"paddleCustomerId" json:"paddleCustomerId"`
	PaddleSubscriptionID string `firestore:"paddleSubscriptionId" json:"paddleSubscriptionId"`
	PaddleTransactionID  string `firestore:"paddleTransactionId" json:"paddleTransactionId"`
	CurrentPeriodStartsAt string `firestore:"currentPeriodStartsAt,omitempty" json:"currentPeriodStartsAt,omitempty"`
	CurrentPeriodEnd     string `firestore:"currentPeriodEnd" json:"currentPeriodEnd"`
	CancelAtPeriodEnd    bool   `firestore:"cancelAtPeriodEnd" json:"cancelAtPeriodEnd"`
}

type TransactionRecord struct {
	EventID           string    `firestore:"eventId" json:"eventId"`
	TransactionID     string    `firestore:"transactionId" json:"transactionId"`
	UserID            string    `firestore:"userId" json:"userId"`
	SubscriptionLevel string    `firestore:"subscriptionLevel" json:"subscriptionLevel"`
	PlanID            string    `firestore:"planId" json:"planId"`
	Status            string    `firestore:"status" json:"status"`
	PaddleCustomerID  string    `firestore:"paddleCustomerId" json:"paddleCustomerId"`
	PaddleSubID       string    `firestore:"paddleSubscriptionId" json:"paddleSubscriptionId"`
	ReceiptURL        string    `firestore:"receiptUrl,omitempty" json:"receiptUrl,omitempty"`
	InvoicePDF        string    `firestore:"invoicePdf,omitempty" json:"invoicePdf,omitempty"`
	ProcessedAt       time.Time `firestore:"processedAt" json:"processedAt"`
}

type PaymentRepository interface {
	IsEventProcessed(ctx context.Context, eventID string, transactionID string) (bool, error)
	ProcessTransactionCompleted(ctx context.Context, eventID string, txRecord TransactionRecord, sub UserSubscription, limits UserLimits) error
	GetUserPayments(ctx context.Context, userID string) ([]TransactionRecord, error)
	MarkSubscriptionCancelAtPeriodEnd(ctx context.Context, userID string) error
}


type firestorePaymentRepo struct {
	client *firestore.Client
}

func NewPaymentRepository(client *firestore.Client) PaymentRepository {
	return &firestorePaymentRepo{client: client}
}

func (r *firestorePaymentRepo) IsEventProcessed(ctx context.Context, eventID string, transactionID string) (bool, error) {
	if eventID != "" {
		doc, err := r.client.Collection("webhook_events").Doc(eventID).Get(ctx)
		if err == nil && doc.Exists() {
			return true, nil
		}
	}
	if transactionID != "" {
		doc, err := r.client.Collection("webhook_transactions").Doc(transactionID).Get(ctx)
		if err == nil && doc.Exists() {
			return true, nil
		}
	}
	return false, nil
}

func (r *firestorePaymentRepo) ProcessTransactionCompleted(ctx context.Context, eventID string, txRecord TransactionRecord, sub UserSubscription, limits UserLimits) error {
	return r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// 1. Verificación de idempotencia en Firestore dentro de la transacción
		if eventID != "" {
			eventRef := r.client.Collection("webhook_events").Doc(eventID)
			doc, err := tx.Get(eventRef)
			if err == nil && doc.Exists() {
				// Ya fue procesado previamente
				return nil
			}
		}

		if txRecord.TransactionID != "" {
			txRef := r.client.Collection("webhook_transactions").Doc(txRecord.TransactionID)
			doc, err := tx.Get(txRef)
			if err == nil && doc.Exists() {
				// Ya fue procesado previamente
				return nil
			}
		}

		// 2. Registrar evento y transacción
		if eventID != "" {
			eventRef := r.client.Collection("webhook_events").Doc(eventID)
			if err := tx.Set(eventRef, txRecord); err != nil {
				return fmt.Errorf("failed to record webhook event: %w", err)
			}
		}

		if txRecord.TransactionID != "" {
			txRef := r.client.Collection("webhook_transactions").Doc(txRecord.TransactionID)
			if err := tx.Set(txRef, txRecord); err != nil {
				return fmt.Errorf("failed to record transaction: %w", err)
			}
		}

		// 3. Guardar historial de pago en subcolección del usuario
		if txRecord.UserID != "" && txRecord.TransactionID != "" {
			userPayRef := r.client.Collection("users").Doc(txRecord.UserID).Collection("payments").Doc(txRecord.TransactionID)
			_ = tx.Set(userPayRef, txRecord)
		}

		// 4. Actualizar documento principal del usuario (suscripción + límites incrementados)
		if txRecord.UserID != "" {
			userRef := r.client.Collection("users").Doc(txRecord.UserID)
			updates := []firestore.Update{
				{Path: "subscription.planId", Value: sub.PlanID},
				{Path: "subscription.plan_id", Value: sub.PlanID},
				{Path: "subscription.level", Value: sub.Level},
				{Path: "subscription.status", Value: sub.Status},
				{Path: "subscription.paddleCustomerId", Value: sub.PaddleCustomerID},
				{Path: "subscription.paddle_customer_id", Value: sub.PaddleCustomerID},
				{Path: "subscription.paddleSubscriptionId", Value: sub.PaddleSubscriptionID},
				{Path: "subscription.paddle_subscription_id", Value: sub.PaddleSubscriptionID},
				{Path: "subscription.paddleTransactionId", Value: sub.PaddleTransactionID},
				{Path: "subscription.currentPeriodEnd", Value: sub.CurrentPeriodEnd},
				{Path: "subscription.cancelAtPeriodEnd", Value: sub.CancelAtPeriodEnd},
				{Path: "limits.maxProjects", Value: limits.MaxProjects},
				{Path: "limits.maxBandwidthGB", Value: limits.MaxBandwidthGB},
				{Path: "limits.buildMinutesLimit", Value: limits.BuildMinutesLimit},
				{Path: "limits.features.customDomains", Value: limits.Features.CustomDomains},
				{Path: "limits.features.emailSupport", Value: limits.Features.EmailSupport},
				{Path: "limits.features.whatsappSupport", Value: limits.Features.WhatsappSupport},
				{Path: "updatedAt", Value: time.Now().Format(time.RFC3339)},
			}

			if err := tx.Update(userRef, updates); err != nil {
				return tx.Set(userRef, map[string]interface{}{
					"subscription": sub,
					"limits":       limits,
					"updatedAt":    time.Now().Format(time.RFC3339),
				}, firestore.MergeAll)
			}
		}

		return nil
	})
}

func (r *firestorePaymentRepo) GetUserPayments(ctx context.Context, userID string) ([]TransactionRecord, error) {
	iter := r.client.Collection("users").Doc(userID).Collection("payments").Documents(ctx)
	defer iter.Stop()

	var payments []TransactionRecord
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var tx TransactionRecord
		if err := doc.DataTo(&tx); err == nil {
			payments = append(payments, tx)
		}
	}
	if payments == nil {
		payments = []TransactionRecord{}
	}
	return payments, nil
}

func (r *firestorePaymentRepo) MarkSubscriptionCancelAtPeriodEnd(ctx context.Context, userID string) error {
	userRef := r.client.Collection("users").Doc(userID)
	_, err := userRef.Update(ctx, []firestore.Update{
		{Path: "subscription.cancelAtPeriodEnd", Value: true},
		{Path: "updatedAt", Value: time.Now().Format(time.RFC3339)},
	})
	return err
}

