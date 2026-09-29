//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"emsim/internal/platform/audit"
	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuditRecordPersistsOnCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool := migratedTestPool(t, ctx)

	actorID := uuid.New()
	resourceID := uuid.New()
	entry := audit.Entry{
		ActorID:      &actorID,
		ActorRole:    "instructor",
		Action:       "auth.login",
		ResourceType: "user",
		ResourceID:   &resourceID,
		Outcome:      audit.OutcomeOK,
		RequestID:    "req-audit-1",
		Details:      map[string]any{"reason": "credentials_ok"},
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := audit.Record(ctx, tx, entry); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	var (
		gotActorID      uuid.UUID
		gotActorRole    string
		gotAction       string
		gotResourceType string
		gotResourceID   uuid.UUID
		gotOutcome      string
		gotRequestID    string
		gotDetails      map[string]any
	)
	err = pool.QueryRow(ctx, `
		SELECT actor_id, actor_role, action, resource_type, resource_id, outcome, request_id, details
		FROM audit_log WHERE action = 'auth.login' AND request_id = 'req-audit-1'
	`).Scan(&gotActorID, &gotActorRole, &gotAction, &gotResourceType, &gotResourceID, &gotOutcome, &gotRequestID, &gotDetails)
	if err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if gotActorID != actorID || gotActorRole != "instructor" || gotAction != "auth.login" ||
		gotResourceType != "user" || gotResourceID != resourceID || gotOutcome != "ok" || gotRequestID != "req-audit-1" {
		t.Fatalf("audit row = %+v, want it to match the recorded entry", struct {
			ActorID, ActorRole, Action, ResourceType, ResourceID, Outcome, RequestID any
		}{gotActorID, gotActorRole, gotAction, gotResourceType, gotResourceID, gotOutcome, gotRequestID})
	}
	if gotDetails["reason"] != "credentials_ok" {
		t.Fatalf("details = %v, want reason=credentials_ok", gotDetails)
	}
}

func TestAuditRecordRolledBackLeavesNoRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool := migratedTestPool(t, ctx)

	entry := audit.Entry{
		Action:       "auth.login",
		ResourceType: "user",
		Outcome:      audit.OutcomeRejected,
		RequestID:    "req-audit-rollback",
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := audit.Record(ctx, tx, entry); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback tx: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE request_id = 'req-audit-rollback'`).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("audit rows after rollback = %d, want 0", count)
	}
}

func TestAuditRecordSystemActorHasNoActorID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool := migratedTestPool(t, ctx)

	entry := audit.Entry{
		Action:       "auth.bootstrap_admin",
		ResourceType: "user",
		Outcome:      audit.OutcomeOK,
		RequestID:    "req-audit-system",
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := audit.Record(ctx, tx, entry); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	var actorID *uuid.UUID
	var actorRole *string
	if err := pool.QueryRow(ctx, `
		SELECT actor_id, actor_role FROM audit_log WHERE request_id = 'req-audit-system'
	`).Scan(&actorID, &actorRole); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if actorID != nil {
		t.Fatalf("actor_id = %v, want NULL for a system action", *actorID)
	}
	if actorRole != nil {
		t.Fatalf("actor_role = %v, want NULL for a system action", *actorRole)
	}
}

func TestAuditLogOutcomeConstraintRejectsUnknownValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool := migratedTestPool(t, ctx)

	_, err := pool.Exec(ctx, `
		INSERT INTO audit_log (action, resource_type, outcome)
		VALUES ('auth.login', 'user', 'success')
	`)
	if err == nil {
		t.Fatal("insert with an unknown outcome unexpectedly succeeded")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("insert error = %v, want a CHECK violation (23514)", err)
	}
}

// migratedTestPool opens a fresh, fully migrated test database and returns
// a connected pool. It is the shared starting point for tests that don't
// need to observe migration state themselves (unlike TestPlatformSchema).
func migratedTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	return openTestPool(t, ctx, databaseURL)
}
