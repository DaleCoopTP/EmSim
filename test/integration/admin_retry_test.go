//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"emsim/internal/assessment"
	assessmentpg "emsim/internal/assessment/postgres"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"
	trainingpg "emsim/internal/training/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func retryTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *tasks.Store, id uuid.UUID, guards map[tasks.Kind]tasks.RetryGuard) (tasks.RetryTarget, error) {
	t.Helper()
	var target tasks.RetryTarget
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, target, err = store.RetryTx(ctx, tx, id, guards)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return target, nil
}

// TestAdminRetryKeepsTaskIdentity (ADR-033): a dead_letter task goes back
// to pending under the same id and dedup key with one more attempt in
// its budget, and its next claim continues the lease-token sequence.
// done/cancelled tasks, unknown ids and kinds guarded by NeverRetry are
// refused.
func TestAdminRetryKeepsTaskIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tasks.Spec{Name: "system.noop", Pool: "short", MaxAttempts: 1, Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	store := tasks.NewStore(pool, registry)
	insert := func(kind, status string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		var query string
		switch status {
		case "dead_letter":
			query = `INSERT INTO tasks (id, kind, scope_type, dedup_key, status, attempts, max_attempts, lease_token, terminal_worker, last_error_code, terminal_at)
				VALUES ($1, $2, 'system', $3, 'dead_letter', 1, 1, 1, 'w', 'boom', clock_timestamp())`
		case "done":
			query = `INSERT INTO tasks (id, kind, scope_type, dedup_key, status, attempts, max_attempts, lease_token, terminal_worker, terminal_at)
				VALUES ($1, $2, 'system', $3, 'done', 1, 1, 1, 'w', clock_timestamp())`
		case "cancelled":
			query = `INSERT INTO tasks (id, kind, scope_type, dedup_key, status, attempts, max_attempts, terminal_at)
				VALUES ($1, $2, 'system', $3, 'cancelled', 0, 1, clock_timestamp())`
		}
		if _, err := pool.Exec(ctx, query, id, kind, "retry-test:"+id.String()); err != nil {
			t.Fatal(err)
		}
		return id
	}
	guards := map[tasks.Kind]tasks.RetryGuard{training.KindCallerReply: tasks.NeverRetry{}}

	dead := insert("system.noop", "dead_letter")
	if target, err := retryTask(t, ctx, pool, store, dead, guards); err != nil || target != tasks.RetryPending {
		t.Fatalf("retry dead_letter = %q, %v", target, err)
	}
	var status, dedup string
	var attempts, maxAttempts, token int
	if err := pool.QueryRow(ctx, `SELECT status, dedup_key, attempts, max_attempts, lease_token FROM tasks WHERE id = $1`, dead).
		Scan(&status, &dedup, &attempts, &maxAttempts, &token); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || dedup != "retry-test:"+dead.String() || attempts != 1 || maxAttempts != 2 || token != 1 {
		t.Fatalf("after retry: status=%s dedup=%s attempts=%d max=%d token=%d", status, dedup, attempts, maxAttempts, token)
	}
	lease, claimed, err := store.Claim(ctx, tasks.ClaimRequest{Kinds: []tasks.Kind{"system.noop"}, WorkerID: "retry-worker", Now: databaseTime(t, ctx, pool)})
	if err != nil || !claimed || lease.TaskID != dead || lease.Token != 2 {
		t.Fatalf("claim after retry = %+v, %v, %v", lease, claimed, err)
	}

	for _, id := range []uuid.UUID{insert("system.noop", "done"), insert("system.noop", "cancelled"), insert(string(training.KindCallerReply), "dead_letter")} {
		if _, err := retryTask(t, ctx, pool, store, id, guards); !errors.Is(err, tasks.ErrNotRetryable) {
			t.Fatalf("retry %s = %v, want ErrNotRetryable", id, err)
		}
	}
	if _, err := retryTask(t, ctx, pool, store, uuid.New(), guards); !errors.Is(err, tasks.ErrNotFound) {
		t.Fatalf("retry unknown = %v, want ErrNotFound", err)
	}
}

// TestAdminRetryAssessmentGuard (ADR-033, RFC-001 §7.4): an evaluate task
// that failed before its input was sealed goes back to waiting and then
// through the normal coordinator to a single auto; once the item has an
// auto, no retry of its evaluate task is allowed.
func TestAdminRetryAssessmentGuard(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	taskStore := mustTaskEnqueuer(pool)
	trainingService := newTrainingService(pool)
	// No evaluator registered: input preparation fails (as in
	// TestAssessmentInputPreparationFailureAllowsManualAssessment).
	broken := assessment.NewService(
		assessmentpg.NewStore(pool), trainingpg.NewStore(pool), trainingpg.NewStore(pool), trainingpg.NewStore(pool), contentpg.NewStore(pool), taskStore,
		assessment.Registry{}, nil,
	)
	itemID := closePilotItemForAssessment(t, ctx, pool, trainingService)
	mustRunCoordinatorTick(t, ctx, pool, broken)
	taskID := evaluateTaskID(t, ctx, pool, itemID)
	if status, _ := evaluateTaskStatus(t, ctx, pool, itemID); status != "failed" {
		t.Fatalf("evaluate after preparation failure = %s", status)
	}

	fixed := newAssessmentServiceForTest(pool, taskStore)
	guards := map[tasks.Kind]tasks.RetryGuard{training.KindAssessmentEvaluate: fixed}
	if target, err := retryTask(t, ctx, pool, taskStore, taskID, guards); err != nil || target != tasks.RetryWaiting {
		t.Fatalf("retry before input = %q, %v", target, err)
	}
	mustRunCoordinatorTick(t, ctx, pool, fixed)
	if handleErr, ok := claimAndHandle(t, ctx, pool, taskStore, fixed, "retry-worker"); !ok || handleErr != nil {
		t.Fatalf("claim after retry: ok=%v err=%v", ok, handleErr)
	}
	rows := readAssessments(t, ctx, pool, itemID)
	if len(rows) != 1 || rows[0].Kind != "auto" {
		t.Fatalf("assessments after retried evaluate = %+v", rows)
	}

	// Force the task back into dead_letter: with an auto on record the
	// guard still refuses a second evaluation.
	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'dead_letter', last_error_code = 'boom', max_attempts = attempts WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := retryTask(t, ctx, pool, taskStore, taskID, guards); !errors.Is(err, tasks.ErrNotRetryable) {
		t.Fatalf("retry with auto = %v, want ErrNotRetryable", err)
	}
}

func evaluateTaskID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("no evaluate task")
		}
		t.Fatal(err)
	}
	return id
}
