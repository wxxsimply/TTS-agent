package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tts-agent/internal/domain"
)

func TestReportNodeID(t *testing.T) {
	if id := NewReportNode(&fakeChatClient{}).ID(); id != NodeReport {
		t.Fatalf("unexpected node ID: %s", id)
	}
}

func TestReportNodeStoresModelNarrativeAndRoutesToChart(t *testing.T) {
	client := &fakeChatClient{
		hasAPIKey: true,
		response: "## 结论\n\n销售额为 1,286 元。\n\n" +
			"## 关键发现\n\n- 销售表现稳定\n\n" +
			"## 数据说明\n\n- 结果来自测试数据",
	}
	dictionary := &domain.Dictionary{
		Fields: map[string]domain.FieldDefinition{},
		Aliases: map[string]domain.FieldDefinition{
			"total_sales": {Label: "销售额", Format: "currency", Unit: "元"},
		},
		Metrics: map[string]domain.MetricDefinition{},
	}
	rows := []map[string]interface{}{{"total_sales": 1286.0}}
	state := &State{
		Query:      "销售额是多少？",
		Rows:       rows,
		Dictionary: dictionary,
	}

	result, err := NewReportNode(client).Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeChart {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if state.Report.Conclusion != "销售额为 1,286 元。" {
		t.Fatalf("unexpected conclusion: %q", state.Report.Conclusion)
	}
	if state.Report.ColumnLabels["total_sales"] != "销售额" {
		t.Fatalf("dictionary label was not preserved: %#v", state.Report.ColumnLabels)
	}
	if len(state.Report.Insights) != 1 || len(state.Report.Caveats) != 1 {
		t.Fatalf("markdown sections were not parsed: %#v", state.Report)
	}
	if !reflect.DeepEqual(state.Rows, rows) {
		t.Fatal("report generation changed source rows")
	}
	for _, fragment := range []string{"销售额是多少？", "total_sales", "1286"} {
		if !strings.Contains(client.user, fragment) {
			t.Errorf("report prompt is missing %q", fragment)
		}
	}
}

func TestReportNodeFallsBackWithoutAPIKey(t *testing.T) {
	client := &fakeChatClient{}
	state := &State{
		Query: "订单数是多少？",
		Rows:  []map[string]interface{}{{"order_count": int64(12)}},
	}

	result, err := NewReportNode(client).Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeChart {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if state.Report.Conclusion == "" || state.Report.DataScope.RowCount != 1 {
		t.Fatalf("base report was not generated: %#v", state.Report)
	}
	if client.system != "" || client.user != "" {
		t.Fatal("model must not be called without an API key")
	}
	if state.Dictionary == nil {
		t.Fatal("node did not initialize a missing dictionary")
	}
}

func TestReportNodeFallsBackOnModelError(t *testing.T) {
	dictionary := &domain.Dictionary{
		Fields:  map[string]domain.FieldDefinition{},
		Aliases: map[string]domain.FieldDefinition{},
		Metrics: map[string]domain.MetricDefinition{},
	}
	rows := []map[string]interface{}{{"order_count": int64(12)}}
	expected := buildBaseReport("订单数是多少？", rows, dictionary)
	client := &fakeChatClient{hasAPIKey: true, err: errors.New("model unavailable")}
	state := &State{Query: "订单数是多少？", Rows: rows, Dictionary: dictionary}

	result, err := NewReportNode(client).Run(context.Background(), state)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Next != NodeChart {
		t.Fatalf("unexpected next node: %s", result.Next)
	}
	if state.Report.Conclusion != expected.Conclusion || state.Report.DataScope.RowCount != 1 {
		t.Fatalf("model failure did not preserve the base report: %#v", state.Report)
	}
	if !reflect.DeepEqual(state.Rows, rows) {
		t.Fatal("model failure changed source rows")
	}
}
