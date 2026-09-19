// internal/postgres/postgres.go; adapted: Ready() checks for the platform
// "tasks" table only, not the domain tables (runs/run_items/dialogues/
// evaluations/run_results) that were not ported — see
// docs/technical-discovery.md §3.5.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"emsim/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

const ExpectedSchemaVersion int64 = 1

var (
	ErrDatabaseURLRequired = errors.New("database URL is required")
	ErrDatabaseConfig      = errors.New("database configuration is invalid")
	ErrDatabaseUnavailable = errors.New("database is unavailable")
	ErrMigrationFailed     = errors.New("database migration failed")
	ErrSchemaInspection    = errors.New("schema inspection failed")
)

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, ErrDatabaseURLRequired
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", ErrDatabaseConfig)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", ErrDatabaseUnavailable)
	}
	return pool, nil
}

func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := pool.Ping(operationCtx); err != nil {
		return fmt.Errorf("ping database: %w", ErrDatabaseUnavailable)
	}
	return nil
}

func Up(ctx context.Context, databaseURL string) error {
	return migrate(ctx, databaseURL, true)
}

func Down(ctx context.Context, databaseURL string) error {
	return migrate(ctx, databaseURL, false)
}

func CurrentVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var exists bool
	if err := pool.QueryRow(operationCtx, "SELECT to_regclass('public.goose_db_version') IS NOT NULL").Scan(&exists); err != nil {
		return 0, fmt.Errorf("inspect migration table: %w", ErrSchemaInspection)
	}
	if !exists {
		return 0, nil
	}

	var version int64
	if err := pool.QueryRow(operationCtx, "SELECT COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0) FROM public.goose_db_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read migration version: %w", ErrSchemaInspection)
	}
	return version, nil
}

func Ready(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	version, err := CurrentVersion(ctx, pool)
	if err != nil {
		return false, err
	}
	if version != ExpectedSchemaVersion {
		return false, nil
	}

	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var tableCount int
	if err := pool.QueryRow(operationCtx, `
		SELECT count(*)
		FROM pg_catalog.pg_tables
		WHERE schemaname = 'public'
		  AND tablename = 'tasks'
	`).Scan(&tableCount); err != nil {
		return false, fmt.Errorf("inspect application tables: %w", ErrSchemaInspection)
	}
	return tableCount == 1, nil
}

func migrate(ctx context.Context, databaseURL string, up bool) error {
	db, err := openSQL(databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	operationCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if err := db.PingContext(operationCtx); err != nil {
		return fmt.Errorf("ping migration database: %w", ErrDatabaseUnavailable)
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations.Files,
		goose.WithDisableGlobalRegistry(true),
		goose.WithSlog(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", ErrMigrationFailed)
	}

	if up {
		if _, err := provider.Up(operationCtx); err != nil {
			return fmt.Errorf("migrate up: %w", ErrMigrationFailed)
		}
		return nil
	}

	if _, err := provider.Down(operationCtx); err != nil {
		return fmt.Errorf("migrate down: %w", ErrMigrationFailed)
	}
	return nil
}

func openSQL(databaseURL string) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, ErrDatabaseURLRequired
	}

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open migration database: %w", ErrDatabaseConfig)
	}

	db := sql.OpenDB(stdlib.GetConnector(*config))
	db.SetMaxOpenConns(1)
	return db, nil
}
