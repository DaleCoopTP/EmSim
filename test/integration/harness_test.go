// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// test/integration/schema_test.go; adapted: the domain tables
// (runs/run_items/dialogues/evaluations/run_results) were not ported, so
// this only exercises the platform tables migrated so far ("tasks", and
// from migration 00002 "audit_log") — CHECK constraints, migrate up/down/
// up, and readiness. down/up now migrates through every applied migration
// (downToZero), not a single goose step, since goose's own Down only
// reverts the latest version. The queue lifecycle tests (claim/heartbeat/
// terminal/recovery) live in task_queue_test.go/task_recovery_test.go
// once internal/platform/tasks exists; audit_test.go covers audit_log's
// own writer. Adapted: added a TEST_DATABASE_URL escape hatch alongside
// testcontainers, since testcontainers needs Docker on every developer
// machine (docs/technical-discovery.md §6).
//
//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPlatformSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	databaseURL := openTestDatabase(t, ctx)

	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer pool.Close()
	if err := pgstore.Ping(ctx, pool); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}

	assertPostgreSQL16(t, ctx, pool)
	assertVersionAndReadiness(t, ctx, pool, 0, false)

	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	assertVersionAndReadiness(t, ctx, pool, pgstore.ExpectedSchemaVersion, true)
	assertTableSet(t, ctx, pool)
	assertTaskConstraints(t, ctx, pool)

	downToZero(t, ctx, pool, databaseURL)
	assertVersionAndReadiness(t, ctx, pool, 0, false)
	assertApplicationTablesAbsent(t, ctx, pool)

	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up after down: %v", err)
	}
	assertVersionAndReadiness(t, ctx, pool, pgstore.ExpectedSchemaVersion, true)
	assertTableSet(t, ctx, pool)
}

// downToZero migrates databaseURL down one goose version at a time until
// none remain applied. goose's own Down (pgstore.Down) reverts only the
// latest applied version, like "goose down" — it is not "goose down-to 0".
func downToZero(t *testing.T, ctx context.Context, pool *pgxpool.Pool, databaseURL string) {
	t.Helper()
	for {
		version, err := pgstore.CurrentVersion(ctx, pool)
		if err != nil {
			t.Fatalf("current migration version: %v", err)
		}
		if version == 0 {
			return
		}
		if err := pgstore.Down(ctx, databaseURL); err != nil {
			t.Fatalf("migrate down from version %d: %v", version, err)
		}
	}
}

// openTestDatabase returns a connection string for an empty PostgreSQL 16
// database. With TEST_DATABASE_URL set it resets and reuses that database
// (a locally running PostgreSQL, no Docker required); otherwise it starts a
// disposable postgres:16-alpine container via testcontainers.
func openTestDatabase(t *testing.T, ctx context.Context) string {
	t.Helper()

	if databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); databaseURL != "" {
		resetSchema(t, ctx, databaseURL)
		t.Cleanup(func() { resetSchema(t, context.Background(), databaseURL) })
		return databaseURL
	}

	if os.Getenv("CI") == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}

	container, err := tcpostgres.Run(
		ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("emsim"),
		tcpostgres.WithUsername("emsim"),
		tcpostgres.WithPassword("integration-only"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL 16: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := container.Terminate(cleanupCtx); err != nil {
			t.Errorf("terminate PostgreSQL 16: %v", err)
		}
	})

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("build PostgreSQL connection string: %v", err)
	}
	return databaseURL
}

