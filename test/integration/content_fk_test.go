// New test: the users_service_code_fkey NOT VALID -> validated
// transition migrations/00004_content_catalog.sql's header comment
// describes and cmd/emsim/import.go's "import services" step
// orchestrates (internal/platform/postgres.OrphanServiceCodes/
// ValidateUsersServiceCodeFK).
//
//go:build integration

package integration_test

import (
	"context"
	"testing"

	authpg "emsim/internal/auth/postgres"
	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

func TestValidateUsersServiceCodeFK(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// Simulate an installation that already has a trainee with a
	// service_code before migration 00004 (and its NOT VALID FK) ever
	// existed: migrate only to 00003 first, insert the user (schema v3
	// has no FK to violate), then migrate the rest of the way — NOT
	// VALID means 00004 must not scan/reject this pre-existing row.
	if err := pgstore.UpTo(ctx, databaseURL, 3); err != nil {
		t.Fatalf("migrate up to 3: %v", err)
	}
	authStore := authpg.NewStore(pool)
	if err := authStore.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := authStore.InsertUser(ctx, tx, newTrainee("orphaned", "ghost_service"))
		return err
	}); err != nil {
		t.Fatalf("insert trainee with orphan service_code (pre-migration 00004): %v", err)
	}
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up (rest, including 00004): %v", err)
	}

	orphans, err := pgstore.OrphanServiceCodes(ctx, pool)
	if err != nil {
		t.Fatalf("OrphanServiceCodes: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "ghost_service" {
		t.Fatalf("OrphanServiceCodes = %v, want [ghost_service]", orphans)
	}

	// VALIDATE CONSTRAINT while an orphan exists must fail — this is
	// PostgreSQL's own guarantee, not something the orchestration adds;
	// the test exists so the "check orphans first" ordering in
	// cmd/emsim/import.go is provably necessary, not just tidy.
	if err := pgstore.ValidateUsersServiceCodeFK(ctx, pool); err == nil {
		t.Fatalf("ValidateUsersServiceCodeFK should fail while an orphan service_code exists")
	}

	if _, err := pool.Exec(ctx, `INSERT INTO services (code, name, workflow) VALUES ('ghost_service', 'Ghost', '{}'::jsonb)`); err != nil {
		t.Fatalf("insert missing service: %v", err)
	}

	orphansAfter, err := pgstore.OrphanServiceCodes(ctx, pool)
	if err != nil {
		t.Fatalf("OrphanServiceCodes (after fix): %v", err)
	}
	if len(orphansAfter) != 0 {
		t.Fatalf("OrphanServiceCodes (after fix) = %v, want none", orphansAfter)
	}
	if err := pgstore.ValidateUsersServiceCodeFK(ctx, pool); err != nil {
		t.Fatalf("ValidateUsersServiceCodeFK (after fix): %v", err)
	}

	var validated bool
	if err := pool.QueryRow(ctx, `SELECT convalidated FROM pg_constraint WHERE conname = 'users_service_code_fkey'`).Scan(&validated); err != nil {
		t.Fatalf("read pg_constraint: %v", err)
	}
	if !validated {
		t.Fatalf("users_service_code_fkey.convalidated = false after ValidateUsersServiceCodeFK")
	}

	// idempotent: calling it again (constraint already valid) is a no-op success
	if err := pgstore.ValidateUsersServiceCodeFK(ctx, pool); err != nil {
		t.Fatalf("ValidateUsersServiceCodeFK (idempotent re-run): %v", err)
	}
}
