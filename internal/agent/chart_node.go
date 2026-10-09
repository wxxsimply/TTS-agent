package agent

import (
	"context"

	"github.com/tts-agent/internal/chart"
)

type ChartNode struct{}

func NewChartNode() *ChartNode {
	return &ChartNode{}
}

func (n *ChartNode) ID() NodeID {
	return NodeChart
}

func (n *ChartNode) Run(_ context.Context, state *State) (NodeResult, error) {
	state.ChartConfig = chart.BuildChartConfig(
		state.Rows,
		state.Query,
		state.Report.ColumnLabels,
	)
	state.Status = RunCompleted
	return NodeResult{Next: NodeEnd}, nil
}
