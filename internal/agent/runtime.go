package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tts-agent/internal/llm"
)

const CurrentGraphVersion = "tts-sql-agent/v2"
const CurrentPromptVersion = "sql-report/v3"

const (
	RunRunning   = "running"
	RunCompleted = "completed"
	RunFailed    = "failed"
)

type StepStatus string

const (
	StepCompleted StepStatus = "completed"
	StepRetrying  StepStatus = "retrying"
	StepFailed    StepStatus = "failed"
)

// StepTrace is a safe execution trace: it records graph routing and timing but
// never stores prompts, SQL result rows, or model responses.
type StepTrace struct {
	Node       NodeID     `json:"node"`
	Next       NodeID     `json:"next,omitempty"`
	Status     StepStatus `json:"status"`
	Attempt    int        `json:"attempt,omitempty"`
	DurationMS int64      `json:"duration_ms"`
	Error      string     `json:"error,omitempty"`
}

type GraphRuntime struct {
	nodes       map[NodeID]Node
	maxAttempts int
	maxSteps    int
	recorder    RunRecorder
}

func NewGraphRuntime(maxRetries int, nodes ...Node) (*GraphRuntime, error) {
	if maxRetries < 0 {
		maxRetries = 0
	}
	runtime := &GraphRuntime{
		nodes:       make(map[NodeID]Node, len(nodes)),
		maxAttempts: maxRetries + 1,
		maxSteps:    (maxRetries+1)*4 + 8,
		recorder:    NoopRunRecorder{},
	}
	for _, node := range nodes {
		if node == nil {
			return nil, fmt.Errorf("graph contains a nil node")
		}
		id := node.ID()
		if id == "" || id == NodeEnd {
			return nil, fmt.Errorf("invalid graph node ID: %q", id)
		}
		if _, exists := runtime.nodes[id]; exists {
			return nil, fmt.Errorf("duplicate graph node: %s", id)
		}
		runtime.nodes[id] = node
	}
	return runtime, nil
}

// WithRecorder attaches lifecycle persistence while keeping the graph usable
// with a no-op recorder in unit tests and offline tools.
func (r *GraphRuntime) WithRecorder(recorder RunRecorder) *GraphRuntime {
	if recorder == nil {
		r.recorder = NoopRunRecorder{}
	} else {
		r.recorder = recorder
	}
	return r
}

func (r *GraphRuntime) Run(ctx context.Context, state *State, start NodeID) error {
	return r.run(ctx, state, start, false)
}

// Resume continues an already persisted run from the next node stored in its
// latest checkpoint. The caller must atomically acquire the run first.
func (r *GraphRuntime) Resume(ctx context.Context, state *State, start NodeID) error {
	if state == nil || state.RunID == uuid.Nil {
		return fmt.Errorf("resumed graph state or run ID is missing")
	}
	if state.GraphVersion != CurrentGraphVersion {
		return ErrGraphVersionUnsupported
	}
	return r.run(ctx, state, start, true)
}

