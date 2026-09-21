// New test: platform/tasks' Finalizer extension point (slice 6, ADR-019)
// against real PostgreSQL — a task kind with a registered Finalizer is
// excluded from the generic dead_letter/failed write, in both
// Recovery.ResolveFailure's exhaustion branch and Recovery.ReapExpired's
// lease-expiry branch, and instead resolved through the Finalizer's own
// domain-first transaction. A kind still short on retry budget (either
// path) is untouched — the Finalizer only ever sees genuine exhaustion.
// This exercises the platform primitive generically (a fake Finalizer
// with no real domain effect); internal/assessment's own Finalizer and
// its atomic auto-write are covered by that package's own integration
// tests (a later commit).
//
//go:build integration

package integration_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"emsim/internal/platform/tasks"

	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const finalizerKind tasks.Kind = "test.finalized"

func TestFinalizer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)

	policy := tasks.DefaultPolicy()
	registry, err := tasks.NewRegistry(policy)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	spec := tasks.Spec{Name: finalizerKind, Pool: "finalizer", MaxAttempts: 3, Lease: 90 * time.Second, RetryBase: 5 * time.Second, Priority: 10}
	if err := registry.Register(spec); err != nil {
		t.Fatalf("register finalizer kind: %v", err)
	}
	store := tasks.NewStore(pool, registry)

	newRecovery := func(t *testing.T) (*tasks.Recovery, *recordingFinalizer) {
		t.Helper()
		recoverer, err := tasks.NewRecovery(pool, policy, tasks.NoJitter{}, registry)
		if err != nil {
			t.Fatalf("new recovery: %v", err)
		}
		finalizer := &recordingFinalizer{pool: pool}
		if err := recoverer.RegisterFinalizer(finalizerKind, finalizer); err != nil {
			t.Fatalf("register finalizer: %v", err)
		}
		return recoverer, finalizer
	}

	t.Run("permanent failure delegates to the finalizer", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		recoverer, finalizer := newRecovery(t)
		now := databaseTime(t, ctx, pool)
		insertQueueTask(t, ctx, pool, finalizerKind, 10, now, now)
		lease := mustClaimKind(t, ctx, store, finalizerKind, "permanent-worker", now)
		failure := handlerFailure(t, tasks.Permanent, "invalid_context")
		resolution, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: now.Add(time.Second), Failure: failure,
		})
		if err != nil || resolution != tasks.ResolutionFailed {
			t.Fatalf("resolution/error = %q/%v", resolution, err)
		}
		if got := finalizer.calls(); len(got) != 1 {
			t.Fatalf("finalizer calls = %d, want 1", len(got))
		} else if got[0].status != tasks.TaskFailed || got[0].code != "invalid_context" || got[0].worker != "permanent-worker" || got[0].token != lease.Token {
			t.Fatalf("finalizer call = %+v", got[0])
		}
		assertFinalizedTask(t, ctx, pool, lease.TaskID, "failed", "invalid_context")
	})

	t.Run("retryable failure with budget left never reaches the finalizer", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		recoverer, finalizer := newRecovery(t)
		now := databaseTime(t, ctx, pool)
		insertQueueTask(t, ctx, pool, finalizerKind, 10, now, now)
		lease := mustClaimKind(t, ctx, store, finalizerKind, "retry-worker", now)
		failure := handlerFailure(t, tasks.Retryable, "temporarily_unavailable")
		resolution, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: now.Add(time.Second), Failure: failure, NextAttemptAt: now.Add(spec.RetryBase),
		})
		if err != nil || resolution != tasks.ResolutionRequeued {
			t.Fatalf("resolution/error = %q/%v", resolution, err)
		}
		if got := finalizer.calls(); len(got) != 0 {
			t.Fatalf("finalizer calls = %d, want 0", len(got))
		}
		var status string
		if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", lease.TaskID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "pending" {
			t.Fatalf("task status = %q, want pending (generic retry path)", status)
		}
	})

	t.Run("exhausted retryable failure delegates to the finalizer", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		recoverer, finalizer := newRecovery(t)
		now := databaseTime(t, ctx, pool)
		taskID := insertQueueTask(t, ctx, pool, finalizerKind, 10, now, now)
		if _, err := pool.Exec(ctx, "UPDATE tasks SET attempts = 2, lease_token = 2 WHERE id = $1", taskID); err != nil {
			t.Fatalf("prepare exhausted task: %v", err)
		}
		lease := mustClaimKind(t, ctx, store, finalizerKind, "exhaust-worker", now)
		failure := handlerFailure(t, tasks.Retryable, "temporarily_unavailable")
		resolution, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: now.Add(time.Second), Failure: failure, NextAttemptAt: now.Add(spec.RetryBase),
		})
		if err != nil || resolution != tasks.ResolutionDeadLetter {
			t.Fatalf("resolution/error = %q/%v", resolution, err)
		}
		if got := finalizer.calls(); len(got) != 1 || got[0].status != tasks.TaskDeadLetter {
			t.Fatalf("finalizer calls = %+v", got)
		}
		assertFinalizedTask(t, ctx, pool, lease.TaskID, "dead_letter", "temporarily_unavailable")
	})

	t.Run("reaped exhausted lease delegates to the finalizer", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		recoverer, finalizer := newRecovery(t)
		lease := insertExpiredLease(t, ctx, pool, finalizerKind, 3, "reap-exhaust-owner")
		summary, err := recoverer.ReapExpired(ctx)
		if err != nil || summary.DeadLetter != 1 || summary.Requeued != 0 {
			t.Fatalf("reap summary/error = %+v/%v", summary, err)
		}
		if got := finalizer.calls(); len(got) != 1 || got[0].status != tasks.TaskDeadLetter || got[0].code != "lease_expired" {
			t.Fatalf("finalizer calls = %+v", got)
		}
		assertFinalizedTask(t, ctx, pool, lease.TaskID, "dead_letter", "lease_expired")
	})

	t.Run("reaped lease with budget left is requeued, never delegated", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		recoverer, finalizer := newRecovery(t)
		insertExpiredLease(t, ctx, pool, finalizerKind, 1, "reap-retry-owner")
		summary, err := recoverer.ReapExpired(ctx)
		if err != nil || summary.Requeued != 1 || summary.DeadLetter != 0 {
			t.Fatalf("reap summary/error = %+v/%v", summary, err)
		}
		if got := finalizer.calls(); len(got) != 0 {
			t.Fatalf("finalizer calls = %d, want 0", len(got))
		}
	})

	t.Run("a stale lease is reported as ErrLeaseLost, not written", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		taskID := insertQueueTask(t, ctx, pool, finalizerKind, 10, now, now)
		finalizer := &recordingFinalizer{pool: pool}
		if err := finalizer.FinalizeExpired(ctx, taskID, "nobody", 999, tasks.TaskDeadLetter, "lease_expired"); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("stale finalize error = %v, want ErrLeaseLost", err)
		}
		var status string
		if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", taskID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "pending" {
			t.Fatalf("task status after stale finalize = %q, want unchanged pending", status)
		}
	})

	t.Run("registering the same kind twice is rejected", func(t *testing.T) {
		recoverer, err := tasks.NewRecovery(pool, policy, tasks.NoJitter{}, registry)
		if err != nil {
			t.Fatal(err)
		}
		f := &recordingFinalizer{pool: pool}
		if err := recoverer.RegisterFinalizer(finalizerKind, f); err != nil {
			t.Fatalf("first register: %v", err)
		}
		if err := recoverer.RegisterFinalizer(finalizerKind, f); !errors.Is(err, tasks.ErrDuplicateKind) {
			t.Fatalf("second register error = %v, want ErrDuplicateKind", err)
		}
	})
}

