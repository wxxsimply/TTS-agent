package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tts-agent/internal/fewshot"
	"github.com/tts-agent/internal/schema"
)

type fakeChatClient struct {
	hasAPIKey bool
	response  string
	err       error
	system    string
	user      string
}

func (f *fakeChatClient) HasAPIKey() bool {
	return f.hasAPIKey
}

func (f *fakeChatClient) Chat(_ context.Context, system, user string) (string, error) {
	f.system = system
	f.user = user
	return f.response, f.err
}

func TestGenerateSQLNodeID(t *testing.T) {
	node := NewGenerateSQLNode(&fakeChatClient{})
	if node.ID() != NodeGenerateSQL {
		t.Fatalf("unexpected node ID: %s", node.ID())
	}
}

func TestGenerateSQLNodeWritesSQLAndReturnsValidationNode(t *testing.T) {
	client := &fakeChatClient{
		hasAPIKey: true,
		response:  "```sql\nSELECT COUNT(*) FROM orders\n```",
	}
	rows := []map[string]interface{}{{"existing": true}}
	errorsBefore := []string{"previous database error"}
	state := &State{
		Query: "count orders",
		Schemas: []schema.SchemaResult{{
			TableName: "orders",
			Content:   "orders(id)",
		}},
		Examples: []fewshot.Example{{
			Question: "order count",
			SQL:      "SELECT COUNT(*) FROM orders",
		}},
		Rows:            rows,
		Errors:          append([]string(nil), errorsBefore...),
		Attempts:        2,
		DatasetTimeHint: "dataset ends at 2026-06-29",
	}

	result, err := NewGenerateSQLNode(client).Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeValidateSQL {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if state.SQL != "SELECT COUNT(*) FROM orders" {
		t.Fatalf("unexpected SQL: %q", state.SQL)
	}
	if state.Attempts != 2 || !reflect.DeepEqual(state.Rows, rows) || !reflect.DeepEqual(state.Errors, errorsBefore) {
		t.Fatal("node changed unrelated state fields")
	}
	for _, fragment := range []string{"orders(id)", "order count", "previous database error", "dataset ends at 2026-06-29", "count orders"} {
		if !strings.Contains(client.user, fragment) {
			t.Errorf("model prompt is missing %q", fragment)
		}
	}
}

func TestGenerateSQLNodeReturnsModelError(t *testing.T) {
	expected := errors.New("model unavailable")
	client := &fakeChatClient{hasAPIKey: true, err: expected}
	state := &State{Query: "count orders"}

	result, err := NewGenerateSQLNode(client).Run(context.Background(), state)
	if !errors.Is(err, expected) {
		t.Fatalf("expected model error, got %v", err)
	}
	if result.Next != "" {
		t.Fatalf("failed node must not select a next node: %s", result.Next)
	}
	if state.SQL != "" {
		t.Fatalf("failed node must not write SQL: %q", state.SQL)
	}
}

func TestGenerateSQLNodeRejectsMissingAPIKey(t *testing.T) {
	client := &fakeChatClient{}
	_, err := NewGenerateSQLNode(client).Run(context.Background(), &State{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "api key not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}
