package agent

import (
	"context"
	"strings"
	"testing"
)

func TestValidateSQLNodeID(t *testing.T) {
	if id := NewValidateSQLNode().ID(); id != NodeValidateSQL {
		t.Fatalf("unexpected node ID: %s", id)
	}
}

func TestValidateSQLNodeSanitizesValidSelect(t *testing.T) {
	state := &State{SQL: "SELECT id FROM orders"}
	result, err := NewValidateSQLNode().Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeExecuteSQL {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if !strings.Contains(state.SQL, "agent_guarded_result LIMIT 1000") {
		t.Fatalf("SQL was not sanitized: %s", state.SQL)
	}
	if len(state.SeenSQL) != 1 {
		t.Fatalf("validated SQL fingerprint was not recorded: %#v", state.SeenSQL)
	}
}

func TestValidateSQLNodeRoutesRejectedSQLBackToGeneration(t *testing.T) {
	state := &State{SQL: "DROP TABLE orders"}
	result, err := NewValidateSQLNode().Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeGenerateSQL {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if len(state.Errors) != 1 || !strings.Contains(state.Errors[0], "Dangerous") {
		t.Fatalf("validation error was not recorded: %#v", state.Errors)
	}
}

func TestValidateSQLNodeRejectsDatasetWildcard(t *testing.T) {
	state := &State{SQL: "SELECT * FROM dataset_sales", DatasetTable: "dataset_sales"}
	result, err := NewValidateSQLNode().Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeGenerateSQL {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if len(state.Errors) != 1 || !strings.Contains(state.Errors[0], "wildcard") {
		t.Fatalf("dataset output error was not recorded: %#v", state.Errors)
	}
}

func TestValidateSQLNodeRejectsTableOutsideDataset(t *testing.T) {
	state := &State{SQL: "SELECT id FROM orders", DatasetTable: "dataset_sales"}
	result, err := NewValidateSQLNode().Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeGenerateSQL {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if len(state.Errors) != 1 || !strings.Contains(state.Errors[0], "outside the selected dataset") {
		t.Fatalf("dataset table error was not recorded: %#v", state.Errors)
	}
}

func TestValidateSQLNodeStopsRepeatedSQL(t *testing.T) {
	node := NewValidateSQLNode()
	state := &State{SQL: "SELECT id FROM orders"}
	first, err := node.Run(context.Background(), state)
	if err != nil || first.Next != NodeExecuteSQL {
		t.Fatalf("first validation failed: next=%s err=%v", first.Next, err)
	}

	// Simulate the generation node producing the same SQL on the next attempt.
	state.SQL = "SELECT id FROM orders"
	second, err := node.Run(context.Background(), state)
	if err != nil {
		t.Fatalf("second validation returned error: %v", err)
	}
	if second.Next != NodeEnd {
		t.Fatalf("repeated SQL should stop the run, got %s", second.Next)
	}
	if len(state.Errors) != 1 || !strings.Contains(state.Errors[0], "Repeated SQL") {
		t.Fatalf("repeated SQL error was not recorded: %#v", state.Errors)
	}
}
