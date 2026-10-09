package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tts-agent/internal/domain"
	"github.com/tts-agent/internal/schema"
	"github.com/tts-agent/internal/store"
)

func RunDDL(ctx context.Context, dsn, domainsPath, domainName string) error {
	plugin, err := domain.Get(domainName)
	if err != nil {
		return err
	}
	ddl, err := os.ReadFile(plugin.DDLPath)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, string(ddl))
	return err
}

func SeedDomain(dsn, domainsPath, domainName string) error {
	plugin, err := domain.Get(domainName)
	if err != nil {
		return err
	}
	return plugin.SeedFn(domainsPath, dsn)
}

func InitSchemaIndex(ctx context.Context, db *store.Postgres, domainsPath string, domainName string, dim int) error {
	router := schema.NewRouter(db, dim, domainsPath)
	return router.IndexDomain(ctx, domainName)
}

func RunMigrations(db *store.Postgres) error {
	migrationsDir := filepath.Join("internal", "store", "migrations")
	return db.RunMigrations(context.Background(), migrationsDir)
}

func EnsureSetup(ctx context.Context, db *store.Postgres, dsn, domainsPath, defaultDomain string, dim int) error {
	if err := RunMigrations(db); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if err := RunDDL(ctx, dsn, domainsPath, defaultDomain); err != nil {
		return fmt.Errorf("ddl: %w", err)
	}
	if err := SeedDomain(dsn, domainsPath, defaultDomain); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if err := InitSchemaIndex(ctx, db, domainsPath, defaultDomain, dim); err != nil {
		return fmt.Errorf("schema index: %w", err)
	}
	return nil
}
