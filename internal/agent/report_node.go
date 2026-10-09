package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tts-agent/internal/domain"
)

type ReportNode struct {
	client ChatClient
}

func NewReportNode(client ChatClient) *ReportNode {
	return &ReportNode{client: client}
}

func (n *ReportNode) ID() NodeID {
	return NodeReport
}

func (n *ReportNode) Run(ctx context.Context, state *State) (NodeResult, error) {
	dictionary := state.Dictionary
	if dictionary == nil {
		dictionary = &domain.Dictionary{
			Fields:  map[string]domain.FieldDefinition{},
			Aliases: map[string]domain.FieldDefinition{},
			Metrics: map[string]domain.MetricDefinition{},
		}
		state.Dictionary = dictionary
	}

	report := buildBaseReport(state.Query, state.Rows, dictionary)
	report = summarizeReport(ctx, n.client, state, report)
	state.Report = report

	return NodeResult{Next: NodeChart}, nil
}

func summarizeReport(ctx context.Context, client ChatClient, state *State, report Report) Report {
	if len(state.Rows) == 0 || client == nil || !client.HasAPIKey() {
		return report
	}
	preview := state.Rows
	if len(preview) > 10 {
		preview = preview[:10]
	}
	rowsJSON, _ := json.Marshal(preview)
	metricsJSON, _ := json.Marshal(report.Metrics)
	metricDefinitionsJSON, _ := json.Marshal(state.Dictionary.Metrics)
	prompt := fmt.Sprintf("用户问题：%s\n查询结果（前10条，共%d条）：%s\n已计算核心指标：%s\n业务指标定义：%s\n\n请直接返回以下 Markdown 格式的中文报告，不要使用 JSON，不要添加代码块：\n\n## 结论\n用1-3句话直接回答问题。\n\n## 关键发现\n- 最多3条有业务价值的发现\n\n## 数据说明\n- 只写数据确实无法支持的限制；没有限制时省略本节\n\n不得编造查询结果中不存在的数字。",
		state.Query, len(state.Rows), string(rowsJSON), string(metricsJSON), string(metricDefinitionsJSON))
	raw, err := client.Chat(ctx, "你是面向中国电商业务人员的数据分析助手。所有报告必须使用简体中文，先说结论，再说关键发现。", prompt)
	if err != nil {
		return report
	}
	return applyMarkdownNarrative(report, raw)
}
