package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/tts-agent/internal/agent"
)

func TestWriteAgentRunErrorUsesSafeStatusCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{name: "not found", err: agent.ErrRunNotFound, status: http.StatusNotFound},
		{name: "completed", err: agent.ErrRunCompleted, status: http.StatusConflict},
		{name: "active", err: agent.ErrRunActive, status: http.StatusConflict},
		{name: "not recoverable", err: agent.ErrRunNotRecoverable, status: http.StatusUnprocessableEntity},
		{name: "missing checkpoint", err: agent.ErrCheckpointNotFound, status: http.StatusConflict},
		{name: "version mismatch", err: agent.ErrGraphVersionUnsupported, status: http.StatusConflict},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			writeAgentRunError(ctx, testCase.err)
			if response.Code != testCase.status {
				t.Fatalf("expected status %d, got %d", testCase.status, response.Code)
			}
		})
	}
}
