// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/worker/composition.go; adapted: no dialogue/judge application
// services to compose — this skeleton registers the one kind that exists
// so far, system.noop (a health-check task with no domain effect; a
// training/assessment module registers its own kinds against the same
// Registry once it exists). Pools are short/llm/stt instead of one Runner
// per fixed Kind. e2eRecoveryPolicy keeps the shared timing knobs, but no
// longer touches retry bases (those are per-kind Spec.RetryBase now, set
// once in registerKinds regardless of policy — see kindSystemNoop).
package main

import (
	"context"
	"errors"
	"time"

	"emsim/internal/platform/config"
	"emsim/internal/platform/observability"
	"emsim/internal/platform/tasks"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// kindSystemNoop is a trivial task kind that always succeeds: the
// end-to-end proof that enqueue -> claim -> handle -> terminal works
// through a real database before any domain module exists (RFC-001 §13,
// W0: "задача noop проходит очередь").
const kindSystemNoop tasks.Kind = "system.noop"

func registerKinds(registry *tasks.Registry) error {
	return registry.Register(tasks.Spec{
		Name: kindSystemNoop, Pool: "short", MaxAttempts: 3,
		Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 10,
	})
}

func noopHandler(pool *pgxpool.Pool, store *tasks.Store) tasks.Handler {
	return tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return errors.New("noop transaction failed")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(nil),
		}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("noop commit failed")
		}
		return nil
	})
}

func compose(processConfig config.Worker, pool *pgxpool.Pool, metrics *observability.Metrics, logger observability.Logger) (tasks.Components, error) {
	policy := tasks.DefaultPolicy()
	if processConfig.LocalTestPolicy == "e2e-fast-v1" {
		policy = e2eRecoveryPolicy()
	}
	registry, err := tasks.NewRegistry(policy)
	if err != nil {
		return tasks.Components{}, errors.New("registry configuration is invalid")
	}
	if err := registerKinds(registry); err != nil {
		return tasks.Components{}, errors.New("kind registration is invalid")
	}

	store := tasks.NewStore(pool, registry)
	recoveryStore, err := tasks.NewRecovery(pool, policy, tasks.NoJitter{}, registry)
	if err != nil {
		return tasks.Components{}, errors.New("recovery configuration is invalid")
	}

	components := tasks.Components{}
	if processConfig.Role == tasks.RoleWorker || processConfig.Role == tasks.RoleAll {
		components.Worker, err = composePools(processConfig, policy, pool, store, recoveryStore, registry, metrics)
		if err != nil {
			return tasks.Components{}, err
		}
	}
	if processConfig.Role == tasks.RoleMaintenance || processConfig.Role == tasks.RoleAll {
		components.Maintenance, err = composeMaintenance(policy, pool, recoveryStore, metrics, logger)
		if err != nil {
			return tasks.Components{}, err
		}
	}
	return components, nil
}

func composePools(
	processConfig config.Worker, policy tasks.Policy, pool *pgxpool.Pool,
	store *tasks.Store, recoveryStore *tasks.Recovery, registry *tasks.Registry, metrics *observability.Metrics,
) (tasks.Supervisor, error) {
	handlers := tasks.NewHandlerRegistry()
	if err := handlers.Register(kindSystemNoop, noopHandler(pool, store)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}

	var runners []tasks.Supervisor
	for _, poolConfig := range []struct {
		name string
		size int
	}{
		{"short", processConfig.ShortConcurrency},
		{"llm", processConfig.LLMConcurrency},
		{"stt", processConfig.STTConcurrency},
	} {
		kinds := registry.Pool(poolConfig.name)
		if len(kinds) == 0 {
			continue
		}
		runner, err := newRunner(poolConfig.name, poolConfig.size, kinds, processConfig, policy, store, recoveryStore, handlers, registry, metrics)
		if err != nil {
			return nil, err
		}
		runners = append(runners, runner)
	}
	if len(runners) == 0 {
		return nil, errors.New("no task pools are registered")
	}
	return tasks.Composite(runners...)
}

func composeMaintenance(
	policy tasks.Policy, pool *pgxpool.Pool, recoveryStore *tasks.Recovery, metrics *observability.Metrics, logger observability.Logger,
) (tasks.Supervisor, error) {
	reaper, err := tasks.NewReaper(policy, tasks.SystemTickerFactory{}, recoveryStore)
	if err != nil {
		return nil, errors.New("maintenance configuration is invalid")
	}
	sampler, err := observability.NewSampler(
		tasks.NewSamplerStore(pool), metrics, logger, 15*time.Second, 5*time.Second, observability.SystemTickerFactory{},
	)
	if err != nil {
		return nil, errors.New("maintenance configuration is invalid")
	}
	return tasks.Composite(reaper, sampler)
}

func e2eRecoveryPolicy() tasks.Policy {
	policy := tasks.DefaultPolicy()
	policy.HeartbeatInterval = 500 * time.Millisecond
	policy.HeartbeatJitter = 0
	policy.HeartbeatTimeout = 200 * time.Millisecond
	policy.SafetyMargin = 500 * time.Millisecond
	policy.ReaperInterval = 200 * time.Millisecond
	policy.ReclaimGrace = 0
	policy.RetryCap = 400 * time.Millisecond
	return policy
}

func newRunner(
	poolName string, poolSize int, kinds []tasks.Kind, processConfig config.Worker, policy tasks.Policy,
	store *tasks.Store, recoveryStore *tasks.Recovery, handlers *tasks.HandlerRegistry, registry *tasks.Registry, metrics *observability.Metrics,
) (*tasks.Runner, error) {
	runner, err := tasks.NewRunner(
		poolName, poolSize, kinds, processConfig.WorkerID, policy, tasks.NoJitter{}, tasks.SystemClock{},
		tasks.SystemTickerFactory{}, tasks.SystemTimerFactory{}, store, recoveryStore, handlers, registry,
		processConfig.PollInterval, processConfig.DrainTimeout,
	)
	if err == nil {
		runner.SetObserver(metrics)
	}
	return runner, err
}