// recordingFinalizer is a Finalizer fixture: it re-verifies the lease
// exactly the way a real, domain-owning Finalizer must (its own fresh
// transaction, re-checking worker/token under a fresh row lock), then
// writes a result marker no generic terminal write ever sets — so a test
// asserting on that marker proves the write came from here, not from
// pgstore.go's own dead_letter/failed path.
type recordingFinalizer struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	seen []finalizeCall
}

type finalizeCall struct {
	taskID uuid.UUID
	worker string
	token  uint64
	status tasks.TaskStatus
	code   tasks.ErrorCode
}

func (f *recordingFinalizer) FinalizeExpired(ctx context.Context, taskID uuid.UUID, workerID string, token uint64, terminalStatus tasks.TaskStatus, code tasks.ErrorCode) error {
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var actualWorker *string
	var actualToken int64
	err = tx.QueryRow(ctx, `SELECT status, leased_worker, lease_token FROM tasks WHERE id = $1 FOR UPDATE`, taskID).
		Scan(&status, &actualWorker, &actualToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if status != "leased" || actualWorker == nil || *actualWorker != workerID || actualToken != int64(token) {
		return tasks.ErrLeaseLost
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status = $2, leased_worker = NULL, lease_started_at = NULL, lease_expires_at = NULL,
			terminal_worker = $3, next_attempt_at = NULL, last_error_code = $4,
			terminal_at = $5, updated_at = $5, result = $6
		WHERE id = $1
	`, taskID, string(terminalStatus), workerID, string(code), now, []byte(`{"finalized":true}`)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	f.seen = append(f.seen, finalizeCall{taskID: taskID, worker: workerID, token: token, status: terminalStatus, code: code})
	f.mu.Unlock()
	return nil
}

func (f *recordingFinalizer) calls() []finalizeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]finalizeCall(nil), f.seen...)
}

// assertFinalizedTask is assertTerminalTask plus the finalizer's own
// result marker — proof the write took the Finalizer path, not the
// generic one (which never sets result).
func assertFinalizedTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, status, code string) {
	t.Helper()
	assertTerminalTask(t, ctx, pool, taskID, status, code)
	var result []byte
	if err := pool.QueryRow(ctx, "SELECT result FROM tasks WHERE id = $1", taskID).Scan(&result); err != nil {
		t.Fatalf("read finalized result: %v", err)
	}
	if !strings.Contains(string(result), `"finalized"`) {
		t.Fatalf("finalized result = %s, want the finalizer's own marker", result)
	}
}
