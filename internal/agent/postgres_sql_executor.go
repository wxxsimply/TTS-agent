package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tts-agent/internal/store"
)

// PostgresSQLExecutor runs model-generated SQL with the database-level safety
// controls required by the data agent.
type PostgresSQLExecutor struct {
	db        *store.Postgres
	timeoutMS int
}

func NewPostgresSQLExecutor(db *store.Postgres, timeoutMS int) *PostgresSQLExecutor {
	return &PostgresSQLExecutor{db: db, timeoutMS: timeoutMS}
}

func (e *PostgresSQLExecutor) Execute(ctx context.Context, sql string, userID uuid.UUID) ([]map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.timeoutMS)*time.Millisecond)
	defer cancel()

	tx, err := e.db.ReadonlyPool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%d'", e.timeoutMS)); err != nil {
		return nil, err
	}
	lockTimeout := e.timeoutMS
	if lockTimeout > 2000 {
		lockTimeout = 2000
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", lockTimeout)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL idle_in_transaction_session_timeout = '%dms'", e.timeoutMS)); err != nil {
		return nil, err
	}
	if userID != uuid.Nil {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.current_user_id', $1, true)`, userID.String()); err != nil {
			return nil, err
		}
	}

	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	var out []map[string]interface{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(fields))
		for i, field := range fields {
			row[string(field.Name)] = normalizeValue(values[i])
		}
		out = append(out, row)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case time.Time:
		return typed.Format(time.RFC3339)
	case []byte:
		return string(typed)
	default:
		return typed
	}
}
