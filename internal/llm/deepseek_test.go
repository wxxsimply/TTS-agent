package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChatUsesCurrentModelAndDisablesThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "deepseek-v4-flash" {
			t.Errorf("unexpected model: %s", request.Model)
		}
		if request.Thinking.Type != "disabled" {
			t.Errorf("thinking should be disabled, got %q", request.Thinking.Type)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"SELECT 1"}}]}`))
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL, "deepseek-v4-flash", 5)
	result, err := client.Chat(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if result != "SELECT 1" {
		t.Fatalf("unexpected response: %q", result)
	}
}

func TestBadRequestDoesNotOpenCircuit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid model","type":"invalid_request_error","code":"invalid_request_error"}}`))
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL, "old-model", 5)
	for i := 0; i < 4; i++ {
		_, err := client.Chat(context.Background(), "system", "user")
		if err == nil {
			t.Fatal("expected API error")
		}
		if !IsNonRetryable(err) {
			t.Fatalf("400 should be non-retryable: %v", err)
		}
	}
	if client.circuitOpen() || client.failures != 0 {
		t.Fatal("deterministic 400 errors must not affect the circuit breaker")
	}
}

func TestServerErrorsOpenCircuit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary failure","type":"server_error","code":"server_error"}}`))
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL, "deepseek-v4-flash", 5)
	for i := 0; i < 3; i++ {
		if _, err := client.Chat(context.Background(), "system", "user"); err == nil {
			t.Fatal("expected API error")
		}
	}
	if !client.circuitOpen() {
		t.Fatal("three server failures should open the circuit")
	}
	if _, err := client.Chat(context.Background(), "system", "user"); err != ErrCircuitOpen {
		t.Fatalf("expected circuit-open error, got %v", err)
	}
}

func TestCircuitBreakerOpensAndResets(t *testing.T) {
	client := &Client{}
	client.recordFailure()
	client.recordFailure()
	if client.circuitOpen() {
		t.Fatal("circuit opened before threshold")
	}
	client.recordFailure()
	if !client.circuitOpen() {
		t.Fatal("circuit should be open")
	}
	client.recordSuccess()
	if client.circuitOpen() || client.failures != 0 || !client.circuitUntil.Equal(time.Time{}) {
		t.Fatal("successful call should reset circuit")
	}
}
