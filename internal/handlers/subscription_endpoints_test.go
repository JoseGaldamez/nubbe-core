package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGetUserPayments_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &App{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req, _ := http.NewRequest("GET", "/api/v1/user/payments", nil)
	c.Request = req

	app.GetUserPayments(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestCancelUserSubscription_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &App{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req, _ := http.NewRequest("POST", "/api/v1/subscription/cancel", nil)
	c.Request = req

	app.CancelUserSubscription(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestResumeUserSubscription_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &App{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req, _ := http.NewRequest("POST", "/api/v1/subscription/resume", nil)
	c.Request = req

	app.ResumeUserSubscription(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestGetCustomerPortalURL_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &App{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req, _ := http.NewRequest("GET", "/api/v1/subscription/portal", nil)
	c.Request = req

	app.GetCustomerPortalURL(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestSyncUserSubscription_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &App{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req, _ := http.NewRequest("GET", "/api/v1/subscription/sync", nil)
	c.Request = req

	app.SyncUserSubscription(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}
