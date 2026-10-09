package agent

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSQLFingerprintNormalizesFormatting(t *testing.T) {
	first := sqlFingerprint("SELECT  *\nFROM orders")
	second := sqlFingerprint(" select * from ORDERS ")
	if first != second {
		t.Fatalf("fingerprints should match: %q != %q", first, second)
	}
}

func TestDatasetTimeContextUsesDatasetMaximumDate(t *testing.T) {
	minDate := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	maxDate := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)
	hint := datasetTimeContext(&minDate, &maxDate)
	if hint == "" || !containsAll(hint, "2026-04-01", "2026-06-29", "禁止使用 CURRENT_DATE") {
		t.Fatalf("unexpected dataset time context: %s", hint)
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}

func TestAgentErrorClassification(t *testing.T) {
	cases := map[string]string{
		"Repeated SQL generated; retry stopped": "repeated_sql",
		"Database error: statement timeout":     "timeout",
		"status code: 429":                      "llm_rate_limit",
		"Dangerous SQL functions detected":      "security_rejected",
	}
	for message, expected := range cases {
		if actual := classifyAgentError([]string{message}); actual != expected {
			t.Errorf("%q: expected %s, got %s", message, expected, actual)
		}
	}
}

func TestNonRetryableGenerationErrors(t *testing.T) {
	if !nonRetryableGenerationError(errors.New("status code: 400")) {
		t.Fatal("invalid model configuration must not be retried")
	}
	if !nonRetryableGenerationError(errors.New("status code: 429")) {
		t.Fatal("rate limit must not trigger immediate agent retries")
	}
	if nonRetryableGenerationError(errors.New("temporary upstream 500")) {
		t.Fatal("transient server error should remain retryable")
	}
	if !nonRetryableGenerationError(errors.New("status code: 402")) {
		t.Fatal("billing errors must not be retried")
	}
}
