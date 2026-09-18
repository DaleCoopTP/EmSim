// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// test/integration/task_queue_test.go; adapted: fixtures insert bare task
// rows (no runs/run_items/dialogues — nothing to reference, see
// docs/technical-discovery.md §3.1); Claim takes a Kinds set instead of
// one fixed Kind, so the old "dialogue and judge queues are isolated" case
// becomes "two claim pools are isolated" with arbitrary registered kinds.
// Added: priority ordering, Enqueue's dedup-returns-existing-id replay,
// Cancel semantics, and waiting tasks not being claimable.
//
//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskQueueLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)

	now := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	registry := newQueueTestRegistry(t)
	store := tasks.NewStore(pool, registry)

	t.Run("empty and future queue return no work", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		if _, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "worker-empty", now)); err != nil || found {
			t.Fatalf("empty claim found/error = %t/%v", found, err)
		}
		insertQueueTask(t, ctx, pool, queueKindA, 10, now.Add(time.Minute), now)
		if _, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "worker-future", now)); err != nil || found {
			t.Fatalf("future claim found/error = %t/%v", found, err)
		}
	})

	t.Run("one task has one current owner", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		start := make(chan struct{})
		type result struct {
			lease tasks.Lease
			found bool
			err   error
		}
		results := make(chan result, 2)
		for _, worker := range []string{"worker-one", "worker-two"} {
			worker := worker
			go func() {
				<-start
				lease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, worker, now))
				results <- result{lease: lease, found: found, err: err}
			}()
		}
		close(start)
		found := 0
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatalf("concurrent claim: %v", result.err)
			}
			if result.found {
				found++
				if result.lease.Attempt != 1 || result.lease.Token != 1 {
					t.Fatalf("first lease attempt/token = %d/%d", result.lease.Attempt, result.lease.Token)
				}
			}
		}
		if found != 1 {
			t.Fatalf("successful claims = %d, want 1", found)
		}
	})

	t.Run("two workers claim two different tasks", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		start := make(chan struct{})
		leases := make(chan tasks.Lease, 2)
		errorsChannel := make(chan error, 2)
		for _, worker := range []string{"worker-a", "worker-b"} {
			worker := worker
			go func() {
				<-start
				lease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, worker, now))
				if err == nil && !found {
					err = errors.New("no task claimed")
				}
				leases <- lease
				errorsChannel <- err
			}()
		}
		close(start)
		first := <-leases
		second := <-leases
		for range 2 {
			if err := <-errorsChannel; err != nil {
				t.Fatalf("claim one of two tasks: %v", err)
			}
		}
		if first.TaskID == second.TaskID {
			t.Fatalf("workers claimed the same task %s", first.TaskID)
		}
	})

	t.Run("locked oldest task does not block next eligible", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		oldest := insertQueueTask(t, ctx, pool, queueKindA, 10, now.Add(-time.Minute), now.Add(-time.Minute))
		next := insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		lockTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin task lock: %v", err)
		}
		defer func() { _ = lockTx.Rollback(ctx) }()
		if _, err := lockTx.Exec(ctx, "SELECT id FROM tasks WHERE id = $1 FOR UPDATE", oldest); err != nil {
			t.Fatalf("lock oldest task: %v", err)
		}
		lease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "worker-next", now))
		if err != nil || !found || lease.TaskID != next {
			t.Fatalf("claim with oldest locked = %s/%t/%v, want %s", lease.TaskID, found, err, next)
		}
	})

	t.Run("two claim pools are isolated", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		aTask := insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		bTask := insertQueueTask(t, ctx, pool, queueKindB, 10, now, now)
		bLease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindB}, "b-worker", now))
		if err != nil || !found || bLease.TaskID != bTask || bLease.Kind != queueKindB {
			t.Fatalf("pool b claim = %+v/%t/%v", bLease, found, err)
		}
		aLease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "a-worker", now))
		if err != nil || !found || aLease.TaskID != aTask || aLease.Kind != queueKindA {
			t.Fatalf("pool a claim = %+v/%t/%v", aLease, found, err)
		}
	})

	t.Run("priority is claimed before age", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		low := insertQueueTask(t, ctx, pool, queueKindA, 10, now.Add(-time.Minute), now.Add(-time.Minute))
		high := insertQueueTask(t, ctx, pool, queueKindA, 90, now, now)
		lease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "priority-worker", now))
		if err != nil || !found || lease.TaskID != high {
			t.Fatalf("priority claim = %s/%t/%v, want high-priority task %s (not low-priority %s)", lease.TaskID, found, err, high, low)
		}
	})

	t.Run("waiting tasks are not claimable", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertWaitingTask(t, ctx, pool, queueKindA, now)
		if _, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "waiting-worker", now)); err != nil || found {
			t.Fatalf("waiting claim found/error = %t/%v", found, err)
		}
	})

	t.Run("heartbeat and terminal writes require current lease", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		lease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "current-worker", now))
		if err != nil || !found {
			t.Fatalf("claim heartbeat task = %t/%v", found, err)
		}
		heartbeatAt := now.Add(30 * time.Second)
		expiresAt, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{Lease: lease, Now: heartbeatAt, LeaseDuration: 2 * time.Minute})
		if err != nil || !expiresAt.After(lease.ExpiresAt) {
			t.Fatalf("heartbeat expiry/error = %v/%v", expiresAt, err)
		}
		if _, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{Lease: lease, Now: heartbeatAt, LeaseDuration: time.Minute}); !errors.Is(err, tasks.ErrInvalidLeaseInterval) {
			t.Fatalf("non-extending heartbeat error = %v", err)
		}
		wrongWorker := lease
		wrongWorker.WorkerID = "wrong-worker"
		if _, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{Lease: wrongWorker, Now: heartbeatAt, LeaseDuration: 3 * time.Minute}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("wrong-worker heartbeat error = %v", err)
		}
		stale := lease
		stale.Token++
		if _, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{Lease: stale, Now: heartbeatAt, LeaseDuration: 3 * time.Minute}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("stale-token heartbeat error = %v", err)
		}
		outcome, err := tasks.Failed("invalid_response", []byte(`{"reason":"invalid_response"}`))
		if err != nil {
			t.Fatalf("failed outcome: %v", err)
		}
		terminalAt := heartbeatAt.Add(time.Second)
		terminalTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin terminal transaction: %v", err)
		}
		defer func() { _ = terminalTx.Rollback(ctx) }()
		if _, err := store.Terminal(ctx, terminalTx, tasks.TerminalRequest{Lease: wrongWorker, Now: terminalAt, Outcome: outcome}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("wrong-worker terminal error = %v", err)
		}
		if _, err := store.Terminal(ctx, terminalTx, tasks.TerminalRequest{Lease: stale, Now: terminalAt, Outcome: outcome}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("stale-token terminal error = %v", err)
		}
		if result, err := store.Terminal(ctx, terminalTx, tasks.TerminalRequest{Lease: lease, Now: terminalAt, Outcome: outcome}); err != nil || result != tasks.TerminalApplied {
			t.Fatalf("current terminal result/error = %q/%v", result, err)
		}
	})

	t.Run("terminal replay distinguishes identical and conflicting outcomes", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		lease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "replay-worker", now))
		if err != nil || !found {
			t.Fatalf("claim replay task = %t/%v", found, err)
		}
		terminalTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin replay transaction: %v", err)
		}
		defer func() { _ = terminalTx.Rollback(ctx) }()
		request := tasks.TerminalRequest{Lease: lease, Now: now.Add(time.Second), Outcome: tasks.Done([]byte(`{"ok":true}`))}
		if result, err := store.Terminal(ctx, terminalTx, request); err != nil || result != tasks.TerminalApplied {
			t.Fatalf("initial terminal result/error = %q/%v", result, err)
		}
		if result, err := store.Terminal(ctx, terminalTx, request); err != nil || result != tasks.TerminalAlreadyApplied {
			t.Fatalf("identical replay result/error = %q/%v", result, err)
		}
		conflicting := request
		conflicting.Outcome, err = tasks.Failed("remote_rejected", nil)
		if err != nil {
			t.Fatalf("conflicting outcome: %v", err)
		}
		if _, err := store.Terminal(ctx, terminalTx, conflicting); !errors.Is(err, tasks.ErrTerminalConflict) {
			t.Fatalf("conflicting replay error = %v", err)
		}
		wrongWorker := request
		wrongWorker.Lease.WorkerID = "other-worker"
		if _, err := store.Terminal(ctx, terminalTx, wrongWorker); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("wrong-worker replay error = %v", err)
		}
	})

	t.Run("enqueue is idempotent on dedup_key", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		taskID := uuid.New()
		request := tasks.EnqueueRequest{
			TaskID: taskID, Kind: queueKindA, ScopeType: "system",
			DedupKey: "enqueue-test:" + taskID.String(), NextAttemptAt: now,
		}
		firstID, created, err := store.Enqueue(ctx, request)
		if err != nil || !created || firstID != taskID {
			t.Fatalf("first enqueue id/created/error = %s/%t/%v", firstID, created, err)
		}
		replay := request
		replay.TaskID = uuid.New()
		secondID, created, err := store.Enqueue(ctx, replay)
		if err != nil || created || secondID != firstID {
			t.Fatalf("replay enqueue id/created/error = %s/%t/%v, want %s/false", secondID, created, err, firstID)
		}
	})

	t.Run("cancel moves pending and leased tasks to cancelled", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		pendingID := insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		cancelled, err := store.Cancel(ctx, tasks.CancelRequest{TaskID: pendingID, Now: now})
		if err != nil || !cancelled {
			t.Fatalf("cancel pending task = %t/%v", cancelled, err)
		}
		if again, err := store.Cancel(ctx, tasks.CancelRequest{TaskID: pendingID, Now: now}); err != nil || again {
			t.Fatalf("cancel already-cancelled task = %t/%v, want false", again, err)
		}

		leasedID := insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		lease, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "cancel-owner", now))
		if err != nil || !found || lease.TaskID != leasedID {
			t.Fatalf("claim task to cancel = %s/%t/%v", lease.TaskID, found, err)
		}
		cancelled, err = store.Cancel(ctx, tasks.CancelRequest{TaskID: leasedID, Now: now})
		if err != nil || !cancelled {
			t.Fatalf("cancel leased task = %t/%v", cancelled, err)
		}
		if _, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{Lease: lease, Now: now.Add(time.Second), LeaseDuration: time.Minute}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("heartbeat on cancelled lease error = %v, want ErrLeaseLost", err)
		}
	})
}

