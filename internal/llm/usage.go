package llm

import (
	"context"
	"sync"
)

type Usage struct {
	InputTokens       int64
	OutputTokens      int64
	Calls             int
	EstimatedCostYuan float64
}

type UsageAccumulator struct {
	mu    sync.Mutex
	usage Usage
}

func NewUsageAccumulator(initial Usage) *UsageAccumulator {
	return &UsageAccumulator{usage: initial}
}

func (a *UsageAccumulator) Add(inputTokens, outputTokens int64, estimatedCostYuan float64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.usage.InputTokens += inputTokens
	a.usage.OutputTokens += outputTokens
	a.usage.Calls++
	a.usage.EstimatedCostYuan += estimatedCostYuan
}

func (a *UsageAccumulator) Snapshot() Usage {
	if a == nil {
		return Usage{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usage
}

type usageContextKey struct{}

func WithUsageAccumulator(ctx context.Context, accumulator *UsageAccumulator) context.Context {
	return context.WithValue(ctx, usageContextKey{}, accumulator)
}

func recordUsage(ctx context.Context, inputTokens, outputTokens int64, estimatedCostYuan float64) {
	accumulator, _ := ctx.Value(usageContextKey{}).(*UsageAccumulator)
	accumulator.Add(inputTokens, outputTokens, estimatedCostYuan)
}
