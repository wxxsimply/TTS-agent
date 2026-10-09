package agent

import (
	"context"

	"github.com/google/uuid"
)

// SQLExecutor is the database contract needed by the SQL execution node.
type SQLExecutor interface {
	Execute(ctx context.Context, sql string, userID uuid.UUID) ([]map[string]interface{}, error)
}
