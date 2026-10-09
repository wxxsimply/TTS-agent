package agent

import (
	"context"
	"testing"
)

func TestChartNodeBuildsChartAndCompletesRun(t *testing.T) {
	state := &State{
		Query: "各渠道销售额",
		Rows: []map[string]interface{}{
			{"channel": "自然搜索", "sales": 120.0},
			{"channel": "广告", "sales": 80.0},
		},
		Report: Report{ColumnLabels: map[string]string{
			"channel": "渠道",
			"sales":   "销售额",
		}},
	}

	result, err := NewChartNode().Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeEnd {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if state.Status != "completed" {
		t.Fatalf("unexpected status: %s", state.Status)
	}
	if state.ChartConfig == nil {
		t.Fatal("chart configuration was not generated")
	}
}

func TestChartNodeCompletesWithoutChartableRows(t *testing.T) {
	state := &State{Query: "订单数", Report: Report{ColumnLabels: map[string]string{}}}
	result, err := NewChartNode().Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeEnd || state.Status != "completed" {
		t.Fatalf("unexpected result: next=%s status=%s", result.Next, state.Status)
	}
	if state.ChartConfig != nil {
		t.Fatalf("empty rows should not produce a chart: %#v", state.ChartConfig)
	}
}
