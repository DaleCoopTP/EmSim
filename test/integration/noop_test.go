// New test: RFC-001 §13's W0 acceptance criterion — "задача noop проходит
// очередь" ("the noop task passes through the queue") — a Runner backed
// by a real database claims, handles, and terminates a task end to end.
// This exercises the same shape as cmd/emsim's system.noop kind
// (worker_composition.go) but is self-contained: test/integration cannot
// import cmd/emsim (it is package main).
//
//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestNoopTaskPassesThroughTheQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	resetTasks(t, ctx, pool)

	const noopKind tasks.Kind = "system.noop"
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Register(tasks.Spec{
		Name: noopKind, Pool: "short", MaxAttempts: 3, Lease: 90 * time.Second, RetryBase: time.Second, Priority: 10,
	}); err != nil {
		t.Fatalf("register noop kind: %v", err)
	}
	store := tasks.NewStore(pool, registry)
	recoveryStore, err := tasks.NewRecovery(pool, tasks.DefaultPolicy(), tasks.NoJitter{}, registry)
	if err != nil {
		t.Fatalf("new recovery: %v", err)
	}

	handlers := tasks.NewHandlerRegistry()
	if err := handlers.Register(noopKind, tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(nil),
		}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})); err != nil {
		t.Fatalf("register noop handler: %v", err)
	}

	runner, err := tasks.NewRunner(
		"short", 1, []tasks.Kind{noopKind}, "noop-test-worker", tasks.DefaultPolicy(), tasks.NoJitter{}, tasks.SystemClock{},
		tasks.SystemTickerFactory{}, tasks.SystemTimerFactory{}, store, recoveryStore, handlers, registry,
		100*time.Millisecond, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	taskID := uuid.New()
	if _, _, err := store.Enqueue(ctx, tasks.EnqueueRequest{
		TaskID: taskID, Kind: noopKind, ScopeType: "system",
		DedupKey: "noop-test:" + taskID.String(), NextAttemptAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("enqueue noop task: %v", err)
	}

	runCtx, runCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runner.Run(runCtx) }()
	t.Cleanup(func() {
		runCancel()
		if err := <-done; err != nil {
			t.Errorf("runner shutdown: %v", err)
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	var status string
	for {
		if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id = $1", taskID).Scan(&status); err != nil {
			t.Fatalf("read noop task status: %v", err)
		}
		if status == "done" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("noop task did not reach done within 5s, status = %q", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
