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
	"github.com/jackc/pgx/v5"
)

func TestTaskGuarantees(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	registry := newQueueTestRegistry(t)
	store := tasks.NewStore(pool, registry)
	recovery, err := tasks.NewRecovery(pool, tasks.DefaultPolicy(), tasks.NoJitter{}, registry)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("committed replay compares JSON contents and preserves result", func(t *testing.T) {
		for _, failed := range []bool{false, true} {
			resetTasks(t, ctx, pool)
			now := databaseTime(t, ctx, pool)
			insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
			lease := mustClaimKind(t, ctx, store, queueKindA, "replay", now)
			outcome := tasks.Done([]byte(`{"a":1,"b":2}`))
			if failed {
				outcome, err = tasks.Failed("invalid_response", outcome.Result)
				if err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			request := tasks.TerminalRequest{Lease: lease, Now: now, Outcome: outcome}
			if got, err := store.Terminal(ctx, tx, request); err != nil || got != tasks.TerminalApplied {
				t.Fatalf("terminal = %s/%v", got, err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				result   []byte
				conflict bool
			}{
				{[]byte(`{ "b":2, "a":1 }`), false},
				{[]byte(`{"a":2,"b":2}`), true},
				{nil, true},
				{[]byte("null"), true},
			} {
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				request.Outcome.Result = test.result
				got, callErr := store.Terminal(ctx, tx, request)
				_ = tx.Rollback(ctx)
				if test.conflict {
					if !errors.Is(callErr, tasks.ErrTerminalConflict) {
						t.Fatalf("changed result = %s/%v", got, callErr)
					}
				} else if callErr != nil || got != tasks.TerminalAlreadyApplied {
					t.Fatalf("replay = %s/%v", got, callErr)
				}
			}
			tx, err = pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			request.Outcome = outcome
			if failed {
				request.Outcome.Code = "different_code"
			} else {
				request.Outcome, err = tasks.Failed("invalid_response", outcome.Result)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, conflictErr := store.Terminal(ctx, tx, request)
			_ = tx.Rollback(ctx)
			if !errors.Is(conflictErr, tasks.ErrTerminalConflict) {
				t.Fatalf("status/code replay = %v", conflictErr)
			}
			var unchanged bool
			if err := pool.QueryRow(ctx, "SELECT result = $2::jsonb FROM tasks WHERE id=$1", lease.TaskID, outcome.Result).Scan(&unchanged); err != nil || !unchanged {
				t.Fatalf("stored result changed: %t/%v", unchanged, err)
			}
		}
	})

	t.Run("caller clock cannot claim future work or revive expired lease", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now.Add(time.Hour), now)
		if _, found, err := store.Claim(ctx, claimKinds([]tasks.Kind{queueKindA}, "future", now.Add(24*time.Hour))); err != nil || found {
			t.Fatalf("future claim = %t/%v", found, err)
		}
		lease := insertLeasedTask(t, ctx, pool, queueKindA, 1, "expired", now.Add(-time.Minute), now.Add(-time.Second))
		staleNow := now.Add(-30 * time.Second)
		if _, err := store.Heartbeat(ctx, tasks.HeartbeatRequest{Lease: lease, Now: staleNow, LeaseDuration: time.Minute}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("heartbeat = %v", err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{Lease: lease, Now: staleNow, Outcome: tasks.Done(nil)}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("terminal = %v", err)
		}
		_ = tx.Rollback(ctx)
		if _, err := recovery.ResolveFailure(ctx, tasks.FailureRequest{Lease: lease, Now: staleNow, Failure: handlerFailure(t, tasks.Permanent, "invalid_response")}); !errors.Is(err, tasks.ErrLeaseLost) {
			t.Fatalf("failure = %v", err)
		}
	})

	t.Run("clock is sampled after a task lock wait", func(t *testing.T) {
		for _, operation := range []string{"terminal", "heartbeat", "failure"} {
			t.Run(operation, func(t *testing.T) {
				resetTasks(t, ctx, pool)
				now := databaseTime(t, ctx, pool)
				lease := insertLeasedTask(t, ctx, pool, queueKindA, 1, "blocked", now.Add(-time.Minute), now.Add(time.Minute))
				locker, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer locker.Rollback(ctx)
				if _, err := locker.Exec(ctx, "SELECT id FROM tasks WHERE id=$1 FOR UPDATE", lease.TaskID); err != nil {
					t.Fatal(err)
				}
				var blockerPID int
				if err := locker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blockerPID); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				failure := handlerFailure(t, tasks.Permanent, "invalid_response")
				go func() {
					var err error
					switch operation {
					case "terminal":
						var tx pgx.Tx
						tx, err = pool.Begin(ctx)
						if err == nil {
							_, err = store.Terminal(ctx, tx, tasks.TerminalRequest{Lease: lease, Now: now, Outcome: tasks.Done(nil)})
							_ = tx.Rollback(ctx)
						}
					case "heartbeat":
						_, err = store.Heartbeat(ctx, tasks.HeartbeatRequest{Lease: lease, Now: now, LeaseDuration: 2 * time.Minute})
					case "failure":
						_, err = recovery.ResolveFailure(ctx, tasks.FailureRequest{Lease: lease, Now: now, Failure: failure})
					}
					result <- err
				}()
				deadline := time.Now().Add(5 * time.Second)
				for {
					var blocked bool
					if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))", blockerPID).Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("operation did not wait on task lock")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if _, err := locker.Exec(ctx, "UPDATE tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", lease.TaskID); err != nil {
					t.Fatal(err)
				}
				if err := locker.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-result; !errors.Is(err, tasks.ErrLeaseLost) {
					t.Fatalf("expired after lock wait = %v", err)
				}
			})
		}
	})

	t.Run("terminal and cancel have one winner", func(t *testing.T) {
		resetTasks(t, ctx, pool)
		now := databaseTime(t, ctx, pool)
		insertQueueTask(t, ctx, pool, queueKindA, 10, now, now)
		lease := mustClaimKind(t, ctx, store, queueKindA, "cancel-race", now)
		start := make(chan struct{})
		terminalDone := make(chan error, 1)
		type cancelResult struct {
			changed bool
			err     error
		}
		cancelDone := make(chan cancelResult, 1)
		go func() {
			<-start
			tx, err := pool.Begin(ctx)
			if err == nil {
				_, err = store.Terminal(ctx, tx, tasks.TerminalRequest{Lease: lease, Now: now, Outcome: tasks.Done(nil)})
				if err == nil {
					err = tx.Commit(ctx)
				}
				_ = tx.Rollback(ctx)
			}
			terminalDone <- err
		}()
		go func() {
			<-start
			changed, err := store.Cancel(ctx, tasks.CancelRequest{TaskID: lease.TaskID, Now: now})
			cancelDone <- cancelResult{changed, err}
		}()
		close(start)
		terminalErr, cancelled := <-terminalDone, <-cancelDone
		if cancelled.err != nil {
			t.Fatal(cancelled.err)
		}
		if terminalErr != nil && !errors.Is(terminalErr, tasks.ErrLeaseLost) {
			t.Fatal(terminalErr)
		}
		if (terminalErr == nil) == cancelled.changed {
			t.Fatalf("terminal=%v, cancelled=%t", terminalErr, cancelled.changed)
		}
		var status string
		if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", lease.TaskID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		want := "done"
		if cancelled.changed {
			want = "cancelled"
		}
		if status != want {
			t.Fatalf("winner status=%s, want %s", status, want)
		}
	})

	t.Run("enqueue and cancel commit or roll back with domain effect", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "CREATE TABLE review_effects (id uuid PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = pool.Exec(context.Background(), "DROP TABLE review_effects") }()
		for _, commit := range []bool{false, true} {
			resetTasks(t, ctx, pool)
			id := uuid.New()
			request := tasks.EnqueueRequest{TaskID: id, Kind: queueKindA, ScopeType: "system", DedupKey: id.String(), NextAttemptAt: time.Now()}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO review_effects VALUES ($1)", id); err != nil {
				t.Fatal(err)
			}
			if _, created, err := store.EnqueueTx(ctx, tx, request); err != nil || !created {
				t.Fatalf("enqueue = %t/%v", created, err)
			}
			var visible bool
			if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT FROM tasks WHERE id=$1)", id).Scan(&visible); err != nil || visible {
				t.Fatalf("uncommitted task visible: %t/%v", visible, err)
			}
			if commit {
				err = tx.Commit(ctx)
			} else {
				err = tx.Rollback(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			var taskExists, effectExists bool
			if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT FROM tasks WHERE id=$1), EXISTS(SELECT FROM review_effects WHERE id=$1)", id).Scan(&taskExists, &effectExists); err != nil {
				t.Fatal(err)
			}
			if taskExists != commit || effectExists != commit {
				t.Fatalf("atomic enqueue = %t/%t", taskExists, effectExists)
			}
			if !commit {
				if _, _, err := store.Enqueue(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, "INSERT INTO review_effects VALUES ($1) ON CONFLICT DO NOTHING", id); err != nil {
				t.Fatal(err)
			}
			tx, err = pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "DELETE FROM review_effects WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			if changed, err := store.CancelTx(ctx, tx, tasks.CancelRequest{TaskID: id, Now: time.Now()}); err != nil || !changed {
				t.Fatalf("cancel = %t/%v", changed, err)
			}
			if commit {
				err = tx.Commit(ctx)
			} else {
				err = tx.Rollback(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			var status string
			if err := pool.QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", id).Scan(&status); err != nil {
				t.Fatal(err)
			}
			want := "pending"
			if commit {
				want = "cancelled"
			}
			if status != want {
				t.Fatalf("cancel status = %s, want %s", status, want)
			}
			if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT FROM review_effects WHERE id=$1)", id).Scan(&effectExists); err != nil || effectExists != !commit {
				t.Fatalf("effect after cancellation = %t/%v", effectExists, err)
			}
		}
		if _, _, err := store.EnqueueTx(ctx, nil, tasks.EnqueueRequest{}); !errors.Is(err, tasks.ErrInvalidRequest) {
			t.Fatalf("nil enqueue tx = %v", err)
		}
		if _, err := store.CancelTx(ctx, nil, tasks.CancelRequest{}); !errors.Is(err, tasks.ErrInvalidRequest) {
			t.Fatalf("nil cancel tx = %v", err)
		}
	})
}
