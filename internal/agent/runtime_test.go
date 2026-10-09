package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type recordingRunRecorder struct {
	startCount   int
	startedRunID uuid.UUID
	version      string
	startNode    NodeID
	sequences    []int
	steps        []StepTrace
	finishStatus string
	errorType    string
	finishCount  int
	recordErrAt  int
}

func (r *recordingRunRecorder) StartRun(_ context.Context, state *State, start NodeID) error {
	r.startCount++
	r.startedRunID = state.RunID
	r.version = state.GraphVersion
	r.startNode = start
	return nil
}

func (r *recordingRunRecorder) RecordStep(_ context.Context, _ *State, sequence int, step StepTrace) error {
	if r.recordErrAt == sequence {
		return fmt.Errorf("record step %d failed", sequence)
	}
	r.sequences = append(r.sequences, sequence)
	r.steps = append(r.steps, step)
	return nil
}

func (r *recordingRunRecorder) FinishRun(_ context.Context, _ *State, status string, errorType string) error {
	r.finishStatus = status
	r.errorType = errorType
	r.finishCount++
	return nil
}

type runtimeTestNode struct {
	id  NodeID
	run func(context.Context, *State) (NodeResult, error)
}

func (n *runtimeTestNode) ID() NodeID {
	return n.id
}

func (n *runtimeTestNode) Run(ctx context.Context, state *State) (NodeResult, error) {
	return n.run(ctx, state)
}

func TestGraphRuntimeExecutesRegisteredRouteAndRecordsSteps(t *testing.T) {
	visited := []NodeID{}
	node := func(id, next NodeID) Node {
		return &runtimeTestNode{id: id, run: func(_ context.Context, state *State) (NodeResult, error) {
			visited = append(visited, id)
			if id == NodeChart {
				state.Status = "completed"
			}
			return NodeResult{Next: next}, nil
		}}
	}
	runtime, err := NewGraphRuntime(1,
		node(NodeGenerateSQL, NodeValidateSQL),
		node(NodeValidateSQL, NodeExecuteSQL),
		node(NodeExecuteSQL, NodeReport),
		node(NodeReport, NodeChart),
		node(NodeChart, NodeEnd),
	)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	state := &State{}
	if err := runtime.Run(context.Background(), state, NodeGenerateSQL); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	expected := []NodeID{NodeGenerateSQL, NodeValidateSQL, NodeExecuteSQL, NodeReport, NodeChart}
	if !reflect.DeepEqual(visited, expected) {
		t.Fatalf("unexpected route: %#v", visited)
	}
	if state.Attempts != 1 || len(state.Steps) != len(expected) {
		t.Fatalf("unexpected attempts or steps: attempts=%d steps=%#v", state.Attempts, state.Steps)
	}
	if state.Steps[len(state.Steps)-1].Next != NodeEnd || state.Status != "completed" {
		t.Fatalf("run did not complete: %#v", state.Steps)
	}
}

func TestGraphRuntimePersistsRunLifecycle(t *testing.T) {
	node := func(id, next NodeID) Node {
		return &runtimeTestNode{id: id, run: func(_ context.Context, state *State) (NodeResult, error) {
			if id == NodeChart {
				state.Status = RunCompleted
			}
			return NodeResult{Next: next}, nil
		}}
	}
	runtime, err := NewGraphRuntime(0,
		node(NodeGenerateSQL, NodeChart),
		node(NodeChart, NodeEnd),
	)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	recorder := &recordingRunRecorder{}
	runtime.WithRecorder(recorder)
	state := &State{}
	if err := runtime.Run(context.Background(), state, NodeGenerateSQL); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if state.RunID == uuid.Nil || recorder.startedRunID != state.RunID {
		t.Fatalf("run ID was not initialized and recorded: state=%s recorder=%s", state.RunID, recorder.startedRunID)
	}
	if state.GraphVersion != CurrentGraphVersion || recorder.version != CurrentGraphVersion {
		t.Fatalf("graph version was not recorded: state=%q recorder=%q", state.GraphVersion, recorder.version)
	}
	if recorder.startNode != NodeGenerateSQL || !reflect.DeepEqual(recorder.sequences, []int{1, 2}) {
		t.Fatalf("unexpected persisted route: start=%s sequences=%v", recorder.startNode, recorder.sequences)
	}
	if recorder.finishCount != 1 || recorder.finishStatus != RunCompleted || recorder.errorType != "" {
		t.Fatalf("run was not finalized as completed: %#v", recorder)
	}
}