func resetSchema(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL for reset: %v", err)
	}
	defer pool.Close()
	// Drop dependants before their referenced tables. Trigger functions from
	// the content/training migrations outlive DROP TABLE and must also be
	// removed or a subsequent migration fails with "function already exists".
	if _, err := pool.Exec(ctx, `
		DROP VIEW IF EXISTS lesson_report_rows;
		DROP VIEW IF EXISTS item_final_assessment;
		DROP TABLE IF EXISTS report_files;
		DROP FUNCTION IF EXISTS guard_report_file_update();
		DROP TABLE IF EXISTS training_examples;
		DROP TABLE IF EXISTS assessments;
		DROP FUNCTION IF EXISTS guard_assessment_revision();
		DROP TABLE IF EXISTS assessment_inputs;
		DROP TABLE IF EXISTS trainee_assessment_state;
		DROP TABLE IF EXISTS control_reports;
		DROP TABLE IF EXISTS item_events;
		DROP TABLE IF EXISTS evidence;
		DROP TABLE IF EXISTS intake_dispatches;
		DROP TABLE IF EXISTS intake_notifications;
		DROP TABLE IF EXISTS actions;
		DROP TABLE IF EXISTS calls;
		DROP TABLE IF EXISTS items;
		DROP TABLE IF EXISTS runs;
		DROP TABLE IF EXISTS assignments;
		DROP TABLE IF EXISTS lessons;
		DROP TABLE IF EXISTS intake_catalog_versions;
		DROP FUNCTION IF EXISTS reject_immutable_change();
		DROP TABLE IF EXISTS voice_assets;
		DROP TABLE IF EXISTS scenario_versions;
		DROP FUNCTION IF EXISTS reject_scenario_version_delete();
		DROP FUNCTION IF EXISTS protect_scenario_version_content();
		DROP TABLE IF EXISTS scenarios;
		DROP TABLE IF EXISTS tickets;
		DROP TABLE IF EXISTS classifier_types;
		DROP TABLE IF EXISTS sessions;
		DROP TABLE IF EXISTS audit_log;
		DROP TABLE IF EXISTS tasks;
		DROP TABLE IF EXISTS users;
		DROP TABLE IF EXISTS workstations;
		DROP TABLE IF EXISTS services;
		DROP TABLE IF EXISTS blobs;
		DROP TABLE IF EXISTS goose_db_version;
	`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
}

func TestResetSchemaAfterContentMigrations(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("first migrate up: %v", err)
	}

	resetSchema(t, ctx, databaseURL)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up after resetSchema: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	assertVersionAndReadiness(t, ctx, pool, pgstore.ExpectedSchemaVersion, true)
}

// openTestPool opens and pings a pool against an already-migrated test
// database. Other _test.go files in this package use it after their own
// openTestDatabase + pgstore.Up, rather than repeating the open/ping
// boilerplate TestPlatformSchema above needs inline (it asserts
// readiness before the schema exists, so it cannot use this helper for its
// first pool).
func openTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.Ping(ctx, pool); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	return pool
}

func assertPostgreSQL16(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var versionNumber int
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&versionNumber); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	if versionNumber < 160000 || versionNumber >= 170000 {
		t.Fatalf("PostgreSQL version number = %d, want major 16", versionNumber)
	}
}

func assertVersionAndReadiness(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wantVersion int64, wantReady bool) {
	t.Helper()
	version, err := pgstore.CurrentVersion(ctx, pool)
	if err != nil {
		t.Fatalf("current migration version: %v", err)
	}
	if version != wantVersion {
		t.Fatalf("migration version = %d, want %d", version, wantVersion)
	}
	ready, err := pgstore.Ready(ctx, pool)
	if err != nil {
		t.Fatalf("schema readiness: %v", err)
	}
	if ready != wantReady {
		t.Fatalf("schema readiness = %t, want %t", ready, wantReady)
	}
}

func assertTableSet(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT tablename
		FROM pg_catalog.pg_tables
		WHERE schemaname = 'public' AND tablename <> 'goose_db_version'
		ORDER BY tablename
	`)
	if err != nil {
		t.Fatalf("list application tables: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan application table: %v", err)
		}
		got = append(got, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate application tables: %v", err)
	}
	want := []string{
		"actions", "assessment_inputs", "assessments", "assignments", "audit_log", "blobs", "calls",
		"classifier_types", "control_reports", "evidence",
		"intake_catalog_versions", "intake_dispatches", "intake_notifications", "item_events",
		"items", "lessons", "report_files", "runs", "scenario_versions", "scenarios",
		"services", "sessions", "tasks", "tickets", "trainee_assessment_state", "training_examples", "users",
		"voice_assets", "workstations",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("application tables = %v, want %v", got, want)
	}
}

func assertApplicationTablesAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_catalog.pg_tables
		WHERE schemaname = 'public' AND tablename IN (
			'actions', 'assessment_inputs', 'assessments', 'assignments', 'audit_log', 'blobs', 'calls',
			'classifier_types', 'control_reports', 'evidence', 'intake_catalog_versions',
			'intake_dispatches', 'intake_notifications', 'item_events',
			'items', 'lessons', 'report_files', 'runs', 'scenario_versions', 'scenarios',
			'services', 'sessions', 'tasks', 'tickets', 'trainee_assessment_state', 'training_examples', 'users',
			'voice_assets', 'workstations'
		)
	`).Scan(&count); err != nil {
		t.Fatalf("count application tables: %v", err)
	}
	if count != 0 {
		t.Fatalf("application tables after down = %d, want 0", count)
	}
}