func (r *GraphRuntime) run(ctx context.Context, state *State, start NodeID, resumed bool) (runErr error) {
	if state == nil {
		return fmt.Errorf("graph state is nil")
	}
	if state.RunID == uuid.Nil {
		state.RunID = uuid.New()
	}
	if state.GraphVersion == "" {
		state.GraphVersion = CurrentGraphVersion
	}
	if state.PromptVersion == "" {
		state.PromptVersion = CurrentPromptVersion
	}
	if state.RunSource == "" {
		state.RunSource = "interactive"
	}
	usage := llm.NewUsageAccumulator(llm.Usage{
		InputTokens: state.InputTokens, OutputTokens: state.OutputTokens,
		Calls: state.LLMCalls, EstimatedCostYuan: state.EstimatedCostYuan,
	})
	runContext := llm.WithUsageAccumulator(ctx, usage)
	syncUsage := func() {
		snapshot := usage.Snapshot()
		state.InputTokens = snapshot.InputTokens
		state.OutputTokens = snapshot.OutputTokens
		state.LLMCalls = snapshot.Calls
		state.EstimatedCostYuan = snapshot.EstimatedCostYuan
	}
	if r.recorder == nil {
		r.recorder = NoopRunRecorder{}
	}
	state.NextNode = start
	if !resumed {
		if err := withRecorderContext(ctx, func(recorderCtx context.Context) error {
			return r.recorder.StartRun(recorderCtx, state, start)
		}); err != nil {
			return err
		}
	}
	defer func() {
		syncUsage()
		status := RunFailed
		errorType := graphRunErrorType(state, runErr)
		if runErr == nil && state.Status == RunCompleted {
			status = RunCompleted
			errorType = ""
		}
		finishErr := withRecorderContext(ctx, func(recorderCtx context.Context) error {
			return r.recorder.FinishRun(recorderCtx, state, status, errorType)
		})
		if finishErr != nil {
			runErr = errors.Join(runErr, finishErr)
		}
	}()

	recordStep := func(trace StepTrace) error {
		syncUsage()
		state.Steps = append(state.Steps, trace)
		state.CurrentNode = trace.Node
		state.NextNode = trace.Next
		sequence := state.StepSequence + 1
		err := withRecorderContext(ctx, func(recorderCtx context.Context) error {
			return r.recorder.RecordStep(recorderCtx, state, sequence, trace)
		})
		if err == nil {
			state.StepSequence = sequence
		}
		return err
	}

	current := start
	for stepIndex := 0; stepIndex < r.maxSteps; stepIndex++ {
		if current == NodeEnd {
			return nil
		}
		if current == NodeGenerateSQL {
			if state.Attempts >= r.maxAttempts {
				return nil
			}
			state.Attempts++
		}

		node, exists := r.nodes[current]
		if !exists {
			return fmt.Errorf("graph node is not registered: %s", current)
		}
		startedAt := time.Now()
		errorCountBefore := len(state.Errors)
		result, err := node.Run(runContext, state)
		trace := StepTrace{
			Node:       current,
			Next:       result.Next,
			Status:     StepCompleted,
			Attempt:    state.Attempts,
			DurationMS: time.Since(startedAt).Milliseconds(),
		}

		if err != nil {
			if current != NodeGenerateSQL {
				trace.Status = StepFailed
				trace.Next = current
				trace.Error = err.Error()
				if recordErr := recordStep(trace); recordErr != nil {
					return recordErr
				}
				return fmt.Errorf("run graph node %s: %w", current, err)
			}
			state.Errors = append(state.Errors, err.Error())
			trace.Error = err.Error()
			if nonRetryableGenerationError(err) || state.Attempts >= r.maxAttempts {
				trace.Status = StepFailed
				trace.Next = NodeGenerateSQL
				if recordErr := recordStep(trace); recordErr != nil {
					return recordErr
				}
				current = NodeEnd
				continue
			}
			if !waitRetryBackoff(ctx, state.Attempts-1) {
				if ctx.Err() != nil {
					state.Errors = append(state.Errors, ctx.Err().Error())
					trace.Error = ctx.Err().Error()
				}
				trace.Status = StepFailed
				trace.Next = NodeGenerateSQL
				if recordErr := recordStep(trace); recordErr != nil {
					return recordErr
				}
				current = NodeEnd
				continue
			}
			trace.Status = StepRetrying
			trace.Next = NodeGenerateSQL
			if recordErr := recordStep(trace); recordErr != nil {
				return recordErr
			}
			current = NodeGenerateSQL
			continue
		}

		if result.Next == "" {
			trace.Status = StepFailed
			trace.Error = "node returned an empty next node"
			if recordErr := recordStep(trace); recordErr != nil {
				return recordErr
			}
			return fmt.Errorf("graph node %s returned an empty next node", current)
		}
		if len(state.Errors) > errorCountBefore {
			trace.Error = state.Errors[len(state.Errors)-1]
			if result.Next == NodeGenerateSQL {
				trace.Status = StepRetrying
			} else if result.Next == NodeEnd {
				trace.Status = StepFailed
			}
		}
		if recordErr := recordStep(trace); recordErr != nil {
			return recordErr
		}
		current = result.Next
	}
	return fmt.Errorf("graph exceeded maximum step count: %d", r.maxSteps)
}

func graphRunErrorType(state *State, runErr error) string {
	if runErr != nil {
		return "runtime_error"
	}
	if state != nil && len(state.Errors) > 0 {
		return classifyAgentError(state.Errors)
	}
	return "generation_error"
}

func withRecorderContext(parent context.Context, operation func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
	defer cancel()
	return operation(ctx)
}

func nonRetryableGenerationError(err error) bool {
	if llm.IsNonRetryable(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "api key not configured") ||
		strings.Contains(message, "unauthorized") ||
		strings.Contains(message, "status code: 400") ||
		strings.Contains(message, "status code: 401") ||
		strings.Contains(message, "status code: 402") ||
		strings.Contains(message, "status code: 403") ||
		strings.Contains(message, "status code: 429") ||
		strings.Contains(message, "rate limit") ||
		strings.Contains(message, "circuit breaker") ||
		strings.Contains(message, "deadline exceeded") ||
		strings.Contains(message, "context canceled")
}

func waitRetryBackoff(ctx context.Context, attempt int) bool {
	delay := 200 * time.Millisecond * time.Duration(1<<attempt)
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
