//go:build integration

package integration_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"emsim/internal/platform/audit"
	pgstore "emsim/internal/platform/postgres"
)

// TestAuditPruneDeletesOnlyOldRowsInBatches (ADR-033): the retention
// boundary is PostgreSQL's own clock, rows at or after it survive, and a
// batch smaller than the backlog still removes all of it.
func TestAuditPruneDeletesOnlyOldRowsInBatches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_log (at, action, resource_type, outcome)
		SELECT now() - make_interval(days => d), 'test.old', 'user', 'ok' FROM generate_series(200, 204) AS d;
		INSERT INTO audit_log (at, action, resource_type, outcome)
		VALUES (now() - make_interval(days => 10), 'test.recent', 'user', 'ok');
	`); err != nil {
		t.Fatal(err)
	}
	deleted, err := audit.Prune(ctx, pool, 183, 2)
	if err != nil || deleted != 5 {
		t.Fatalf("Prune = %d, %v; want 5", deleted, err)
	}
	var old, recent int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action='test.old'), count(*) FILTER (WHERE action='test.recent') FROM audit_log`).Scan(&old, &recent); err != nil {
		t.Fatal(err)
	}
	if old != 0 || recent != 1 {
		t.Fatalf("old=%d recent=%d after prune", old, recent)
	}
	if again, err := audit.Prune(ctx, pool, 183, 2); err != nil || again != 0 {
		t.Fatalf("repeat Prune = %d, %v", again, err)
	}
}

// TestAuditPruneScheduledThroughWorkerProcess (ADR-033): a real worker
// process in the "all" role has its maintenance scheduler enqueue the
// day's audit.prune once — also across a restart — and its short pool
// run it: old rows go, the prune leaves its own system audit row.
func TestAuditPruneScheduledThroughWorkerProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_log (at, action, resource_type, outcome)
		VALUES (now() - interval '400 days', 'test.old', 'user', 'ok'),
		       (now() - interval '1 day', 'test.recent', 'user', 'ok')`); err != nil {
		t.Fatal(err)
	}
	// Slot at midnight UTC: it has always passed, so the scheduler's
	// first tick at startup enqueues today's prune.
	t.Setenv("SCHEDULE_TZ", "UTC")
	t.Setenv("AUDIT_PRUNE_AT", "00:00")
	t.Setenv("AUDIT_RETENTION_DAYS", "183")

	waitDone := func() {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			var done bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT FROM tasks WHERE kind='audit.prune' AND status='done')`).Scan(&done); err != nil {
				t.Fatal(err)
			}
			if done {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("audit.prune did not finish")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	first := startWorkerProcess(t, binary, databaseURL, "all", "prune-first")
	waitDone()
	first.stop(t, false)
	second := startWorkerProcess(t, binary, databaseURL, "all", "prune-second")
	time.Sleep(500 * time.Millisecond)
	second.stop(t, false)

	var tasksCount int
	var dedupKey string
	if err := pool.QueryRow(ctx, `SELECT count(*), min(dedup_key) FROM tasks WHERE kind='audit.prune'`).Scan(&tasksCount, &dedupKey); err != nil {
		t.Fatal(err)
	}
	if want := "audit.prune:daily:" + time.Now().UTC().Format("2006-01-02"); tasksCount != 1 || dedupKey != want {
		t.Fatalf("audit.prune tasks = %d (%s), want one %s", tasksCount, dedupKey, want)
	}
	var old, recent int
	var deleted string
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE action='test.old'),
		count(*) FILTER (WHERE action='test.recent'),
		coalesce(max(details->>'deleted') FILTER (WHERE action='audit.prune' AND actor_role='system' AND actor_id IS NULL), '')
		FROM audit_log`).Scan(&old, &recent, &deleted); err != nil {
		t.Fatal(err)
	}
	if old != 0 || recent != 1 || deleted != "1" {
		t.Fatalf("old=%d recent=%d prune record deleted=%q", old, recent, deleted)
	}
}
