package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type fakeSQLExecutor struct {
	rows   []map[string]interface{}
	err    error
	sql    string
	userID uuid.UUID
}

func (f *fakeSQLExecutor) Execute(_ context.Context, sql string, userID uuid.UUID) ([]map[string]interface{}, error) {
	f.sql = sql
	f.userID = userID
	return f.rows, f.err
}

func TestExecuteSQLNodeID(t *testing.T) {
	if id := NewExecuteSQLNode(&fakeSQLExecutor{}).ID(); id != NodeExecuteSQL {
		t.Fatalf("unexpected node ID: %s", id)
	}
}

func TestExecuteSQLNodeStoresRowsAndRoutesToReport(t *testing.T) {
	userID := uuid.New()
	rows := []map[string]interface{}{{"order_count": int64(12)}}
	executor := &fakeSQLExecutor{rows: rows}
	state := &State{
		SQL:    "SELECT COUNT(*) AS order_count FROM orders",
		UserID: userID,
	}

	result, err := NewExecuteSQLNode(executor).Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeReport {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if state.Status != "executed" {
		t.Fatalf("unexpected state status: %s", state.Status)
	}
	if !reflect.DeepEqual(state.Rows, rows) {
		t.Fatalf("unexpected rows: %#v", state.Rows)
	}
	if executor.sql != state.SQL || executor.userID != userID {
		t.Fatalf("executor received sql=%q userID=%s", executor.sql, executor.userID)
	}
}

func TestExecuteSQLNodeRecordsDatabaseErrorAndRetriesGeneration(t *testing.T) {
	executor := &fakeSQLExecutor{err: errors.New("statement timeout")}
	state := &State{
		SQL:    "SELECT id FROM orders",
		Rows:   []map[string]interface{}{{"existing": true}},
		Status: "validated",
	}

	result, err := NewExecuteSQLNode(executor).Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeGenerateSQL {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if state.Status == "executed" {
		t.Fatal("failed execution must not mark the state as executed")
	}
	if len(state.Errors) != 1 || !strings.Contains(state.Errors[0], "Database error: statement timeout") {
		t.Fatalf("database error was not recorded: %#v", state.Errors)
	}
	if len(state.Rows) != 1 {
		t.Fatal("failed execution changed unrelated state rows")
	}
}