func TestGraphRuntimeFinalizesRunWhenStepPersistenceFails(t *testing.T) {
	node := &runtimeTestNode{id: NodeGenerateSQL, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{Next: NodeEnd}, nil
	}}
	runtime, err := NewGraphRuntime(0, node)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	recorder := &recordingRunRecorder{recordErrAt: 1}
	runtime.WithRecorder(recorder)
	state := &State{}
	err = runtime.Run(context.Background(), state, NodeGenerateSQL)
	if err == nil || !strings.Contains(err.Error(), "record step 1 failed") {
		t.Fatalf("step persistence error was not returned: %v", err)
	}
	if recorder.finishCount != 1 || recorder.finishStatus != RunFailed || recorder.errorType != "runtime_error" {
		t.Fatalf("failed run was not finalized: %#v", recorder)
	}
}

func TestGraphRuntimeResumeContinuesFromCheckpointSequence(t *testing.T) {
	node := func(id, next NodeID) Node {
		return &runtimeTestNode{id: id, run: func(_ context.Context, state *State) (NodeResult, error) {
			if id == NodeChart {
				state.Status = RunCompleted
			}
			return NodeResult{Next: next}, nil
		}}
	}
	runtime, err := NewGraphRuntime(1,
		node(NodeReport, NodeChart),
		node(NodeChart, NodeEnd),
	)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	recorder := &recordingRunRecorder{}
	runtime.WithRecorder(recorder)
	state := &State{
		RunID: uuid.New(), GraphVersion: CurrentGraphVersion,
		StepSequence: 3, Attempts: 1, Status: "executed",
	}
	if err := runtime.Resume(context.Background(), state, NodeReport); err != nil {
		t.Fatalf("Resume returned error: %v", err)
	}
	if recorder.startCount != 0 {
		t.Fatalf("resume must not create a second run record: starts=%d", recorder.startCount)
	}
	if !reflect.DeepEqual(recorder.sequences, []int{4, 5}) || state.StepSequence != 5 {
		t.Fatalf("resume did not continue checkpoint sequence: persisted=%v state=%d", recorder.sequences, state.StepSequence)
	}
	if recorder.finishStatus != RunCompleted {
		t.Fatalf("resumed run was not completed: %#v", recorder)
	}
}

func TestGraphRuntimeResumeRejectsIncompatibleVersion(t *testing.T) {
	runtime, err := NewGraphRuntime(0)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	state := &State{RunID: uuid.New(), GraphVersion: "legacy/v0"}
	if err := runtime.Resume(context.Background(), state, NodeGenerateSQL); !errors.Is(err, ErrGraphVersionUnsupported) {
		t.Fatalf("expected graph version error, got %v", err)
	}
}

func TestGraphRuntimeStopsAfterConfiguredAttempts(t *testing.T) {
	generate := &runtimeTestNode{id: NodeGenerateSQL, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{Next: NodeValidateSQL}, nil
	}}
	validate := &runtimeTestNode{id: NodeValidateSQL, run: func(_ context.Context, state *State) (NodeResult, error) {
		state.Errors = append(state.Errors, "invalid SQL")
		return NodeResult{Next: NodeGenerateSQL}, nil
	}}
	runtime, err := NewGraphRuntime(1, generate, validate)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	state := &State{}
	if err := runtime.Run(context.Background(), state, NodeGenerateSQL); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if state.Attempts != 2 || len(state.Steps) != 4 || len(state.Errors) != 2 {
		t.Fatalf("retry limit was not enforced: attempts=%d steps=%d errors=%#v", state.Attempts, len(state.Steps), state.Errors)
	}
	if state.Steps[1].Status != StepRetrying || state.Steps[3].Status != StepRetrying {
		t.Fatalf("validation retries were not traced: %#v", state.Steps)
	}
}

func TestGraphRuntimeStopsOnNonRetryableGenerationError(t *testing.T) {
	generate := &runtimeTestNode{id: NodeGenerateSQL, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{}, errors.New("DeepSeek API key not configured")
	}}
	runtime, err := NewGraphRuntime(3, generate)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	state := &State{}
	if err := runtime.Run(context.Background(), state, NodeGenerateSQL); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if state.Attempts != 1 || len(state.Steps) != 1 || state.Steps[0].Status != StepFailed {
		t.Fatalf("non-retryable failure was not traced: %#v", state.Steps)
	}
	if len(state.Errors) != 1 || !strings.Contains(strings.ToLower(state.Errors[0]), "api key") {
		t.Fatalf("generation error was not recorded: %#v", state.Errors)
	}
}

func TestGraphRuntimeRejectsDuplicateAndMissingNodes(t *testing.T) {
	node := &runtimeTestNode{id: NodeValidateSQL, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{Next: NodeEnd}, nil
	}}
	if _, err := NewGraphRuntime(0, node, node); err == nil {
		t.Fatal("duplicate nodes must be rejected")
	}
	runtime, err := NewGraphRuntime(0, node)
	if err != nil {
		t.Fatalf("NewGraphRuntime returned error: %v", err)
	}
	state := &State{}
	if err := runtime.Run(context.Background(), state, NodeGenerateSQL); err == nil {
		t.Fatal("missing start node must be rejected")
	}
}
