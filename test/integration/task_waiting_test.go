// New test: platform/tasks' waiting-task primitives (slice 6, ADR-019) —
// EnqueueWaitingTx, WaitingDue, PromoteWaitingTx, FailWaitingTx and
// ByDedupKey — against real PostgreSQL. A waiting task already existed at
// the DB-constraint level (migrations/00001's tasks_state_shape); this is
// new Go-level plumbing on top of it, exercised generically rather than
// through internal/assessment (a later commit's own integration tests
// cover the coordinator that actually calls these for real).
//
//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"emsim/internal/platform/tasks"

	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const waitingKind tasks.Kind = "test.waiting"

func TestTaskWaitingLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)

	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Register(tasks.Spec{
		Name: waitingKind, Pool: "waiting", MaxAttempts: 3, Lease: 90 * time.Second, RetryBase: 5 * time.Second, Priority: 100,
	}); err != nil {
		t.Fatalf("register waiting kind: %v", err)
	}
	store := tasks.NewStore(pool, registry)

	t.Run("waiting task is enqueued blocked and not claimable", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		taskID := uuid.New()
		created, err := beginEnqueueWaiting(t, ctx, pool, store, tasks.EnqueueWaitingRequest{
			TaskID: taskID, Kind: waitingKind, ScopeType: "item", DedupKey: "wait:" + taskID.String(),
			Payload: []byte(`{"item_id":"x"}`), WaitUntil: now, WaitReason: "awaiting_input",
		})
		if err != nil || !created {
			t.Fatalf("enqueue waiting = %t/%v", created, err)
		}
		assertWaitingTaskState(t, ctx, pool, taskID, 0)
		if _, found, err := store.Claim(ctx, tasks.ClaimRequest{Kinds: []tasks.Kind{waitingKind}, WorkerID: "claim-attempt", Now: now}); err != nil || found {
			t.Fatalf("claim waiting task found/error = %t/%v", found, err)
		}
	})

	t.Run("enqueueing the same dedup key twice returns the existing id", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		firstID := uuid.New()
		request := tasks.EnqueueWaitingRequest{
			TaskID: firstID, Kind: waitingKind, ScopeType: "item", DedupKey: "wait:dup",
			WaitUntil: now, WaitReason: "awaiting_input",
		}
		created1, err := beginEnqueueWaiting(t, ctx, pool, store, request)
		if err != nil || !created1 {
			t.Fatalf("first enqueue = %t/%v", created1, err)
		}
		request.TaskID = uuid.New()
		gotID, created2, err := enqueueWaitingTx(t, ctx, pool, store, request)
		if err != nil || created2 {
			t.Fatalf("second enqueue created/error = %t/%v", created2, err)
		}
		if gotID != firstID {
			t.Fatalf("second enqueue id = %s, want original %s", gotID, firstID)
		}
	})

	t.Run("WaitingDue finds only due tasks of the requested kind", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		dueID := uuid.New()
		if _, _, err := enqueueWaitingTx(t, ctx, pool, store, tasks.EnqueueWaitingRequest{
			TaskID: dueID, Kind: waitingKind, ScopeType: "item", DedupKey: "wait:due",
			Payload: []byte(`{"n":1}`), WaitUntil: now.Add(-time.Second), WaitReason: "awaiting_input",
		}); err != nil {
			t.Fatal(err)
		}
		futureID := uuid.New()
		if _, _, err := enqueueWaitingTx(t, ctx, pool, store, tasks.EnqueueWaitingRequest{
			TaskID: futureID, Kind: waitingKind, ScopeType: "item", DedupKey: "wait:future",
			WaitUntil: now.Add(time.Hour), WaitReason: "awaiting_input",
		}); err != nil {
			t.Fatal(err)
		}
		due, err := store.WaitingDue(ctx, waitingKind, now, 10)
		if err != nil {
			t.Fatalf("waiting due: %v", err)
		}
		if len(due) != 1 || due[0].TaskID != dueID {
			t.Fatalf("waiting due = %+v, want only %s", due, dueID)
		}
		if !jsonEqual(t, due[0].Payload, `{"n":1}`) {
			t.Fatalf("waiting due payload = %s", due[0].Payload)
		}
	})

	t.Run("PromoteWaitingTx makes a task claimable with its new payload", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		taskID := uuid.New()
		if _, _, err := enqueueWaitingTx(t, ctx, pool, store, tasks.EnqueueWaitingRequest{
			TaskID: taskID, Kind: waitingKind, ScopeType: "item", DedupKey: "wait:promote",
			Payload: []byte(`{"item_id":"x"}`), WaitUntil: now, WaitReason: "awaiting_input",
		}); err != nil {
			t.Fatal(err)
		}
		mustPromoteWaiting(t, ctx, pool, store, taskID, []byte(`{"item_id":"x","input_id":"y"}`), now)
		assertTaskState(t, ctx, pool, taskID, "pending", 0, 0, now)

		lease, found, err := store.Claim(ctx, tasks.ClaimRequest{Kinds: []tasks.Kind{waitingKind}, WorkerID: "promoted-worker", Now: now})
		if err != nil || !found {
			t.Fatalf("claim promoted task = %t/%v", found, err)
		}
		if !jsonEqual(t, lease.Payload, `{"item_id":"x","input_id":"y"}`) {
			t.Fatalf("claimed payload = %s, want the promoted payload", lease.Payload)
		}

		// A retried promote (e.g. the coordinator crashing right after its
		// own commit) is a silent no-op — it must not resurrect an
		// already-leased task back to pending.
		mustPromoteWaiting(t, ctx, pool, store, taskID, []byte(`{"ignored":true}`), now)
		var status string
		var attempts int
		if err := pool.QueryRow(ctx, "SELECT status, attempts FROM tasks WHERE id = $1", taskID).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		if status != "leased" || attempts != 1 {
			t.Fatalf("task state after repeat promote = %s/%d, want leased/1 unchanged", status, attempts)
		}
	})

	t.Run("FailWaitingTx terminates input preparation failure without ever having attempted", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		taskID := uuid.New()
		if _, _, err := enqueueWaitingTx(t, ctx, pool, store, tasks.EnqueueWaitingRequest{
			TaskID: taskID, Kind: waitingKind, ScopeType: "item", DedupKey: "wait:fail",
			WaitUntil: now, WaitReason: "awaiting_input",
		}); err != nil {
			t.Fatal(err)
		}
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return store.FailWaitingTx(ctx, tx, taskID, "coordinator", "input_preparation_failed")
		}); err != nil {
			t.Fatalf("fail waiting: %v", err)
		}
		assertTerminalTask(t, ctx, pool, taskID, "failed", "input_preparation_failed")
		var attempts int
		if err := pool.QueryRow(ctx, "SELECT attempts FROM tasks WHERE id = $1", taskID).Scan(&attempts); err != nil {
			t.Fatal(err)
		}
		if attempts != 0 {
			t.Fatalf("attempts after FailWaitingTx = %d, want 0 (never claimed)", attempts)
		}

		// Idempotent: a retry after the same failure is a no-op, not a
		// second terminal write.
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return store.FailWaitingTx(ctx, tx, taskID, "coordinator", "input_preparation_failed")
		}); err != nil {
			t.Fatalf("repeat fail waiting: %v", err)
		}
	})

	t.Run("ByDedupKey reports current status, or ErrNotFound", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		taskID := uuid.New()
		if _, _, err := enqueueWaitingTx(t, ctx, pool, store, tasks.EnqueueWaitingRequest{
			TaskID: taskID, Kind: waitingKind, ScopeType: "item", DedupKey: "wait:lookup",
			WaitUntil: now, WaitReason: "awaiting_input",
		}); err != nil {
			t.Fatal(err)
		}
		summary, err := store.ByDedupKey(ctx, nil, "wait:lookup")
		if err != nil || summary.TaskID != taskID || summary.Status != tasks.TaskWaiting {
			t.Fatalf("by dedup key = %+v/%v", summary, err)
		}
		if _, err := store.ByDedupKey(ctx, nil, "wait:missing"); !errors.Is(err, tasks.ErrNotFound) {
			t.Fatalf("missing dedup key error = %v, want ErrNotFound", err)
		}
	})
}

func enqueueWaitingTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *tasks.Store, request tasks.EnqueueWaitingRequest) (uuid.UUID, bool, error) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, created, err := store.EnqueueWaitingTx(ctx, tx, request)
	if err != nil {
		return id, created, err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id, created, nil
}

func beginEnqueueWaiting(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *tasks.Store, request tasks.EnqueueWaitingRequest) (bool, error) {
	t.Helper()
	_, created, err := enqueueWaitingTx(t, ctx, pool, store, request)
	return created, err
}

func mustPromoteWaiting(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *tasks.Store, taskID uuid.UUID, payload []byte, nextAttemptAt time.Time) {
	t.Helper()
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return store.PromoteWaitingTx(ctx, tx, taskID, payload, nextAttemptAt)
	}); err != nil {
		t.Fatalf("promote waiting: %v", err)
	}
}

func assertWaitingTaskState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, attempts int) {
	t.Helper()
	var status string
	var gotAttempts int
	var waitUntil, waitReason *string
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, wait_until::text, wait_reason
		FROM tasks WHERE id = $1
	`, taskID).Scan(&status, &gotAttempts, &waitUntil, &waitReason); err != nil {
		t.Fatalf("read waiting task: %v", err)
	}
	if status != "waiting" || gotAttempts != attempts || waitUntil == nil || waitReason == nil {
		t.Fatalf("waiting task state = %s/%d/%v/%v", status, gotAttempts, waitUntil, waitReason)
	}
}

// jsonEqual compares two JSON documents by value, not by byte-for-byte
// text — PostgreSQL's jsonb round-trip reformats whitespace (e.g. a space
// after each colon), so a literal string comparison against what a test
// wrote would be comparing formatting, not data.
func jsonEqual(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("unmarshal got JSON %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal want JSON %s: %v", want, err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}
