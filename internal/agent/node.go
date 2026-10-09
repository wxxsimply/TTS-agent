package agent

import "context"

type NodeID string

const (
	NodeGenerateSQL NodeID = "generate_sql"
	NodeValidateSQL NodeID = "validate_sql"
	NodeExecuteSQL  NodeID = "execute_sql"
	NodeReport      NodeID = "generate_report"
	NodeChart       NodeID = "generate_chart"
	NodeEnd         NodeID = "end"
)

type NodeResult struct {
	Next NodeID
}

type Node interface {
	ID() NodeID
	Run(ctx context.Context, state *State) (NodeResult, error)
}

// ChatClient is the smallest model contract needed by agent nodes.
type ChatClient interface {
	Chat(ctx context.Context, system, user string) (string, error)
	HasAPIKey() bool
}
