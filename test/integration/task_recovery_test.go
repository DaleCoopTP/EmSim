// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// test/integration/task_recovery_test.go; adapted: fixtures insert bare
// task rows (no runs/run_items — there is nothing for a failed task to
// cascade into, see pgrecovery.go), so assertTerminalTaskAndItem drops the
// run_items join and the digest column, and there is no
// assertNoDialogueArtifacts equivalent (no dialogues/judge tables). The
// "current retryable failure" and "exhausted retryable failure" cases use
// queueKindA's registered RetryBase instead of policy.DialogueRetryBase.
//
//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"emsim/internal/platform/tasks"

	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)

	policy := tasks.DefaultPolicy()
	registry := newQueueTestRegistry(t)
	spec, ok := registry.Lookup(queueKindA)
	if !ok {
		t.Fatalf("queueKindA not registered")
	}
	recoverer, err := tasks.NewRecovery(pool, policy, tasks.NoJitter{}, registry)
	if err != nil {
		t.Fatalf("create task recovery: %v", err)
	}
	store := tasks.NewStore(pool, registry)
	now := databaseTime(t, ctx, pool)

	t.Run("current retryable failure schedules kind backoff", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		lease := mustClaimKind(t, ctx, store, queueKindA, "retry-worker", now)
		failure := handlerFailure(t, tasks.Retryable, "temporarily_unavailable")
		nextAttempt := now.Add(spec.RetryBase)
		resolution, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: now.Add(time.Second), Failure: failure, NextAttemptAt: nextAttempt,
		})
		if err != nil || resolution != tasks.ResolutionRequeued {
			t.Fatalf("retry resolution/error = %q/%v", resolution, err)
		}
		var scheduled, updated time.Time
		if err := pool.QueryRow(ctx, "SELECT next_attempt_at, updated_at FROM tasks WHERE id=$1", lease.TaskID).Scan(&scheduled, &updated); err != nil {
			t.Fatal(err)
		}
		if scheduled.Sub(updated) != nextAttempt.Sub(now.Add(time.Second)) {
			t.Fatalf("backoff = %v", scheduled.Sub(updated))
		}
		assertTaskState(t, ctx, pool, lease.TaskID, "pending", 1, 1, scheduled)
	})

	t.Run("exhausted retryable failure dead letters task", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		taskID := insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		if _, err := pool.Exec(ctx, "UPDATE tasks SET attempts = 2, lease_token = 2 WHERE id = $1", taskID); err != nil {
			t.Fatalf("prepare exhausted task: %v", err)
		}
		if _, err := pool.Exec(ctx, "UPDATE tasks SET max_attempts = 3 WHERE id = $1", taskID); err != nil {
			t.Fatalf("set max attempts: %v", err)
		}
		lease := mustClaimKind(t, ctx, store, queueKindA, "exhaust-worker", now)
		failure := handlerFailure(t, tasks.Retryable, "temporarily_unavailable")
		resolution, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: now.Add(time.Second), Failure: failure, NextAttemptAt: now.Add(spec.RetryBase),
		})
		if err != nil || resolution != tasks.ResolutionDeadLetter {
			t.Fatalf("exhausted resolution/error = %q/%v", resolution, err)
		}
		assertTerminalTask(t, ctx, pool, lease.TaskID, "dead_letter", "temporarily_unavailable")
	})

	t.Run("permanent failure fails task", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		lease := mustClaimKind(t, ctx, store, queueKindA, "permanent-worker", now)
		failure := handlerFailure(t, tasks.Permanent, "invalid_context")
		resolution, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: now.Add(time.Second), Failure: failure,
		})
		if err != nil || resolution != tasks.ResolutionFailed {
			t.Fatalf("permanent resolution/error = %q/%v", resolution, err)
		}
		assertTerminalTask(t, ctx, pool, lease.TaskID, "failed", "invalid_context")
	})

	t.Run("permanent failure on a second pool also fails its task", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindB, 10, now, now)
		lease := mustClaimKind(t, ctx, store, queueKindB, "pool-b-worker", now)
		failure := handlerFailure(t, tasks.Permanent, "invalid_response")
		resolution, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: now.Add(time.Second), Failure: failure,
		})
		if err != nil || resolution != tasks.ResolutionFailed {
			t.Fatalf("permanent resolution/error = %q/%v", resolution, err)
		}
		assertTerminalTask(t, ctx, pool, lease.TaskID, "failed", "invalid_response")
	})

	t.Run("expired lease requeues and fences stale owner", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		lease := insertExpiredLease(t, ctx, pool, queueKindA, 1, "abandoned-worker")
		summary, err := recoverer.ReapExpired(ctx)
		if err != nil || summary.Requeued != 1 || summary.DeadLetter != 0 {
			t.Fatalf("reap summary/error = %+v/%v", summary, err)
		}
		if _, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{
			Lease: lease, Now: time.Now().UTC(), LeaseDuration: spec.Lease,
		}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("stale heartbeat error = %v", err)
		}
		failure := handlerFailure(t, tasks.Permanent, "abandoned")
		if _, err := recoverer.ResolveFailure(ctx, tasks.FailureRequest{
			Lease: lease, Now: time.Now().UTC(), Failure: failure,
		}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("stale failure error = %v", err)
		}
	})

	t.Run("expired exhausted lease dead letters task", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		lease := insertExpiredLease(t, ctx, pool, queueKindA, 3, "exhausted-owner")
		summary, err := recoverer.ReapExpired(ctx)
		if err != nil || summary.DeadLetter != 1 || summary.Requeued != 0 {
			t.Fatalf("exhausted reap summary/error = %+v/%v", summary, err)
		}
		assertTerminalTask(t, ctx, pool, lease.TaskID, "dead_letter", "lease_expired")
	})

	t.Run("heartbeat extension protects lease from reaper", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		databaseNow := databaseTime(t, ctx, pool)
		lease := insertLeasedTask(t, ctx, pool, queueKindA, 1, "heartbeat-owner", databaseNow.Add(-time.Minute), databaseNow.Add(5*time.Second))
		if _, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{
			Lease: lease, Now: databaseNow.Add(-6 * time.Second), LeaseDuration: spec.Lease,
		}); err != nil {
			t.Fatalf("extend current lease: %v", err)
		}
		summary, err := recoverer.ReapExpired(ctx)
		if err != nil || summary.Count() != 0 {
			t.Fatalf("protected reap summary/error = %+v/%v", summary, err)
		}
	})

	t.Run("expired terminal writer cannot beat reaper", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		databaseNow := databaseTime(t, ctx, pool)
		lease := insertLeasedTask(t, ctx, pool, queueKindA, 1, "race-owner", databaseNow.Add(-2*time.Minute), databaseNow.Add(-30*time.Second))
		outcome, err := tasks.Failed("invalid_response", nil)
		if err != nil {
			t.Fatalf("terminal outcome: %v", err)
		}
		start := make(chan struct{})
		terminalResult := make(chan error, 1)
		reaperResult := make(chan tasks.ReapSummary, 1)
		reaperError := make(chan error, 1)
		go func() {
			<-start
			tx, beginErr := pool.Begin(ctx)
			if beginErr != nil {
				terminalResult <- beginErr
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			_, terminalErr := store.Terminal(ctx, tx, tasks.TerminalRequest{
				Lease: lease, Now: databaseNow.Add(-45 * time.Second), Outcome: outcome,
			})
			if terminalErr == nil {
				terminalErr = tx.Commit(ctx)
			}
			terminalResult <- terminalErr
		}()
		go func() {
			<-start
			summary, reapErr := recoverer.ReapExpired(ctx)
			reaperResult <- summary
			reaperError <- reapErr
		}()
		close(start)
		terminalErr := <-terminalResult
		summary := <-reaperResult
		reapErr := <-reaperError
		terminalWon := terminalErr == nil
		reaperWon := reapErr == nil && summary.Count() == 1
		if terminalWon || !reaperWon {
			t.Fatalf("terminal/reaper winners = %t/%t, errors = %v/%v summary=%+v", terminalWon, reaperWon, terminalErr, reapErr, summary)
		}
		if terminalErr != nil && !errors.Is(terminalErr, tasks.ErrLeaseLost) {
			t.Fatalf("terminal loser error = %v", terminalErr)
		}
	})

	t.Run("cancelled task is not reaped", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		lease := insertExpiredLease(t, ctx, pool, queueKindA, 1, "cancel-race-owner")
		cancelled, err := store.Cancel(ctx, tasks.CancelRequest{TaskID: lease.TaskID, Now: time.Now().UTC()})
		if err != nil || !cancelled {
			t.Fatalf("cancel leased-but-expired task = %t/%v", cancelled, err)
		}
		summary, err := recoverer.ReapExpired(ctx)
		if err != nil || summary.Count() != 0 {
			t.Fatalf("reap after cancel summary/error = %+v/%v, want empty", summary, err)
		}
		var status string
		if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", lease.TaskID).Scan(&status); err != nil {
			t.Fatalf("read cancelled task status: %v", err)
		}
		if status != "cancelled" {
			t.Fatalf("task status after cancel+reap = %q, want cancelled", status)
		}
	})

	t.Run("reaper batch is bounded", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		for index := 0; index < policy.ReaperBatch+1; index++ {
			insertExpiredLease(t, ctx, pool, queueKindA, 1, "batch-owner")
		}
		summary, err := recoverer.ReapExpired(ctx)
		if err != nil || summary.Count() != policy.ReaperBatch {
			t.Fatalf("batch summary/error = %+v/%v", summary, err)
		}
		var leased int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM tasks WHERE status = 'leased'").Scan(&leased); err != nil || leased != 1 {
			t.Fatalf("leased after batch = %d/%v", leased, err)
		}
	})
}

