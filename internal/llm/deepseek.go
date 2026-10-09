package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("LLM circuit breaker is open")

type APIError struct {
	StatusCode int
	Type       string
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("DeepSeek API error, status code: %d, type: %s, code: %s, message: %s",
		e.StatusCode, e.Type, e.Code, e.Message)
}

func (e *APIError) retryable() bool {
	return e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode >= http.StatusInternalServerError
}

func IsNonRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
	}
	return errors.Is(err, ErrCircuitOpen)
}

func StatusCode(err error) (int, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode, true
	}
	return 0, false
}

type Client struct {
	httpClient            *http.Client
	endpoint              string
	model                 string
	apiKey                string
	mu                    sync.Mutex
	failures              int
	circuitUntil          time.Time
	inputPricePerMillion  float64
	outputPricePerMillion float64
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Thinking    thinking      `json:"thinking"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type thinking struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string          `json:"message"`
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
	} `json:"error,omitempty"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

func NewClient(apiKey, baseURL, model string, timeoutSeconds int) *Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	return &Client{
		httpClient: &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
		endpoint:   baseURL + "/chat/completions",
		model:      model,
		apiKey:     apiKey,
	}
}

// WithPricing configures optional RMB prices per one million tokens. Zero
// keeps cost reporting disabled while token accounting remains available.
func (c *Client) WithPricing(inputPricePerMillion, outputPricePerMillion float64) *Client {
	c.inputPricePerMillion = inputPricePerMillion
	c.outputPricePerMillion = outputPricePerMillion
	return c
}

func (c *Client) HasAPIKey() bool {
	return c.apiKey != ""
}

func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	if !c.HasAPIKey() {
		return "", fmt.Errorf("DeepSeek API key not configured")
	}
	if c.circuitOpen() {
		return "", ErrCircuitOpen
	}

	payload, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0,
		Thinking:    thinking{Type: "disabled"},
	})
	if err != nil {
		return "", fmt.Errorf("encode DeepSeek request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create DeepSeek request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.recordFailure()
		return "", fmt.Errorf("call DeepSeek API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		c.recordFailure()
		return "", fmt.Errorf("read DeepSeek response: %w", err)
	}

	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		if resp.StatusCode >= http.StatusInternalServerError {
			c.recordFailure()
		}
		return "", fmt.Errorf("decode DeepSeek response (status code: %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := decodeAPIError(resp.StatusCode, decoded.Error)
		if apiErr.retryable() {
			c.recordFailure()
		}
		return "", apiErr
	}
	if len(decoded.Choices) == 0 {
		c.recordFailure()
		return "", fmt.Errorf("empty LLM response")
	}

	c.recordSuccess()
	estimatedCost := float64(decoded.Usage.PromptTokens)/1_000_000*c.inputPricePerMillion +
		float64(decoded.Usage.CompletionTokens)/1_000_000*c.outputPricePerMillion
	recordUsage(ctx, decoded.Usage.PromptTokens, decoded.Usage.CompletionTokens, estimatedCost)
	return strings.TrimSpace(decoded.Choices[0].Message.Content), nil
}

func decodeAPIError(statusCode int, payload *struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
}) *APIError {
	result := &APIError{StatusCode: statusCode}
	if payload == nil {
		result.Message = http.StatusText(statusCode)
		return result
	}
	result.Message = payload.Message
	result.Type = payload.Type
	if len(payload.Code) > 0 && string(payload.Code) != "null" {
		if err := json.Unmarshal(payload.Code, &result.Code); err != nil {
			result.Code = string(payload.Code)
		}
	}
	return result
}

func (c *Client) circuitOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Before(c.circuitUntil)
}

func (c *Client) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= 3 {
		c.circuitUntil = time.Now().Add(30 * time.Second)
		c.failures = 0
	}
}

func (c *Client) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
	c.circuitUntil = time.Time{}
}
