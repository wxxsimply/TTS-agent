package agent

import (
	"context"
	"fmt"
	"strings"
)

type GenerateSQLNode struct {
	client ChatClient
}

func NewGenerateSQLNode(client ChatClient) *GenerateSQLNode {
	return &GenerateSQLNode{client: client}
}

func (n *GenerateSQLNode) ID() NodeID {
	return NodeGenerateSQL
}

func (n *GenerateSQLNode) Run(ctx context.Context, state *State) (NodeResult, error) {
	if err := generateSQL(ctx, n.client, state); err != nil {
		return NodeResult{}, err
	}
	return NodeResult{Next: NodeValidateSQL}, nil
}

func generateSQL(ctx context.Context, client ChatClient, state *State) error {
	if !client.HasAPIKey() {
		return fmt.Errorf("DeepSeek API key not configured")
	}
	var schemaText strings.Builder
	for _, s := range state.Schemas {
		schemaText.WriteString(s.Content)
		schemaText.WriteString("\n\n")
	}
	var fewshotText strings.Builder
	if len(state.Examples) > 0 {
		fewshotText.WriteString("\n\n参考案例：\n")
		for _, ex := range state.Examples {
			fewshotText.WriteString(fmt.Sprintf("问题：%s\nSQL：%s\n", ex.Question, ex.SQL))
		}
	}
	var errorText strings.Builder
	if len(state.Errors) > 0 {
		errorText.WriteString("\n\n上次执行失败，请修正：\n")
		for _, e := range state.Errors {
			errorText.WriteString(e + "\n")
		}
	}
	timeHint := ""
	if state.DatasetTimeHint != "" {
		timeHint = "\n\n" + state.DatasetTimeHint
	}
	prompt := fmt.Sprintf("相关表结构：\n%s%s%s\n\n用户问题：%s%s", schemaText.String(), fewshotText.String(), errorText.String(), state.Query, timeHint)
	sql, err := client.Chat(ctx, systemPrompt, prompt)
	if err != nil {
		return err
	}
	sql = strings.TrimSpace(sql)
	if strings.HasPrefix(sql, "```") {
		lines := strings.Split(sql, "\n")
		if len(lines) > 1 {
			sql = strings.TrimSuffix(strings.Join(lines[1:], "\n"), "```")
			sql = strings.TrimSpace(sql)
		}
	}
	state.SQL = sql
	return nil
}
