// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/worker/composition.go; adapted: no dialogue/judge application
// services to compose — this skeleton registers the one kind that exists
// so far, system.noop (a health-check task with no domain effect; a
// training/assessment module registers its own kinds against the same
// Registry once it exists). Pools are short/llm/stt/report instead of one Runner
// per fixed Kind. e2eRecoveryPolicy keeps the shared timing knobs, but no
// longer touches retry bases (those are per-kind Spec.RetryBase now, set
// once in registerKinds regardless of policy — see kindSystemNoop).
package main

import (
	"context"
	"errors"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/platform/config"
	"emsim/internal/platform/observability"
	"emsim/internal/platform/tasks"
	"emsim/internal/reporting"
	reportingpg "emsim/internal/reporting/postgres"
	"emsim/internal/training"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// kindSystemNoop is a trivial task kind that always succeeds: the
// end-to-end proof that enqueue -> claim -> handle -> terminal works
// through a real database before any domain module exists (RFC-001 §13,
// W0: "задача noop проходит очередь").
const kindSystemNoop tasks.Kind = "system.noop"

// registerKinds is the single place every task kind's Spec is declared
// (docs/technical-discovery.md §6), shared by both processes that touch
// the queue: the worker (which needs it to run handlers) and the api
// process (which needs it to enqueue training.KindLessonClose with the
// right priority/max_attempts — EnqueueTx reads those from the Registry,
// not from the caller, see internal/training/service.go's Stop). A Spec
// mismatch between what api enqueues and what worker expects cannot
// happen because both call this exact function.
func registerKinds(registry *tasks.Registry) error {
	if err := registry.Register(tasks.Spec{
		Name: kindSystemNoop, Pool: "short", MaxAttempts: 3,
		Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 10,
	}); err != nil {
		return err
	}
	// slice-4-plan.md's C8 spec: pool short, priority 100, lease 2
	// minutes, 5 attempts, retry base 200ms.
	if err := registry.Register(tasks.Spec{
		Name: training.KindLessonClose, Pool: "short", MaxAttempts: 5,
		Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 100,
	}); err != nil {
		return err
	}
	// slice-6-plan.md's C5/C6 spec (RFC-001 §7.4): pool llm, priority
	// 100, lease 5 minutes, 3 attempts. RetryBase is 200ms, same as
	// lesson.close above — not because assessment.evaluate needs a fast
	// retry, but because this one Registry is shared with e2eRecoveryPolicy
	// (RetryCap 400ms) in this file's own e2e-fast tests, and every
	// registered Spec must satisfy the strictest policy that will ever
	// validate it; the real production retry cadence for assessment.evaluate
	// is a slice 6/C6 concern (its own coordinator/handler/finalizer,
	// registered separately once internal/assessment exists), not this
	// placeholder Spec's. training's close (this file's own api process)
	// enqueues it straight into waiting via EnqueueWaitingTx, which — like
	// EnqueueTx above — reads the Spec from this Registry.
	if err := registry.Register(tasks.Spec{
		Name: training.KindAssessmentEvaluate, Pool: "llm", MaxAttempts: 3,
		Lease: 5 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 100,
	}); err != nil {
		return err
	}
	// PDF generation must not occupy short workers used by lesson.close.
	if err := registry.Register(tasks.Spec{Name: reporting.KindBuild, Pool: "report", MaxAttempts: 3, Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 20}); err != nil {
		return err
	}
	return nil
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

// lessonCloseHandler is training.KindLessonClose's worker side (C8): the
// domain close (trainingService.CloseStoppedLesson) and tasks.Terminal
// commit in one transaction, the same fencing/atomicity noopHandler
// already demonstrates for a trivial task — Terminal itself rejects a
// stale lease (lost to the reaper or a second attempt after a crash), so
// a lease that fails this commit simply leaves the domain change rolled
// back for the next attempt to redo, never partially applied.
func lessonCloseHandler(pool *pgxpool.Pool, store *tasks.Store, trainingService *training.Service) tasks.Handler {
	return tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		if lease.ScopeID == nil {
			return errors.New("lesson.close: task has no scope_id")
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return errors.New("lesson.close transaction failed")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := trainingService.CloseStoppedLesson(ctx, tx, *lease.ScopeID); err != nil {
			return err
		}
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(nil),
		}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("lesson.close commit failed")
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

	assessmentService := newAssessmentService(pool, store)
	if err := recoveryStore.RegisterFinalizer(training.KindAssessmentEvaluate, assessmentService); err != nil {
		return tasks.Components{}, errors.New("finalizer registration is invalid")
	}

	components := tasks.Components{}
	if processConfig.Role == tasks.RoleWorker || processConfig.Role == tasks.RoleAll {
		components.Worker, err = composePools(processConfig, policy, pool, store, recoveryStore, registry, metrics, assessmentService)
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
	assessmentService *assessment.Service,
) (tasks.Supervisor, error) {
	handlers := tasks.NewHandlerRegistry()
	if err := handlers.Register(kindSystemNoop, noopHandler(pool, store)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	trainingService := newTrainingService(pool, store)
	if err := handlers.Register(training.KindLessonClose, lessonCloseHandler(pool, store, trainingService)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(training.KindAssessmentEvaluate, assessmentService); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(reporting.KindBuild, reporting.NewBuilderFromEnvironment(reportingpg.NewStore(pool), store)); err != nil {
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
		{"report", processConfig.ReportConcurrency},
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
	// slice-6-plan.md's C6 coordinator (RFC-001 §7.4): a plain 2s ticking
	// loop inside this same worker process, not a claimed task kind of
	// its own — it never appears in registry.Pool(...) above. Shortened
	// under the e2e-fast test policy exactly like e2eRecoveryPolicy
	// shortens ReaperInterval, so integration tests do not wait multiple
	// real seconds per tick.
	coordinatorInterval := 2 * time.Second
	if processConfig.LocalTestPolicy == "e2e-fast-v1" {
		coordinatorInterval = 100 * time.Millisecond
	}
	coordinator, err := assessment.NewCoordinator(assessmentService, tasks.SystemTickerFactory{}, coordinatorInterval, processConfig.WorkerID, policy.ReaperBatch)
	if err != nil {
		return nil, errors.New("coordinator configuration is invalid")
	}
	runners = append(runners, coordinator)
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
