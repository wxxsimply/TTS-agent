package agent

import (
	"strings"
	"testing"

	"github.com/tts-agent/internal/domain"
)

func TestBuildBaseReportUsesChineseDictionary(t *testing.T) {
	dict := &domain.Dictionary{
		Fields: map[string]domain.FieldDefinition{},
		Aliases: map[string]domain.FieldDefinition{
			"total_sales": {Label: "销售额", Format: "currency", Unit: "美元"},
			"email":       {Label: "客户邮箱", Format: "text", Sensitive: true},
		},
		Metrics: map[string]domain.MetricDefinition{},
	}
	report := buildBaseReport("上月销售额是多少？", []map[string]interface{}{{
		"total_sales": 1286000.5,
		"email":       "buyer@example.com",
	}}, dict)

	if report.ColumnLabels["total_sales"] != "销售额" {
		t.Fatalf("unexpected label: %q", report.ColumnLabels["total_sales"])
	}
	if !report.SensitiveColumns["email"] {
		t.Fatal("email should be marked sensitive")
	}
	if len(report.Metrics) != 1 || report.Metrics[0].DisplayValue != "1,286,000.50 美元" {
		t.Fatalf("unexpected metrics: %#v", report.Metrics)
	}
}

func TestApplyNarrativeLimitsInsights(t *testing.T) {
	report := Report{Conclusion: "默认结论"}
	report = applyNarrative(report, `{"conclusion":"销售额保持增长。","insights":["一","二","三","四"],"caveats":["仅供参考"]}`)

	if report.Conclusion != "销售额保持增长。" {
		t.Fatalf("unexpected conclusion: %q", report.Conclusion)
	}
	if len(report.Insights) != 3 {
		t.Fatalf("expected 3 insights, got %d", len(report.Insights))
	}
}

func TestApplyMarkdownNarrative(t *testing.T) {
	report := Report{Conclusion: "默认结论", Caveats: []string{}}
	report = applyMarkdownNarrative(report, "## 结论\n\n订单总数为 5,000 单。\n\n## 关键发现\n\n- 订单规模稳定\n- 退款订单需要关注\n\n## 数据说明\n\n- 结果来自测试数据")

	if report.Conclusion != "订单总数为 5,000 单。" {
		t.Fatalf("unexpected conclusion: %q", report.Conclusion)
	}
	if len(report.Insights) != 2 || len(report.Caveats) != 1 {
		t.Fatalf("unexpected markdown sections: %#v", report)
	}
	if !strings.Contains(report.Markdown, "## 关键发现") {
		t.Fatalf("markdown was not preserved: %q", report.Markdown)
	}
}

func TestDetectTimeRange(t *testing.T) {
	rows := []map[string]interface{}{
		{"order_date": "2026-07-03"},
		{"order_date": "2026-07-01"},
	}
	if got := detectTimeRange(rows); got != "2026-07-01 至 2026-07-03" {
		t.Fatalf("unexpected range: %q", got)
	}
}