// assertTaskConstraints exercises the tasks_* CHECK constraints from
// migrations/00001_platform_tasks.sql: kind shape, payload shape, error
// code shape, attempt budget, lease shape, dedup_key uniqueness, and the
// per-status state-shape rules. There is no FK to a domain table (ADR-010,
// docs/technical-discovery.md §3.1), so every fixture here is a bare task
// row.
func assertTaskConstraints(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	ids := newIDSource()

	task1 := ids.next()
	insertPendingTask(t, ctx, pool, task1, "system.noop", "system", "system.noop:"+task1)

	expectViolation(t, ctx, pool, "UPDATE tasks SET kind = 'finalize' WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET status = 'retrying' WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET priority = -1 WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET payload = '[]'::jsonb WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET last_error_code = 'Invalid Code!' WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET max_attempts = 0 WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET attempts = -1 WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET attempts = max_attempts + 1 WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET attempts = 1 WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET lease_token = 1 WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET lease_token = -1 WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET leased_worker = 'worker' WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET next_attempt_at = NULL WHERE id = $1", task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET status = 'waiting' WHERE id = $1", task1)
	expectViolation(t, ctx, pool, `
		UPDATE tasks SET status = 'leased', attempts = 0, lease_token = 0,
			next_attempt_at = NULL, leased_worker = 'worker',
			lease_started_at = transaction_timestamp(),
			lease_expires_at = transaction_timestamp() + interval '1 minute'
		WHERE id = $1
	`, task1)
	expectViolation(t, ctx, pool, `
		UPDATE tasks SET status = 'done', attempts = 0, lease_token = 0,
			next_attempt_at = NULL, terminal_at = transaction_timestamp()
		WHERE id = $1
	`, task1)
	expectViolation(t, ctx, pool, `
		UPDATE tasks SET status = 'done', attempts = 1, lease_token = 1, next_attempt_at = NULL,
			terminal_worker = 'worker', terminal_at = transaction_timestamp(),
			last_error_code = 'invalid'
		WHERE id = $1
	`, task1)
	expectViolation(t, ctx, pool, `
		UPDATE tasks SET status = 'dead_letter', attempts = 1, lease_token = 1, next_attempt_at = NULL,
			terminal_worker = 'worker', terminal_at = transaction_timestamp(),
			last_error_code = 'exhausted'
		WHERE id = $1
	`, task1)
	expectViolation(t, ctx, pool, "UPDATE tasks SET status = 'cancelled' WHERE id = $1", task1)
	expectViolation(t, ctx, pool, `
		INSERT INTO tasks (id, kind, scope_type, dedup_key, status, max_attempts, next_attempt_at)
		VALUES ($1, 'system.noop', 'system', $2, 'pending', 3, transaction_timestamp())
	`, ids.next(), "system.noop:"+task1)
	expectViolation(t, ctx, pool, `
		INSERT INTO tasks (id, kind, scope_type, dedup_key, status, max_attempts, next_attempt_at)
		VALUES ($1, 'noop', 'system', $2, 'pending', 3, transaction_timestamp())
	`, ids.next(), "bad-kind:"+ids.next())

	assertValidTaskShapes(t, ctx, pool, ids)
}

func assertValidTaskShapes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids *idSource) {
	t.Helper()

	waitingID := ids.next()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (id, kind, scope_type, dedup_key, status, max_attempts, wait_until, wait_reason)
		VALUES ($1, 'assessment.evaluate', 'item', $2, 'waiting', 3, transaction_timestamp() + interval '2 seconds', 'stt_pending')
	`, waitingID, "assessment.evaluate:"+waitingID); err != nil {
		t.Fatalf("insert valid waiting task: %v", err)
	}

	leasedID := ids.next()
	insertPendingTask(t, ctx, pool, leasedID, "voice.render", "call", "voice.render:"+leasedID)
	if _, err := pool.Exec(ctx, `
		UPDATE tasks SET status = 'leased', attempts = 1, lease_token = 1,
			next_attempt_at = NULL, leased_worker = 'worker',
			lease_started_at = transaction_timestamp(),
			lease_expires_at = transaction_timestamp() + interval '2 minutes',
			updated_at = transaction_timestamp()
		WHERE id = $1
	`, leasedID); err != nil {
		t.Fatalf("set valid leased task shape: %v", err)
	}

	doneID := ids.next()
	insertPendingTask(t, ctx, pool, doneID, "report.build", "lesson", "report.build:"+doneID)
	if _, err := pool.Exec(ctx, `
		UPDATE tasks SET status = 'done', attempts = 1, lease_token = 1, next_attempt_at = NULL,
			terminal_worker = 'worker', terminal_at = transaction_timestamp(),
			result = '{"ok":true}'::jsonb, updated_at = transaction_timestamp()
		WHERE id = $1
	`, doneID); err != nil {
		t.Fatalf("set valid done task shape: %v", err)
	}

	failedID := ids.next()
	insertPendingTask(t, ctx, pool, failedID, "scenario.generate", "scenario", "scenario.generate:"+failedID)
	if _, err := pool.Exec(ctx, `
		UPDATE tasks SET status = 'failed', attempts = 1, lease_token = 1, next_attempt_at = NULL,
			terminal_worker = 'worker', terminal_at = transaction_timestamp(),
			last_error_code = 'remote_rejected', updated_at = transaction_timestamp()
		WHERE id = $1
	`, failedID); err != nil {
		t.Fatalf("set valid failed task shape: %v", err)
	}

	deadLetterID := ids.next()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (id, kind, scope_type, dedup_key, status, attempts, max_attempts, lease_token, next_attempt_at)
		VALUES ($1, 'stt.transcribe', 'call', $2, 'pending', 0, 1, 0, transaction_timestamp())
	`, deadLetterID, "stt.transcribe:"+deadLetterID); err != nil {
		t.Fatalf("insert single-attempt task: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE tasks SET status = 'dead_letter', attempts = 1, lease_token = 1, next_attempt_at = NULL,
			terminal_worker = 'worker', terminal_at = transaction_timestamp(),
			last_error_code = 'exhausted', updated_at = transaction_timestamp()
		WHERE id = $1
	`, deadLetterID); err != nil {
		t.Fatalf("set valid dead-letter task shape: %v", err)
	}

	cancelledID := ids.next()
	insertPendingTask(t, ctx, pool, cancelledID, "advice.compute", "lesson", "advice.compute:"+cancelledID)
	if _, err := pool.Exec(ctx, `
		UPDATE tasks SET status = 'cancelled', next_attempt_at = NULL,
			terminal_at = transaction_timestamp(), updated_at = transaction_timestamp()
		WHERE id = $1
	`, cancelledID); err != nil {
		t.Fatalf("set valid cancelled task shape: %v", err)
	}

	want := map[string]string{
		waitingID: "waiting", leasedID: "leased", doneID: "done",
		failedID: "failed", deadLetterID: "dead_letter", cancelledID: "cancelled",
	}
	for id, wantStatus := range want {
		var got string
		if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", id).Scan(&got); err != nil {
			t.Fatalf("read task state: %v", err)
		}
		if got != wantStatus {
			t.Fatalf("task %s status = %q, want %q", id, got, wantStatus)
		}
	}
}

func insertPendingTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, kind, scopeType, dedupKey string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (id, kind, scope_type, dedup_key, status, max_attempts, next_attempt_at)
		VALUES ($1, $2, $3, $4, 'pending', 3, transaction_timestamp())
	`, id, kind, scopeType, dedupKey); err != nil {
		t.Fatalf("insert %s task: %v", kind, err)
	}
}

func expectViolation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	_, err := pool.Exec(ctx, query, args...)
	if err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", strings.TrimSpace(query))
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || !strings.HasPrefix(pgErr.Code, "23") {
		t.Fatalf("statement error = %v, want PostgreSQL integrity violation", err)
	}
}

type idSource struct {
	nextValue int
}

func newIDSource() *idSource {
	return &idSource{nextValue: 1}
}

func (s *idSource) next() string {
	id := fmt.Sprintf("00000000-0000-0000-0000-%012d", s.nextValue)
	s.nextValue++
	return id
}
