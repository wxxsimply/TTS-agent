package schema

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gopkg.in/yaml.v3"

	"github.com/tts-agent/internal/domain"
	"github.com/tts-agent/internal/store"
)

type TableMeta struct {
	Description string            `yaml:"description"`
	Fields      map[string]string `yaml:"fields"`
}

type Router struct {
	db   *store.Postgres
	dim  int
	root string
}

func NewRouter(db *store.Postgres, dim int, root string) *Router {
	return &Router{db: db, dim: dim, root: root}
}

func HashEmbed(text string, dim int) []float32 {
	vec := make([]float32, dim)
	for i := 0; i < dim; i++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", text, i)))
		v := float64(binary.BigEndian.Uint32(h[:4])) / float64(0xFFFFFFFF)
		vec[i] = float32(v*2 - 1)
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v * v)
	}
	if norm > 0 {
		n := float32(1.0 / mathSqrt(norm))
		for i := range vec {
			vec[i] *= n
		}
	}
	return vec
}

func mathSqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func (r *Router) IndexDomain(ctx context.Context, domainName string) error {
	plugin, err := domain.Get(domainName)
	if err != nil {
		return err
	}
	metaBytes, err := os.ReadFile(plugin.MetadataPath)
	if err != nil {
		return err
	}
	var metadata map[string]TableMeta
	if err := yaml.Unmarshal(metaBytes, &metadata); err != nil {
		return err
	}
	ddl, err := os.ReadFile(plugin.DDLPath)
	if err != nil {
		return err
	}
	type indexEntry struct {
		tableName string
		content   string
		embedding []float32
	}
	entries := make([]indexEntry, 0, len(metadata))
	for tableName, info := range metadata {
		var fields strings.Builder
		for k, v := range info.Fields {
			fields.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
		}
		content := fmt.Sprintf("表名: %s\n描述: %s\n字段:\n%s\nDDL片段:\n%s", tableName, info.Description, fields.String(), tableDDL(string(ddl), tableName))
		entries = append(entries, indexEntry{tableName: tableName, content: content, embedding: HashEmbed(content, r.dim)})
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "schema-index:"+domainName); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schema_embeddings WHERE domain = $1`, domainName); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_embeddings (id, domain, table_name, content, embedding) VALUES ($1,$2,$3,$4,$5)`,
			uuid.New(), domainName, entry.tableName, entry.content, pgvector.NewVector(entry.embedding),
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func tableDDL(ddl, tableName string) string {
	pattern := regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?\"?` + regexp.QuoteMeta(tableName) + `\"?\s*\(.*?\);`)
	if matched := pattern.FindString(ddl); matched != "" {
		return strings.TrimSpace(matched)
	}
	return "DDL 未找到，请以字段元数据为准"
}

type SchemaResult struct {
	TableName string  `json:"table_name"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
}

func (r *Router) Retrieve(ctx context.Context, domainName, query string, topK int) ([]SchemaResult, error) {
	vec := pgvector.NewVector(HashEmbed(query, r.dim))
	rows, err := r.db.Pool.Query(ctx,
		`SELECT table_name, content, 1 - (embedding <=> $1) AS score
		 FROM schema_embeddings WHERE domain = $2
		 ORDER BY embedding <=> $1 LIMIT $3`, vec, domainName, topK,
	)
	if err != nil {
		return r.fallbackMetadata(domainName, topK)
	}
	defer rows.Close()
	var out []SchemaResult
	for rows.Next() {
		var s SchemaResult
		if err := rows.Scan(&s.TableName, &s.Content, &s.Score); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if len(out) > 0 {
		return out, nil
	}
	return r.fallbackMetadata(domainName, topK)
}

func (r *Router) fallbackMetadata(domainName string, topK int) ([]SchemaResult, error) {
	plugin, err := domain.Get(domainName)
	if err != nil {
		return nil, err
	}
	metaBytes, err := os.ReadFile(plugin.MetadataPath)
	if err != nil {
		return nil, err
	}
	var metadata map[string]TableMeta
	if err := yaml.Unmarshal(metaBytes, &metadata); err != nil {
		return nil, err
	}
	var out []SchemaResult
	for name, info := range metadata {
		out = append(out, SchemaResult{TableName: name, Content: info.Description, Score: 1})
		if len(out) >= topK {
			break
		}
	}
	return out, nil
}
