package store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
}

type EmailVerificationCode struct {
	ID        uuid.UUID
	Email     string
	Purpose   string
	CodeHash  string
	ExpiresAt time.Time
	Consumed  *time.Time
	Attempts  int
	CreatedAt time.Time
}

type Conversation struct {
	ID                    uuid.UUID  `json:"id"`
	UserID                uuid.UUID  `json:"user_id"`
	Title                 string     `json:"title"`
	Domain                string     `json:"domain"`
	DatasetID             *uuid.UUID `json:"dataset_id,omitempty"`
	DataSourceUnavailable bool       `json:"data_source_unavailable"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type DatasetColumn struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type Dataset struct {
	ID         uuid.UUID       `json:"id"`
	UserID     uuid.UUID       `json:"-"`
	Name       string          `json:"name"`
	SourceFile string          `json:"source_file"`
	SheetName  string          `json:"sheet_name"`
	TableName  string          `json:"-"`
	Columns    []DatasetColumn `json:"columns"`
	RowCount   int             `json:"row_count"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Message struct {
	ID             uuid.UUID       `json:"id"`
	ConversationID uuid.UUID       `json:"conversation_id"`
	AgentRunID     *uuid.UUID      `json:"agent_run_id,omitempty"`
	Role           string          `json:"role"`
	Content        string          `json:"content"`
	SQL            *string         `json:"sql,omitempty"`
	ChartConfig    json.RawMessage `json:"chart_config,omitempty"`
	ResultPreview  json.RawMessage `json:"result_preview,omitempty"`
	ResultData     json.RawMessage `json:"result_data,omitempty"`
	Report         json.RawMessage `json:"report,omitempty"`
	ResultCount    int             `json:"result_count"`
	CreatedAt      time.Time       `json:"created_at"`
}

type SchemaEmbedding struct {
	ID        uuid.UUID
	Domain    string
	TableName string
	Content   string
}

type Postgres struct {
	Pool         *pgxpool.Pool
	ReadonlyPool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, dsn, readonlyDSN string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}
	roPool, err := pgxpool.New(ctx, readonlyDSN)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect readonly db: %w", err)
	}
	return &Postgres{Pool: pool, ReadonlyPool: roPool}, nil
}

func (p *Postgres) Close() {
	if p.Pool != nil {
		p.Pool.Close()
	}
	if p.ReadonlyPool != nil {
		p.ReadonlyPool.Close()
	}
}

func (p *Postgres) RunMigrations(ctx context.Context, migrationsDir string) error {
	if _, err := p.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name VARCHAR(255) PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return err
		}
		tx, err := p.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tts-agent-schema-migrations'))`); err != nil {
			tx.Rollback(ctx)
			return err
		}
		var applied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&applied); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if applied {
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, string(data)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) CreateUser(ctx context.Context, email, passwordHash string) (*User, error) {
	u := &User{ID: uuid.New(), Email: email, PasswordHash: passwordHash, CreatedAt: time.Now().UTC()}
	_, err := p.Pool.Exec(ctx,
		`INSERT INTO app_users (id, email, password_hash, created_at) VALUES ($1, $2, $3, $4)`,
		u.ID, u.Email, u.PasswordHash, u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (p *Postgres) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := p.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM app_users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (p *Postgres) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	u := &User{}
	err := p.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM app_users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (p *Postgres) UpdateUserPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	_, err := p.Pool.Exec(ctx, `UPDATE app_users SET password_hash = $2 WHERE id = $1`, id, passwordHash)
	return err
}

func (p *Postgres) SaveRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := p.Pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		uuid.New(), userID, tokenHash, expiresAt,
	)
	return err
}

