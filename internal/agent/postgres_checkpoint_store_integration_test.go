package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tts-agent/internal/store"
)

func TestPostgresCheckpointResumeIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()

	userID := uuid.New()
	email := fmt.Sprintf("checkpoint-%s@example.test", userID.String())
	if _, err := pool.Exec(ctx, `
		INSERT INTO app_users (id, email, password_hash) VALUES ($1, $2, 'integration-test')`,
		userID, email,
	); err != nil {
		t.Fatalf("create isolated test user: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM app_users WHERE id = $1`, userID); err != nil {
			t.Errorf("clean isolated test user: %v", err)
		}
	}()

	recorder := NewPostgresRunRecorder(&store.Postgres{Pool: pool})
	failingNode := &runtimeTestNode{id: NodeValidateSQL, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{}, errors.New("temporary node crash")
	}}
	firstRuntime, err := NewGraphRuntime(0, failingNode)
	if err != nil {
		t.Fatalf("create failing runtime: %v", err)
	}
	firstRuntime.WithRecorder(recorder)
	state := &State{
		RunID: uuid.New(), GraphVersion: CurrentGraphVersion,
		UserID: userID, Domain: "ecommerce", Query: "integration checkpoint",
		SeenSQL: map[string]struct{}{},
	}
	if err := firstRuntime.Run(ctx, state, NodeValidateSQL); err == nil {
		t.Fatal("first runtime should fail")
	}
	if _, err := recorder.GetRun(ctx, state.RunID, uuid.New()); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("another user must not access the run, got %v", err)
	}

	snapshot, err := recorder.AcquireResume(ctx, state.RunID, userID)
	if err != nil {
		t.Fatalf("acquire persisted run: %v", err)
	}
	if snapshot.Checkpoint.Sequence != 1 || snapshot.Checkpoint.NextNode != NodeValidateSQL {
		t.Fatalf("unexpected persisted checkpoint: %#v", snapshot.Checkpoint)
	}
	if _, err := recorder.AcquireResume(ctx, state.RunID, userID); !errors.Is(err, ErrRunActive) {
		t.Fatalf("a concurrently acquired run must not be resumed twice, got %v", err)
	}

	resumedState := snapshot.Checkpoint.Restore()
	validateNode := &runtimeTestNode{id: NodeValidateSQL, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{Next: NodeExecuteSQL}, nil
	}}
	executeNode := &runtimeTestNode{id: NodeExecuteSQL, run: func(_ context.Context, state *State) (NodeResult, error) {
		state.Rows = []map[string]interface{}{{"metric": 42}}
		state.Status = "executed"
		return NodeResult{Next: NodeReport}, nil
	}}
	reportNode := &runtimeTestNode{id: NodeReport, run: func(_ context.Context, state *State) (NodeResult, error) {
		state.Report = Report{Conclusion: "persisted report"}
		return NodeResult{Next: NodeChart}, nil
	}}
	failingChartNode := &runtimeTestNode{id: NodeChart, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{}, errors.New("temporary chart crash")
	}}
	secondRuntime, err := NewGraphRuntime(0, validateNode, executeNode, reportNode, failingChartNode)
	if err != nil {
		t.Fatalf("create resumed runtime: %v", err)
	}
	secondRuntime.WithRecorder(recorder)
	if err := secondRuntime.Resume(ctx, resumedState, snapshot.Checkpoint.NextNode); err == nil {
		t.Fatal("first resume should fail at chart node")
	}

	secondSnapshot, err := recorder.AcquireResume(ctx, state.RunID, userID)
	if err != nil {
		t.Fatalf("acquire second resume: %v", err)
	}
	if secondSnapshot.Checkpoint.Sequence != 5 || secondSnapshot.Checkpoint.NextNode != NodeChart {
		t.Fatalf("unexpected second checkpoint: %#v", secondSnapshot.Checkpoint)
	}
	if len(secondSnapshot.Rows) != 1 || secondSnapshot.Report.Conclusion != "persisted report" {
		t.Fatalf("run artifacts were not restored: rows=%#v report=%#v", secondSnapshot.Rows, secondSnapshot.Report)
	}
	successChartNode := &runtimeTestNode{id: NodeChart, run: func(_ context.Context, state *State) (NodeResult, error) {
		state.Status = RunCompleted
		return NodeResult{Next: NodeEnd}, nil
	}}
	thirdRuntime, err := NewGraphRuntime(0, successChartNode)
	if err != nil {
		t.Fatalf("create final resumed runtime: %v", err)
	}
	thirdRuntime.WithRecorder(recorder)
	finalState := secondSnapshot.Checkpoint.Restore()
	finalState.Rows = secondSnapshot.Rows
	finalState.Report = secondSnapshot.Report
	if err := thirdRuntime.Resume(ctx, finalState, secondSnapshot.Checkpoint.NextNode); err != nil {
		t.Fatalf("complete second resume: %v", err)
	}

	record, err := recorder.GetRun(ctx, state.RunID, userID)
	if err != nil {
		t.Fatalf("get resumed run: %v", err)
	}
	if record.Status != RunCompleted || record.StepCount != 6 || record.ResumeCount != 2 || record.LatestSequence != 6 {
		t.Fatalf("unexpected resumed run record: %#v", record)
	}
	steps, err := recorder.ListSteps(ctx, state.RunID, userID)
	if err != nil {
		t.Fatalf("list resumed steps: %v", err)
	}
	if len(steps) != 6 || steps[0].Sequence != 1 || steps[5].Sequence != 6 {
		t.Fatalf("step sequence is not idempotent: %#v", steps)
	}
}
