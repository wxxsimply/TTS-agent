package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tts-agent/internal/store"
)

func TestPostgresJobQueueIntegration(t *testing.T) {
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
	email := fmt.Sprintf("job-queue-%s@example.test", userID.String())
	if _, err := pool.Exec(ctx, `INSERT INTO app_users (id, email, password_hash) VALUES ($1,$2,'integration-test')`, userID, email); err != nil {
		t.Fatalf("create job test user: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM app_users WHERE id = $1`, userID); err != nil {
			t.Errorf("clean job test user: %v", err)
		}
	}()

	db := &store.Postgres{Pool: pool}
	jobs := NewPostgresJobStore(db)
	recorder := NewPostgresRunRecorder(db)
	state := newQueuedTestState(userID, "first")
	enqueued, err := jobs.Enqueue(ctx, state, EnqueueOptions{IdempotencyKey: "same-request", MaxAttempts: 3, QueueLimit: 10, UserLimit: 3})
	if err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	duplicate, err := jobs.Enqueue(ctx, newQueuedTestState(userID, "duplicate"), EnqueueOptions{IdempotencyKey: "same-request", MaxAttempts: 3, QueueLimit: 10, UserLimit: 3})
	if err != nil {
		t.Fatalf("deduplicate job: %v", err)
	}
	if !duplicate.Deduplicated || duplicate.RunID != enqueued.RunID {
		t.Fatalf("idempotency key created a second run: first=%#v duplicate=%#v", enqueued, duplicate)
	}

	firstClaim, err := jobs.ClaimNext(ctx, "worker-old", time.Minute, 1)
	if err != nil {
		t.Fatalf("claim first job: %v", err)
	}
	firstSnapshot, err := jobs.LoadClaimedSnapshot(ctx, firstClaim, "worker-old")
	if err != nil {
		t.Fatalf("load first claimed checkpoint: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_jobs SET lease_expires_at = NOW() - INTERVAL '1 second' WHERE id = $1`, firstClaim.ID); err != nil {
		t.Fatalf("expire first worker lease: %v", err)
	}
	secondClaim, err := jobs.ClaimNext(ctx, "worker-new", time.Minute, 1)
	if err != nil {
		t.Fatalf("reclaim expired job: %v", err)
	}
	oldState := firstSnapshot.Checkpoint.Restore()
	oldState.JobID, oldState.JobAttempt, oldState.WorkerID = firstClaim.ID, firstClaim.Attempt, "worker-old"
	err = recorder.RecordStep(ctx, oldState, 1, StepTrace{Node: NodeGenerateSQL, Next: NodeChart, Status: StepCompleted})
	if !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale worker was not fenced, got %v", err)
	}

	secondSnapshot, err := jobs.LoadClaimedSnapshot(ctx, secondClaim, "worker-new")
	if err != nil {
		t.Fatalf("load reclaimed checkpoint: %v", err)
	}
	resumedState := secondSnapshot.Checkpoint.Restore()
	resumedState.JobID, resumedState.JobAttempt, resumedState.WorkerID = secondClaim.ID, secondClaim.Attempt, "worker-new"
	generate := &runtimeTestNode{id: NodeGenerateSQL, run: func(_ context.Context, _ *State) (NodeResult, error) {
		return NodeResult{Next: NodeChart}, nil
	}}
	chart := &runtimeTestNode{id: NodeChart, run: func(_ context.Context, state *State) (NodeResult, error) {
		state.ChartConfig = map[string]interface{}{"type": "line"}
		state.Status = RunCompleted
		return NodeResult{Next: NodeEnd}, nil
	}}
	runtime, err := NewGraphRuntime(0, generate, chart)
	if err != nil {
		t.Fatalf("create queue integration runtime: %v", err)
	}
	runtime.WithRecorder(recorder)
	if err := runtime.Resume(ctx, resumedState, secondSnapshot.Checkpoint.NextNode); err != nil {
		t.Fatalf("execute reclaimed job: %v", err)
	}
	status, err := jobs.FinishJob(ctx, secondClaim, "worker-new", JobOutcome{Completed: true}, time.Second)
	if err != nil || status != JobCompleted {
		t.Fatalf("complete reclaimed job: status=%s err=%v", status, err)
	}
	job, err := jobs.GetJob(ctx, enqueued.RunID, userID)
	if err != nil || job.Status != JobCompleted || job.Attempt != 2 {
		t.Fatalf("unexpected completed job: job=%#v err=%v", job, err)
	}
	output, err := recorder.GetResult(ctx, enqueued.RunID, userID)
	if err != nil || output.Status != RunCompleted || output.ChartConfig["type"] != "line" {
		t.Fatalf("queued result was not persisted: output=%#v err=%v", output, err)
	}

	cancelledRun, err := jobs.Enqueue(ctx, newQueuedTestState(userID, "cancel"), EnqueueOptions{MaxAttempts: 2, QueueLimit: 10, UserLimit: 3})
	if err != nil {
		t.Fatalf("enqueue cancellable job: %v", err)
	}
	if status, err := jobs.Cancel(ctx, cancelledRun.RunID, userID); err != nil || status != JobCancelled {
		t.Fatalf("cancel queued job: status=%s err=%v", status, err)
	}

	if _, err := jobs.Enqueue(ctx, newQueuedTestState(userID, "limit-one"), EnqueueOptions{MaxAttempts: 2, QueueLimit: 10, UserLimit: 1}); err != nil {
		t.Fatalf("enqueue user-limit baseline: %v", err)
	}
	if _, err := jobs.Enqueue(ctx, newQueuedTestState(userID, "limit-two"), EnqueueOptions{MaxAttempts: 2, QueueLimit: 10, UserLimit: 1}); !errors.Is(err, ErrUserJobLimit) {
		t.Fatalf("user backpressure was not enforced: %v", err)
	}
	stats, err := jobs.GetStats(ctx, userID)
	if err != nil || stats.Completed != 1 || stats.Cancelled != 1 || stats.Queued != 1 {
		t.Fatalf("unexpected queue stats: stats=%#v err=%v", stats, err)
	}
}

func newQueuedTestState(userID uuid.UUID, query string) *State {
	return &State{
		RunID: uuid.New(), GraphVersion: CurrentGraphVersion,
		UserID: userID, Domain: "ecommerce", Query: query,
		Errors: []string{}, SeenSQL: map[string]struct{}{},
	}
}