func mustClaimKind(t *testing.T, ctx context.Context, store *tasks.Store, kind tasks.Kind, worker string, now time.Time) tasks.Lease {
	t.Helper()
	lease, found, err := store.Claim(ctx, tasks.ClaimRequest{Kinds: []tasks.Kind{kind}, WorkerID: worker, Now: now})
	if err != nil || !found {
		t.Fatalf("claim %s = %t/%v", kind, found, err)
	}
	return lease
}

func handlerFailure(t *testing.T, retryability tasks.Retryability, code tasks.ErrorCode) *tasks.HandlerFailure {
	t.Helper()
	failure, err := tasks.NewHandlerFailure(retryability, code)
	if err != nil {
		t.Fatalf("handler failure: %v", err)
	}
	return failure
}

func insertExpiredLease(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind tasks.Kind, attempts int, worker string) tasks.Lease {
	t.Helper()
	now := databaseTime(t, ctx, pool)
	return insertLeasedTask(t, ctx, pool, kind, attempts, worker, now.Add(-2*time.Minute), now.Add(-30*time.Second))
}

func insertLeasedTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind tasks.Kind, attempts int, worker string, startedAt, expiresAt time.Time) tasks.Lease {
	t.Helper()
	taskID := insertQueueTask(t, ctx, pool, kind, 10, startedAt, startedAt.Add(-time.Minute))
	if _, err := pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'leased', attempts = $2::integer, max_attempts = greatest(max_attempts, $2::integer),
			lease_token = $2::bigint, leased_worker = $3, lease_started_at = $4, lease_expires_at = $5,
			next_attempt_at = NULL, updated_at = $4
		WHERE id = $1
	`, taskID, attempts, worker, startedAt, expiresAt); err != nil {
		t.Fatalf("lease recovery task: %v", err)
	}
	var lease tasks.Lease
	var scopeID *uuid.UUID
	var kindName string
	var token int64
	if err := pool.QueryRow(ctx, `
		SELECT id, kind, scope_type, scope_id, dedup_key, payload, priority,
			leased_worker, lease_token, attempts, lease_started_at, lease_expires_at
		FROM tasks WHERE id = $1
	`, taskID).Scan(&lease.TaskID, &kindName, &lease.ScopeType, &scopeID, &lease.DedupKey, &lease.Payload, &lease.Priority,
		&lease.WorkerID, &token, &lease.Attempt, &lease.StartedAt, &lease.ExpiresAt); err != nil {
		t.Fatalf("read recovery lease: %v", err)
	}
	lease.Kind = tasks.Kind(kindName)
	lease.ScopeID = scopeID
	lease.Token = uint64(token)
	return lease
}

func assertTaskState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, status string, attempts int, token int64, next time.Time) {
	t.Helper()
	var gotStatus string
	var gotAttempts int
	var gotToken int64
	var nextAttempt time.Time
	var worker *string
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, lease_token, next_attempt_at, leased_worker
		FROM tasks WHERE id = $1
	`, taskID).Scan(&gotStatus, &gotAttempts, &gotToken, &nextAttempt, &worker); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	if gotStatus != status || gotAttempts != attempts || gotToken != token || !nextAttempt.Equal(next) || worker != nil {
		t.Fatalf("task state = %s/%d/%d/%v/%v", gotStatus, gotAttempts, gotToken, nextAttempt, worker)
	}
}

func assertTerminalTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, status string, code string) {
	t.Helper()
	var gotStatus string
	var gotCode string
	var worker *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error_code, leased_worker
		FROM tasks WHERE id = $1
	`, taskID).Scan(&gotStatus, &gotCode, &worker); err != nil {
		t.Fatalf("read terminal task: %v", err)
	}
	if gotStatus != status || gotCode != code || worker != nil {
		t.Fatalf("terminal task = %s/%s/%v, want %s/%s/<nil>", gotStatus, gotCode, worker, status, code)
	}
}

func databaseTime(t *testing.T, ctx context.Context, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var now time.Time
	if err := pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatalf("read database time: %v", err)
	}
	return now
}
