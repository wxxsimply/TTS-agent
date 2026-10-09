package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

type WorkerConfig struct {
	Concurrency       int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	BackoffBase       time.Duration
	UserConcurrency   int
}

type JobPipeline interface {
	ResumeClaimed(ctx context.Context, job *AgentJob, workerID string, jobs JobStore) (*Result, error)
}

type Worker struct {
	jobs     JobStore
	pipeline JobPipeline
	config   WorkerConfig
	id       string
}

func NewWorker(jobs JobStore, pipeline JobPipeline, config WorkerConfig) *Worker {
	if config.Concurrency < 1 {
		config.Concurrency = 1
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 500 * time.Millisecond
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = time.Minute
	}
	if config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseDuration {
		config.HeartbeatInterval = config.LeaseDuration / 3
	}
	if config.BackoffBase <= 0 {
		config.BackoffBase = 2 * time.Second
	}
	if config.UserConcurrency < 1 {
		config.UserConcurrency = 1
	}
	return &Worker{jobs: jobs, pipeline: pipeline, config: config, id: uuid.NewString()}
}

func (w *Worker) Run(ctx context.Context) {
	var group sync.WaitGroup
	for slot := 0; slot < w.config.Concurrency; slot++ {
		group.Add(1)
		go func(slot int) {
			defer group.Done()
			w.runSlot(ctx, slot)
		}(slot)
	}
	group.Wait()
}

func (w *Worker) runSlot(ctx context.Context, slot int) {
	workerID := w.id + ":" + uuid.NewString()
	reapTicker := time.NewTicker(30 * time.Second)
	defer reapTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		select {
		case <-reapTicker.C:
			if count, err := w.jobs.ReapExpired(ctx); err != nil {
				logWorkerEvent("agent_job_reap_error", workerID, nil, "", err)
			} else if count > 0 {
				logWorkerEvent("agent_jobs_reaped", workerID, nil, "", nil)
			}
		default:
		}
		job, err := w.jobs.ClaimNext(ctx, workerID, w.config.LeaseDuration, w.config.UserConcurrency)
		if errors.Is(err, ErrNoJobAvailable) {
			if !waitForWorker(ctx, w.config.PollInterval) {
				return
			}
			continue
		}
		if err != nil {
			logWorkerEvent("agent_job_claim_error", workerID, nil, "", err)
			if !waitForWorker(ctx, w.config.PollInterval) {
				return
			}
			continue
		}
		logWorkerEvent("agent_job_claimed", workerID, job, JobRunning, nil)
		w.processJob(ctx, workerID, job)
	}
}

func (w *Worker) processJob(parent context.Context, workerID string, job *AgentJob) {
	executionCtx, cancelExecution := context.WithCancel(parent)
	heartbeatCtx, stopHeartbeat := context.WithCancel(parent)
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- w.heartbeat(heartbeatCtx, job.ID, workerID, cancelExecution)
	}()

	result, runErr := w.pipeline.ResumeClaimed(executionCtx, job, workerID, w.jobs)
	stopHeartbeat()
	heartbeatErr := <-heartbeatDone
	cancelExecution()

	outcome := JobOutcome{ErrorType: "runtime_error"}
	if result != nil {
		outcome.Completed = result.Status == RunCompleted && runErr == nil
		if result.ErrorType != "" {
			outcome.ErrorType = result.ErrorType
		}
	}
	if errors.Is(heartbeatErr, ErrJobCancelled) {
		outcome.ErrorType = "cancelled"
	}
	finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancelFinish()
	status, finishErr := w.jobs.FinishJob(finishCtx, job, workerID, outcome, w.config.BackoffBase)
	if finishErr != nil {
		logWorkerEvent("agent_job_finish_error", workerID, job, status, finishErr)
		return
	}
	if (status == JobCompleted || status == JobFailed) && result != nil {
		if err := w.jobs.SaveResultMessage(finishCtx, result); err != nil {
			logWorkerEvent("agent_job_message_error", workerID, job, status, err)
		}
	}
	logWorkerEvent("agent_job_finished", workerID, job, status, runErr)
}

func (w *Worker) heartbeat(ctx context.Context, jobID uuid.UUID, workerID string, cancelExecution context.CancelFunc) error {
	ticker := time.NewTicker(w.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cancelRequested, err := w.jobs.Heartbeat(ctx, jobID, workerID, w.config.LeaseDuration)
			if err != nil {
				cancelExecution()
				return err
			}
			if cancelRequested {
				cancelExecution()
				return ErrJobCancelled
			}
		}
	}
}

func waitForWorker(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func logWorkerEvent(event, workerID string, job *AgentJob, status string, err error) {
	entry := map[string]interface{}{"event": event, "worker_id": workerID}
	if job != nil {
		entry["job_id"] = job.ID.String()
		entry["run_id"] = job.RunID.String()
		entry["attempt"] = job.Attempt
	}
	if status != "" {
		entry["status"] = status
	}
	if err != nil {
		entry["error_type"] = "worker_error"
	}
	payload, _ := json.Marshal(entry)
	log.Print(string(payload))
}