func (p *Postgres) GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	t := &RefreshToken{}
	err := p.Pool.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (p *Postgres) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := p.Pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE token_hash = $1`, tokenHash)
	return err
}

func (p *Postgres) EmailCodeRetryAfter(ctx context.Context, email, purpose string, resendInterval time.Duration, maxPerHour int) (time.Duration, error) {
	var latest, earliest *time.Time
	var count int
	if err := p.Pool.QueryRow(ctx, `
		SELECT MAX(created_at), MIN(created_at), COUNT(*)
		FROM email_verification_codes
		WHERE email = $1 AND purpose = $2 AND created_at > NOW() - INTERVAL '1 hour'`, email, purpose,
	).Scan(&latest, &earliest, &count); err != nil {
		return 0, err
	}
	now := time.Now()
	if maxPerHour > 0 && count >= maxPerHour && earliest != nil {
		if wait := earliest.Add(time.Hour).Sub(now); wait > 0 {
			return wait, nil
		}
	}
	if latest != nil {
		if wait := latest.Add(resendInterval).Sub(now); wait > 0 {
			return wait, nil
		}
	}
	return 0, nil
}

func (p *Postgres) CreateEmailVerificationCode(ctx context.Context, email, purpose, codeHash string, expiresAt time.Time) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE email_verification_codes SET consumed_at = NOW() WHERE email = $1 AND purpose = $2 AND consumed_at IS NULL`, email, purpose); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO email_verification_codes (id, email, purpose, code_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`, uuid.New(), email, purpose, codeHash, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) InvalidateEmailVerificationCodes(ctx context.Context, email, purpose string) error {
	_, err := p.Pool.Exec(ctx, `UPDATE email_verification_codes SET consumed_at = NOW() WHERE email = $1 AND purpose = $2 AND consumed_at IS NULL`, email, purpose)
	return err
}

func (p *Postgres) ConsumeEmailVerificationCode(ctx context.Context, email, purpose, expectedHash string, maxAttempts int) (bool, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	code := &EmailVerificationCode{}
	err = tx.QueryRow(ctx, `
		SELECT id, email, purpose, code_hash, expires_at, consumed_at, attempts, created_at
		FROM email_verification_codes
		WHERE email = $1 AND purpose = $2
		ORDER BY created_at DESC
		LIMIT 1 FOR UPDATE`, email, purpose,
	).Scan(&code.ID, &code.Email, &code.Purpose, &code.CodeHash, &code.ExpiresAt, &code.Consumed, &code.Attempts, &code.CreatedAt)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if code.Consumed != nil || time.Now().After(code.ExpiresAt) || code.Attempts >= maxAttempts {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE email_verification_codes SET attempts = attempts + 1 WHERE id = $1`, code.ID); err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare([]byte(code.CodeHash), []byte(expectedHash)) != 1 {
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE email_verification_codes SET consumed_at = NOW() WHERE id = $1`, code.ID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (p *Postgres) ListConversations(ctx context.Context, userID uuid.UUID) ([]Conversation, error) {
	rows, err := p.Pool.Query(ctx,
		`SELECT id, user_id, title, domain, dataset_id, data_source_unavailable, created_at, updated_at FROM conversations WHERE user_id = $1 ORDER BY updated_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.Domain, &c.DatasetID, &c.DataSourceUnavailable, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (p *Postgres) CreateConversation(ctx context.Context, userID uuid.UUID, title, domain string) (*Conversation, error) {
	return p.CreateConversationWithDataset(ctx, userID, title, domain, nil)
}

func (p *Postgres) CreateConversationWithDataset(ctx context.Context, userID uuid.UUID, title, domain string, datasetID *uuid.UUID) (*Conversation, error) {
	c := &Conversation{
		ID: uuid.New(), UserID: userID, Title: title, Domain: domain, DatasetID: datasetID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	_, err := p.Pool.Exec(ctx,
		`INSERT INTO conversations (id, user_id, title, domain, dataset_id, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.ID, c.UserID, c.Title, c.Domain, c.DatasetID, c.CreatedAt, c.UpdatedAt,
	)
	return c, err
}

func (p *Postgres) GetConversation(ctx context.Context, id, userID uuid.UUID) (*Conversation, error) {
	c := &Conversation{}
	err := p.Pool.QueryRow(ctx,
		`SELECT id, user_id, title, domain, dataset_id, data_source_unavailable, created_at, updated_at FROM conversations WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(&c.ID, &c.UserID, &c.Title, &c.Domain, &c.DatasetID, &c.DataSourceUnavailable, &c.CreatedAt, &c.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (p *Postgres) ListDatasets(ctx context.Context, userID uuid.UUID) ([]Dataset, error) {
	rows, err := p.Pool.Query(ctx, `SELECT id, user_id, name, source_file, sheet_name, table_name, columns, row_count, created_at FROM datasets WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Dataset, 0)
	for rows.Next() {
		var d Dataset
		var columns []byte
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.SourceFile, &d.SheetName, &d.TableName, &columns, &d.RowCount, &d.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(columns, &d.Columns); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) GetDataset(ctx context.Context, id, userID uuid.UUID) (*Dataset, error) {
	var d Dataset
	var columns []byte
	err := p.Pool.QueryRow(ctx, `SELECT id, user_id, name, source_file, sheet_name, table_name, columns, row_count, created_at FROM datasets WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&d.ID, &d.UserID, &d.Name, &d.SourceFile, &d.SheetName, &d.TableName, &columns, &d.RowCount, &d.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(columns, &d.Columns); err != nil {
		return nil, err
	}
	return &d, nil
}

// GetDatasetDateRange returns the observed range for the first date column in
// an imported dataset. It gives relative-time questions a stable business
// reference even when demo data is older than the system clock.
func (p *Postgres) GetDatasetDateRange(ctx context.Context, dataset *Dataset) (*time.Time, *time.Time, error) {
	if dataset == nil {
		return nil, nil, nil
	}
	dateColumn := ""
	for _, column := range dataset.Columns {
		if column.Type == "TIMESTAMPTZ" {
			dateColumn = column.Name
			break
		}
	}
	if dateColumn == "" {
		return nil, nil, nil
	}
	query := fmt.Sprintf(`SELECT MIN(%s), MAX(%s) FROM %s`,
		pgx.Identifier{dateColumn}.Sanitize(),
		pgx.Identifier{dateColumn}.Sanitize(),
		pgx.Identifier{dataset.TableName}.Sanitize(),
	)
	var minDate, maxDate *time.Time
	if err := p.Pool.QueryRow(ctx, query).Scan(&minDate, &maxDate); err != nil {
		return nil, nil, err
	}
	return minDate, maxDate, nil
}

func (p *Postgres) CreateDataset(ctx context.Context, dataset *Dataset, values [][]interface{}) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	columnDDL := `"_owner_id" UUID NOT NULL, "_row_number" BIGINT NOT NULL`
	columnNames := []string{"_owner_id", "_row_number"}
	for _, column := range dataset.Columns {
		columnDDL += fmt.Sprintf(", %s %s", pgx.Identifier{column.Name}.Sanitize(), column.Type)
		columnNames = append(columnNames, column.Name)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (%s)`, pgx.Identifier{dataset.TableName}.Sanitize(), columnDDL)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ENABLE ROW LEVEL SECURITY`, pgx.Identifier{dataset.TableName}.Sanitize())); err != nil {
		return err
	}
	policySQL := fmt.Sprintf(`CREATE POLICY dataset_owner_policy ON %s FOR SELECT TO agent_readonly USING ("_owner_id" = NULLIF(current_setting('app.current_user_id', true), '')::uuid)`, pgx.Identifier{dataset.TableName}.Sanitize())
	if _, err := tx.Exec(ctx, policySQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`GRANT SELECT ON %s TO agent_readonly`, pgx.Identifier{dataset.TableName}.Sanitize())); err != nil {
		return err
	}

	copyRows := make([][]interface{}, 0, len(values))
	for index, row := range values {
		copyRows = append(copyRows, append([]interface{}{dataset.UserID, int64(index + 1)}, row...))
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{dataset.TableName}, columnNames, pgx.CopyFromRows(copyRows)); err != nil {
		return err
	}
	columnsJSON, _ := json.Marshal(dataset.Columns)
	_, err = tx.Exec(ctx, `INSERT INTO datasets (id, user_id, name, source_file, sheet_name, table_name, columns, row_count, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		dataset.ID, dataset.UserID, dataset.Name, dataset.SourceFile, dataset.SheetName, dataset.TableName, columnsJSON, dataset.RowCount, dataset.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DeleteDataset(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	dataset, err := p.GetDataset(ctx, id, userID)
	if err != nil || dataset == nil {
		return false, err
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE conversations SET data_source_unavailable = TRUE WHERE dataset_id = $1 AND user_id = $2`, id, userID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP TABLE %s`, pgx.Identifier{dataset.TableName}.Sanitize())); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM datasets WHERE id = $1 AND user_id = $2`, id, userID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (p *Postgres) TouchConversation(ctx context.Context, id uuid.UUID) error {
	_, err := p.Pool.Exec(ctx, `UPDATE conversations SET updated_at = $1 WHERE id = $2`, time.Now().UTC(), id)
	return err
}

func (p *Postgres) ListMessages(ctx context.Context, conversationID uuid.UUID) ([]Message, error) {
	rows, err := p.Pool.Query(ctx,
		`SELECT id, conversation_id, agent_run_id, role, content, sql_text, chart_config, result_preview, result_data, report, result_count, created_at
		 FROM messages WHERE conversation_id = $1 ORDER BY created_at ASC`, conversationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.AgentRunID, &m.Role, &m.Content, &m.SQL, &m.ChartConfig, &m.ResultPreview, &m.ResultData, &m.Report, &m.ResultCount, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (p *Postgres) CreateMessage(ctx context.Context, m *Message) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	_, err := p.Pool.Exec(ctx,
		`INSERT INTO messages (id, conversation_id, agent_run_id, role, content, sql_text, chart_config, result_preview, result_data, report, result_count, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		m.ID, m.ConversationID, m.AgentRunID, m.Role, m.Content, m.SQL, m.ChartConfig, m.ResultPreview, m.ResultData, m.Report, m.ResultCount, m.CreatedAt,
	)
	return err
}

func (p *Postgres) GetMessage(ctx context.Context, id, userID uuid.UUID) (*Message, error) {
	m := &Message{}
	err := p.Pool.QueryRow(ctx,
		`SELECT m.id, m.conversation_id, m.agent_run_id, m.role, m.content, m.sql_text, m.chart_config, m.result_preview, m.result_data, m.report, m.result_count, m.created_at
		 FROM messages m JOIN conversations c ON c.id = m.conversation_id
		 WHERE m.id = $1 AND c.user_id = $2`, id, userID,
	).Scan(&m.ID, &m.ConversationID, &m.AgentRunID, &m.Role, &m.Content, &m.SQL, &m.ChartConfig, &m.ResultPreview, &m.ResultData, &m.Report, &m.ResultCount, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return m, err
}

func (p *Postgres) SchemaEmbeddingsExist(ctx context.Context, domain string) (bool, error) {
	var count int
	err := p.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_embeddings WHERE domain = $1`, domain).Scan(&count)
	return count > 0, err
}

func (p *Postgres) InsertSchemaEmbedding(ctx context.Context, domain, tableName, content string, embedding []float32) error {
	vec := pgvector.NewVector(embedding)
	_, err := p.Pool.Exec(ctx,
		`INSERT INTO schema_embeddings (id, domain, table_name, content, embedding) VALUES ($1,$2,$3,$4,$5)`,
		uuid.New(), domain, tableName, content, vec,
	)
	return err
}
