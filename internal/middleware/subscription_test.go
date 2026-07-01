package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// mockRetriever implementation for testing
type mockRetriever struct {
	sub      Subscription
	subErr   error
	count    int
	countErr error
}

func (m *mockRetriever) GetSubscription(ctx context.Context, userID string) (Subscription, error) {
	return m.sub, m.subErr
}

func (m *mockRetriever) GetProjectCount(ctx context.Context, userID string) (int, error) {
	return m.count, m.countErr
}

func TestSubscriptionLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		userID         string
		mockSub        Subscription
		mockSubErr     error
		mockCount      int
		mockCountErr   error
		expectedStatus int
	}{
		{
			name:           "unauthorized - missing user ID",
			userID:         "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "free user - project count below limit",
			userID:         "user-1",
			mockSub:        Subscription{PlanID: "free"},
			mockCount:      4,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "free user - project count at limit",
			userID:         "user-2",
			mockSub:        Subscription{PlanID: "free"},
			mockCount:      5,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "free user - default plan on database error",
			userID:         "user-3",
			mockSubErr:     errors.New("db error"),
			mockCount:      4, // below free limit of 5
			expectedStatus: http.StatusOK,
		},
		{
			name:           "free user - default plan on database error and at limit",
			userID:         "user-4",
			mockSubErr:     errors.New("db error"),
			mockCount:      5, // at free limit of 5
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "hobby user - project count below limit",
			userID:         "user-5",
			mockSub:        Subscription{PlanID: "hobby"},
			mockCount:      19,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "hobby user - project count at limit",
			userID:         "user-6",
			mockSub:        Subscription{PlanID: "hobby"},
			mockCount:      20,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "pro user - unlimited projects",
			userID:         "user-7",
			mockSub:        Subscription{PlanID: "pro"},
			mockCount:      1000,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "pro user - PlanIDCamel takes precedence",
			userID:         "user-8",
			mockSub:        Subscription{PlanID: "free", PlanIDCamel: "pro"},
			mockCount:      1000,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "project count error",
			userID:         "user-9",
			mockSub:        Subscription{PlanID: "free"},
			mockCountErr:   errors.New("failed to query projects"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup gin context and response recorder
			w := httptest.NewRecorder()
			c, r := gin.CreateTestContext(w)

			// Setup retriever
			retriever := &mockRetriever{
				sub:      tt.mockSub,
				subErr:   tt.mockSubErr,
				count:    tt.mockCount,
				countErr: tt.mockCountErr,
			}

			// Add route with middleware
			r.POST("/test", func(c *gin.Context) {
				if tt.userID != "" {
					c.Set("user_id", tt.userID)
				}
			}, SubscriptionLimitMiddlewareExt(retriever), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			// Create request
			req, _ := http.NewRequest("POST", "/test", nil)
			c.Request = req

			r.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d. Response: %s", tt.expectedStatus, w.Code, w.Body.String())
			}
		})
	}
}
