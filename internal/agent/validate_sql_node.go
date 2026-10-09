package agent

import (
	"context"

	"github.com/tts-agent/internal/security"
)

type ValidateSQLNode struct{}

func NewValidateSQLNode() *ValidateSQLNode {
	return &ValidateSQLNode{}
}

func (n *ValidateSQLNode) ID() NodeID {
	return NodeValidateSQL
}

func (n *ValidateSQLNode) Run(_ context.Context, state *State) (NodeResult, error) {
	if state.DatasetTable != "" {
		if err := security.ValidateDatasetOutput(state.SQL); err != nil {
			state.Errors = append(state.Errors, err.Error())
			return NodeResult{Next: NodeGenerateSQL}, nil
		}
	}
	if state.DatasetTable == "" {
		if err := security.ValidateDomainJoins(state.SQL, state.Domain); err != nil {
			state.Errors = append(state.Errors, err.Error())
			return NodeResult{Next: NodeGenerateSQL}, nil
		}
	}

	result := security.ValidateSQL(state.SQL)
	if !result.Valid {
		state.Errors = append(state.Errors, result.Error)
		return NodeResult{Next: NodeGenerateSQL}, nil
	}
	state.SQL = result.SanitizedSQL

	if state.SeenSQL == nil {
		state.SeenSQL = map[string]struct{}{}
	}
	fingerprint := sqlFingerprint(state.SQL)
	if _, exists := state.SeenSQL[fingerprint]; exists {
		state.Errors = append(state.Errors, "Repeated SQL generated; retry stopped")
		return NodeResult{Next: NodeEnd}, nil
	}
	state.SeenSQL[fingerprint] = struct{}{}

	if state.DatasetTable != "" {
		if err := security.ValidateDatasetTables(state.SQL, state.DatasetTable); err != nil {
			state.Errors = append(state.Errors, err.Error())
			return NodeResult{Next: NodeGenerateSQL}, nil
		}
	}

	return NodeResult{Next: NodeExecuteSQL}, nil
}
