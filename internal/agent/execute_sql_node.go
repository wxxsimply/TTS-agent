package agent

import "context"

type ExecuteSQLNode struct {
	executor SQLExecutor
}

func NewExecuteSQLNode(executor SQLExecutor) *ExecuteSQLNode {
	return &ExecuteSQLNode{executor: executor}
}

func (n *ExecuteSQLNode) ID() NodeID {
	return NodeExecuteSQL
}

func (n *ExecuteSQLNode) Run(ctx context.Context, state *State) (NodeResult, error) {
	rows, err := n.executor.Execute(ctx, state.SQL, state.UserID)
	if err != nil {
		state.Errors = append(state.Errors, "Database error: "+err.Error())
		return NodeResult{Next: NodeGenerateSQL}, nil
	}

	state.Rows = rows
	state.Status = "executed"
	return NodeResult{Next: NodeReport}, nil
}
