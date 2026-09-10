package handlers

import (
	"encoding/json"
	"testing"
)

func TestResolveSubscriptionLevelFromPriceID(t *testing.T) {
	app := &App{
		PaddlePriceHobbyMonthly:  "pri_hobby_m",
		PaddlePriceProMonthly:    "pri_pro_m",
		PaddlePriceHobbyAnnually: "pri_hobby_a",
		PaddlePriceProAnnually:   "pri_pro_a",
	}

	tests := []struct {
		priceID  string
		expected string
	}{
		{"pri_hobby_m", "Hobby_Monthly"},
		{"pri_pro_m", "Pro_Monthly"},
		{"pri_hobby_a", "Hobby_Annually"},
		{"pri_pro_a", "Pro_Annually"},
		{"pri_01kz9hf40ams7nwtmc6rbzddpe", "Hobby_Monthly"},
		{"pri_01kz9hha2svvb7m6qqddrqq7b9", "Pro_Monthly"},
		{"custom_pro_yearly_id", "Pro_Annually"},
		{"custom_hobby_monthly_id", "Hobby_Monthly"},
		{"unknown_price_id", "free"},
		{"", "free"},
	}

	for _, tt := range tests {
		result := app.resolveSubscriptionLevelFromPriceID(tt.priceID)
		if result != tt.expected {
			t.Errorf("resolveSubscriptionLevelFromPriceID(%q) = %q; want %q", tt.priceID, result, tt.expected)
		}
	}
}

func TestResolvePlanAndLimits(t *testing.T) {
	tests := []struct {
		subLevel        string
		expectedPlanID  string
		expectedProjects int
	}{
		{"Hobby_Monthly", "hobby", 20},
		{"Hobby_Annually", "hobby", 20},
		{"hobby", "hobby", 20},
		{"Pro_Monthly", "pro", -1},
		{"Pro_Annually", "pro", -1},
		{"pro", "pro", -1},
		{"free", "free", 5},
		{"unknown", "free", 5},
	}

	for _, tt := range tests {
		planID, limits := resolvePlanAndLimits(tt.subLevel)
		if planID != tt.expectedPlanID {
			t.Errorf("resolvePlanAndLimits(%q) planID = %q; want %q", tt.subLevel, planID, tt.expectedPlanID)
		}
		if limits.MaxProjects != tt.expectedProjects {
			t.Errorf("resolvePlanAndLimits(%q) MaxProjects = %d; want %d", tt.subLevel, limits.MaxProjects, tt.expectedProjects)
		}
	}
}

func TestExtractUserIDAndLevel(t *testing.T) {
	customData := map[string]interface{}{
		"user_id":            "user-abc-123",
		"subscription_level": "Pro_Monthly",
	}

	userID := extractUserID(customData)
	if userID != "user-abc-123" {
		t.Errorf("extractUserID() = %q; want %q", userID, "user-abc-123")
	}

	level := extractSubscriptionLevel(customData)
	if level != "Pro_Monthly" {
		t.Errorf("extractSubscriptionLevel() = %q; want %q", level, "Pro_Monthly")
	}
}

func TestTransactionCompletedDataJSONUnmarshal(t *testing.T) {
	rawJSON := `{
		"id": "txn_12345",
		"status": "completed",
		"customer_id": "ctm_999",
		"subscription_id": "sub_888",
		"custom_data": {
			"user_id": "user-777"
		},
		"billing_period": {
			"starts_at": "2026-09-01T00:00:00Z",
			"ends_at": "2027-09-01T00:00:00Z"
		},
		"details": {
			"receipt_url": "https://paddle.com/receipt/txn_12345"
		}
	}`

	var data TransactionCompletedData
	err := json.Unmarshal([]byte(rawJSON), &data)
	if err != nil {
		t.Fatalf("Failed to unmarshal TransactionCompletedData: %v", err)
	}

	if data.ID != "txn_12345" {
		t.Errorf("data.ID = %q; want %q", data.ID, "txn_12345")
	}
	if data.BillingPeriod == nil || data.BillingPeriod.EndsAt != "2027-09-01T00:00:00Z" {
		t.Errorf("data.BillingPeriod.EndsAt = %v; want %q", data.BillingPeriod, "2027-09-01T00:00:00Z")
	}
	if data.Details == nil || data.Details.ReceiptURL == nil || *data.Details.ReceiptURL != "https://paddle.com/receipt/txn_12345" {
		t.Errorf("data.Details.ReceiptURL invalid")
	}
}
