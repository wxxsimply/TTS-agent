package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRateLimiterRejectsRequestsOverLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewRateLimiter(2)
	router := gin.New()
	router.GET("/limited", limiter.Middleware(func(c *gin.Context) string {
		return c.GetHeader("X-Test-Key")
	}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for requestNumber := 1; requestNumber <= 3; requestNumber++ {
		request := httptest.NewRequest(http.MethodGet, "/limited", nil)
		request.Header.Set("X-Test-Key", "same-client")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		expected := http.StatusNoContent
		if requestNumber == 3 {
			expected = http.StatusTooManyRequests
		}
		if response.Code != expected {
			t.Fatalf("request %d status = %d, want %d", requestNumber, response.Code, expected)
		}
	}
}

func TestRateLimiterSeparatesKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewRateLimiter(1)
	router := gin.New()
	router.GET("/limited", limiter.Middleware(func(c *gin.Context) string {
		return c.GetHeader("X-Test-Key")
	}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, key := range []string{"client-a", "client-b"} {
		request := httptest.NewRequest(http.MethodGet, "/limited", nil)
		request.Header.Set("X-Test-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("status for %s = %d, want %d", key, response.Code, http.StatusNoContent)
		}
	}
}
