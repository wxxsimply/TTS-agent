package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type workerTestPipeline struct {
	run func(context.Context, *AgentJob, string, JobStore) (*Result, error)
}

func (p workerTestPipeline) ResumeClaimed(ctx context.Context, job *AgentJob, workerID string, jobs JobStore) (*Result, error) {
	return p.run(ctx, job, workerID, jobs)
}

type workerTestStore struct {
	mu              sync.Mutex
	cancelHeartbeat bool
	finishOutcome   JobOutcome
	finishStatus    string
	messageSaved    bool
}

func (s *workerTestStore) Enqueue(context.Context, *State, EnqueueOptions) (*EnqueueResult, error) {
	return nil, nil
}
func (s *workerTestStore) ClaimNext(context.Context, string, time.Duration, int) (*AgentJob, error) {
	return nil, ErrNoJobAvailable
}
func (s *workerTestStore) LoadClaimedSnapshot(context.Context, *AgentJob, string) (*ResumeSnapshot, error) {
	return nil, nil
}
func (s *workerTestStore) Heartbeat(context.Context, uuid.UUID, string, time.Duration) (bool, error) {
	return s.cancelHeartbeat, nil
}
func (s *workerTestStore) FinishJob(_ context.Context, _ *AgentJob, _ string, outcome JobOutcome, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishOutcome = outcome
	if s.cancelHeartbeat {
		s.finishStatus = JobCancelled
	} else if outcome.Completed {
		s.finishStatus = JobCompleted
	} else {
		s.finishStatus = JobFailed
	}
	return s.finishStatus, nil
}
func (s *workerTestStore) Cancel(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "", nil
}
func (s *workerTestStore) Retry(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "", nil
}
func (s *workerTestStore) GetJob(context.Context, uuid.UUID, uuid.UUID) (*AgentJob, error) {
	return nil, nil
}
func (s *workerTestStore) ReapExpired(context.Context) (int64, error) { return 0, nil }
func (s *workerTestStore) SaveResultMessage(context.Context, *Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageSaved = true
	return nil
}

func TestWorkerCompletesJobAndPersistsMessage(t *testing.T) {
	store := &workerTestStore{}
	pipeline := workerTestPipeline{run: func(context.Context, *AgentJob, string, JobStore) (*Result, error) {
		return &Result{RunID: uuid.New(), Status: RunCompleted}, nil
	}}
	worker := NewWorker(store, pipeline, WorkerConfig{
		LeaseDuration: time.Second, HeartbeatInterval: 100 * time.Millisecond,
	})
	job := &AgentJob{ID: uuid.New(), RunID: uuid.New(), Attempt: 1}
	worker.processJob(context.Background(), "worker-test", job)
	if !store.finishOutcome.Completed || store.finishStatus != JobCompleted || !store.messageSaved {
		t.Fatalf("worker did not commit successful outcome: %#v", store)
	}
}

func TestWorkerCancelsExecutionWhenHeartbeatRequestsCancellation(t *testing.T) {
	store := &workerTestStore{cancelHeartbeat: true}
	pipeline := workerTestPipeline{run: func(ctx context.Context, _ *AgentJob, _ string, _ JobStore) (*Result, error) {
		<-ctx.Done()
		return &Result{RunID: uuid.New(), Status: RunFailed, ErrorType: "timeout"}, ctx.Err()
	}}
	worker := NewWorker(store, pipeline, WorkerConfig{
		LeaseDuration: 30 * time.Millisecond, HeartbeatInterval: time.Millisecond,
	})
	job := &AgentJob{ID: uuid.New(), RunID: uuid.New(), Attempt: 1}
	worker.processJob(context.Background(), "worker-test", job)
	if store.finishOutcome.ErrorType != "cancelled" || store.finishStatus != JobCancelled {
		t.Fatalf("worker did not honor cancellation: %#v", store)
	}
}
