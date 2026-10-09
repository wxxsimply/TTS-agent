package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRequestIDMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, GetRequestID(c))
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	header := response.Header().Get("X-Request-ID")
	if _, err := uuid.Parse(header); err != nil {
		t.Fatalf("invalid request id header: %q", header)
	}
	if response.Body.String() != header {
		t.Fatal("request id should be available in context")
	}
}