const (
	queueKindA tasks.Kind = "queue.alpha"
	queueKindB tasks.Kind = "queue.beta"
)

func newQueueTestRegistry(t *testing.T) *tasks.Registry {
	t.Helper()
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, spec := range []tasks.Spec{
		{Name: queueKindA, Pool: "a", MaxAttempts: 3, Lease: 90 * time.Second, RetryBase: 5 * time.Second, Priority: 10},
		{Name: queueKindB, Pool: "b", MaxAttempts: 3, Lease: 90 * time.Second, RetryBase: 5 * time.Second, Priority: 10},
	} {
		if err := registry.Register(spec); err != nil {
			t.Fatalf("register %s: %v", spec.Name, err)
		}
	}
	return registry
}

func claimKinds(kinds []tasks.Kind, worker string, now time.Time) tasks.ClaimRequest {
	return tasks.ClaimRequest{Kinds: kinds, WorkerID: worker, Now: now}
}

func resetTasks(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "DELETE FROM tasks"); err != nil {
		t.Fatalf("reset tasks: %v", err)
	}
}

func insertQueueTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind tasks.Kind, priority int16, eligibleAt, createdAt time.Time) uuid.UUID {
	t.Helper()
	taskID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (
			id, kind, scope_type, dedup_key, priority, status, attempts,
			max_attempts, lease_token, next_attempt_at, created_at, updated_at
		) VALUES ($1, $2, 'system', $3, $4, 'pending', 0, 3, 0, $5, $6, $6)
	`, taskID, string(kind), "queue-test:"+taskID.String(), priority, eligibleAt, createdAt); err != nil {
		t.Fatalf("insert queue %s task: %v", kind, err)
	}
	return taskID
}

func insertWaitingTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind tasks.Kind, createdAt time.Time) uuid.UUID {
	t.Helper()
	taskID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (
			id, kind, scope_type, dedup_key, status, max_attempts,
			wait_until, wait_reason, created_at, updated_at
		) VALUES ($1, $2, 'system', $3, 'waiting', 3, $4, 'dependency_pending', $5, $5)
	`, taskID, string(kind), "waiting-test:"+taskID.String(), createdAt.Add(time.Minute), createdAt); err != nil {
		t.Fatalf("insert waiting %s task: %v", kind, err)
	}
	return taskID
}
