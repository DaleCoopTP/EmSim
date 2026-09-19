// This file holds cross-module schema-transition helpers. They live in
// platform, not in auth or content, because they concern a constraint
// between two modules' tables: users.service_code_fkey
// (migrations/00004_content_catalog.sql) was added NOT VALID over an
// installation that may already have users with a service_code no
// services row backs yet — see that migration's header comment.
// cmd/emsim's "import services" subcommand orchestrates the
// NOT VALID -> validated transition using the two functions below.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OrphanServiceCodes returns every distinct users.service_code that has
// no matching services.code row, ordered — the codes
// users_service_code_fkey would reject if VALIDATE CONSTRAINT ran right
// now. An empty result means validation is safe to run.
func OrphanServiceCodes(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT u.service_code FROM users u
		WHERE u.service_code IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM services s WHERE s.code = u.service_code)
		ORDER BY u.service_code
	`)
	if err != nil {
		return nil, fmt.Errorf("query orphan service codes: %w", ErrSchemaInspection)
	}
	defer rows.Close()

	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("scan orphan service code: %w", ErrSchemaInspection)
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read orphan service codes: %w", ErrSchemaInspection)
	}
	return codes, nil
}

// ValidateUsersServiceCodeFK runs ALTER TABLE users VALIDATE CONSTRAINT
// users_service_code_fkey. It is idempotent: a constraint PostgreSQL
// already marked valid is left alone, so a caller (or a compose one-shot
// service) can run "import services" on every startup without repeating
// the table scan VALIDATE CONSTRAINT would otherwise redo each time.
func ValidateUsersServiceCodeFK(ctx context.Context, pool *pgxpool.Pool) error {
	var alreadyValid bool
	err := pool.QueryRow(ctx, `SELECT convalidated FROM pg_constraint WHERE conname = 'users_service_code_fkey'`).Scan(&alreadyValid)
	if err != nil {
		return fmt.Errorf("inspect users_service_code_fkey: %w", ErrSchemaInspection)
	}
	if alreadyValid {
		return nil
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE users VALIDATE CONSTRAINT users_service_code_fkey`); err != nil {
		return fmt.Errorf("validate users_service_code_fkey: %w", ErrMigrationFailed)
	}
	return nil
}
