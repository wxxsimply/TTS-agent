package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestStateCheckpointRoundTripKeepsResumeState(t *testing.T) {
	runID := uuid.New()
	state := &State{
		RunID: runID, GraphVersion: CurrentGraphVersion, Domain: "ecommerce",
		Query: "sales trend", SQL: "SELECT day, sales FROM orders LIMIT 1000",
		Attempts: 2, Errors: []string{"temporary database error"},
		SeenSQL:      map[string]struct{}{"select day, sales from orders limit 1000": {}},
		DatasetTable: "dataset_1", Rows: []map[string]interface{}{{"sales": 42}},
		Report: Report{Conclusion: "sensitive derived output"},
	}
	checkpoint := checkpointFromState(state, NodeExecuteSQL, NodeReport, 4)
	restored := checkpoint.Restore()
	if restored.RunID != runID || restored.StepSequence != 4 || restored.NextNode != NodeReport {
		t.Fatalf("resume identity was not restored: %#v", restored)
	}
	if restored.SQL != state.SQL || restored.Attempts != 2 || len(restored.SeenSQL) != 1 {
		t.Fatalf("resume execution state was not restored: %#v", restored)
	}
	if restored.Rows != nil || restored.Report.Conclusion != "" {
		t.Fatal("large artifacts must not be embedded in checkpoint state")
	}
}

func TestStateCheckpointJSONExcludesArtifactsAndTraces(t *testing.T) {
	state := &State{
		RunID: uuid.New(), GraphVersion: CurrentGraphVersion, Query: "question",
		Rows:   []map[string]interface{}{{"secret_result_marker": 42}},
		Report: Report{Conclusion: "secret_report_marker"},
		Steps:  []StepTrace{{Node: NodeExecuteSQL, Error: "secret_trace_marker"}},
	}
	payload, err := json.Marshal(checkpointFromState(state, NodeExecuteSQL, NodeReport, 1))
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	encoded := string(payload)
	for _, forbidden := range []string{"secret_result_marker", "secret_report_marker", "secret_trace_marker", `"rows":`, `"report":`, `"steps":`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("checkpoint leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRecoverableErrorTypesAreRestricted(t *testing.T) {
	runtimeError := "runtime_error"
	timeout := "timeout"
	configuration := "llm_configuration"
	if !recoverableErrorType(&runtimeError) || !recoverableErrorType(&timeout) {
		t.Fatal("infrastructure failures should be recoverable")
	}
	if recoverableErrorType(&configuration) || recoverableErrorType(nil) {
		t.Fatal("configuration and unclassified failures must not be automatically recoverable")
	}
}
