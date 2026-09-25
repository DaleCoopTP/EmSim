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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/platform/config"
	"emsim/internal/platform/llm"
	"emsim/internal/platform/observability"
	"emsim/internal/platform/tasks"
	"emsim/internal/reporting"
	reportingpg "emsim/internal/reporting/postgres"
	"emsim/internal/training"
	"emsim/internal/training/operator112"
	"emsim/internal/training/operator112/aicaller"

	"github.com/google/uuid"
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
	// 112-5a/ADR-024: its own pool "caller" (never queued behind
	// assessment.evaluate/scenario.generate's own "llm" pool, or behind
	// lesson.close's "short" pool), priority 100 — an applicant waiting
	// on a reply is as time-sensitive as either of those. Lease is
	// comfortably longer than CALLER_REPLY_TIMEOUT (config.Worker),
	// which bounds a single CallerReplier.Reply call, not this Spec's
	// own lease; MaxAttempts=2 with a registered Finalizer (
	// callerReplyFinalizer) turns exhaustion into CallerTurnFailed
	// instead of dead_letter.
	if err := registry.Register(tasks.Spec{
		Name: training.KindCallerReply, Pool: "caller", MaxAttempts: 2,
		Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 100,
	}); err != nil {
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

// callerReplyPayload is training.KindCallerReply's own payload shape —
// decoded from what training.Service.enqueueCallerReply marshals
// (item_id/turn), used by both the handler below and
// callerReplyFinalizer.
type callerReplyPayload struct {
	ItemID uuid.UUID `json:"item_id"`
	Turn   int       `json:"turn"`
}

// callerReplyHandler is training.KindCallerReply's worker side
// (112-5a/ADR-024): CallerReplier.Reply is called entirely outside any
// transaction, bounded by timeout — the queue's first task kind to
// actually call an external adapter with real latency (ADR-003/
// ADR-024; assessment.evaluate's own rule evaluation, by contrast, runs
// as pure Go inside its own transaction, since no LLM client exists yet
// anywhere in this codebase). The reply is then applied and the task's
// own terminal write committed together in one transaction, the same
// atomicity lessonCloseHandler already demonstrates.
//
// Reply's own error (timeout or adapter failure) is the one expected,
// routine failure this handler can hit — unlike a decode/transaction
// error, which stays a plain error so Runner treats it as the
// operational bug it would be (failure.go: "an unexpected error, which
// the Runner treats as an operational failure and stops on"). Reply's
// error must instead come back as a *tasks.HandlerFailure so
// ResolveFailure requeues it (Spec.MaxAttempts=2, RetryBase) and, once
// exhausted, delegates to callerReplyFinalizer instead of crashing the
// whole worker process — UNLESS fallback is non-nil and this is already
// the task's last allowed attempt (lease.Attempt >= maxAttempts, the
// same condition ResolveFailureTx itself uses to stop retrying,
// internal/platform/tasks/pgrecovery.go): ADR-025's decision 6 applies
// fallback's neutral reply instead, so the turn becomes Answered with
// Source=fallback rather than Failed, and the conversation keeps going
// even though the model itself could not be reached. fallback is nil for
// CALLER_REPLIER=stub — StubCallerReplier keeps 112-5a's original
// retry-then-Failed behavior unchanged, since it has no model to fail
// against in the first place.
// callerReplyFallbackOutcome is callerReplyHandler's own retry-vs-
// fallback decision (ADR-025's decision 6), pulled out as a pure
// function so it is testable without a database: apply fallback's
// neutral reply only when one is configured (CALLER_REPLIER=llm) and
// attempt is already the task's last allowed one — attempt >= maxAttempts
// is exactly ResolveFailureTx's own "attempts < maxAttempts" condition
// negated (internal/platform/tasks/pgrecovery.go), so this never fires
// on a try the queue would have retried anyway. ok=false means "return
// a retryable HandlerFailure instead", the pre-112-5b behavior.
func callerReplyFallbackOutcome(fallback func(operator112.CallerReplyRequest) operator112.CallerReply, req operator112.CallerReplyRequest, attempt, maxAttempts int) (operator112.CallerReply, bool) {
	if fallback == nil || attempt < maxAttempts {
		return operator112.CallerReply{}, false
	}
	return fallback(req), true
}

func callerReplyHandler(pool *pgxpool.Pool, store *tasks.Store, trainingService *training.Service, replier operator112.CallerReplier, timeout time.Duration, maxAttempts int, fallback func(operator112.CallerReplyRequest) operator112.CallerReply) tasks.Handler {
	return tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		var payload callerReplyPayload
		if err := json.Unmarshal(lease.Payload, &payload); err != nil {
			return fmt.Errorf("caller.reply: decode payload: %w", err)
		}
		replyCtx, err := trainingService.CallerReplyContext(ctx, payload.ItemID)
		if err != nil {
			return err
		}
		req := operator112.CallerReplyRequest{
			Facts: replyCtx.Dialogue.Facts, Caller: replyCtx.Dialogue.Caller, Transcript: replyCtx.Transcript, Turn: payload.Turn,
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		reply, err := replier.Reply(callCtx, req)
		cancel()
		if err != nil {
			fallbackReply, applyFallback := callerReplyFallbackOutcome(fallback, req, lease.Attempt, maxAttempts)
			if !applyFallback {
				failure, ferr := tasks.NewHandlerFailure(tasks.Retryable, "caller_reply_unavailable")
				if ferr != nil {
					return ferr
				}
				return failure
			}
			reply = fallbackReply
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return errors.New("caller.reply transaction failed")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		outcome := training.CallerReplyOutcome{
			Text: reply.Text, Adapter: reply.Adapter, Source: reply.Source,
			Reveals: reply.Reveals, Generation: reply.Generation,
		}
		if err := trainingService.ApplyCallerReply(ctx, tx, payload.ItemID, payload.Turn, outcome, time.Now().UTC()); err != nil {
			return err
		}
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(nil),
		}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("caller.reply commit failed")
		}
		return nil
	})
}

// callerReplyFinalizer is training.KindCallerReply's tasks.Finalizer
// (112-5a/ADR-024): once the task's own attempt budget is exhausted, the
// pending IntakeCallerTurn becomes CallerTurnFailed — via
// training.Service.FailCallerTurn, in the same transaction as the
// task's own terminal write — instead of staying pending forever and
// blocking every further message in that chat. The pattern (re-derive
// scope from PeekPayload, apply the domain effect, then
// FinalizeExpiredTx, all under one transaction) mirrors assessment.
// Service.FinalizeExpired exactly.
type callerReplyFinalizer struct {
	pool            *pgxpool.Pool
	store           *tasks.Store
	trainingService *training.Service
}

func (f callerReplyFinalizer) FinalizeExpired(ctx context.Context, taskID uuid.UUID, workerID string, token uint64, terminalStatus tasks.TaskStatus, code tasks.ErrorCode) error {
	scopeID, payloadRaw, err := f.store.PeekPayload(ctx, taskID)
	if err != nil {
		return err
	}
	if scopeID == nil {
		return tasks.ErrLeaseLost
	}
	var payload callerReplyPayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return fmt.Errorf("caller.reply: decode exhausted task payload: %w", err)
	}
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return errors.New("caller.reply finalize transaction failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := f.trainingService.FailCallerTurn(ctx, tx, *scopeID, payload.Turn, "reply_unavailable", time.Now().UTC()); err != nil {
		return err
	}
	ok, err := f.store.FinalizeExpiredTx(ctx, tx, taskID, workerID, token, terminalStatus, code)
	if err != nil {
		return err
	}
	if !ok {
		return tasks.ErrLeaseLost
	}
	return tx.Commit(ctx)
}

// callerStubDelay reads CALLER_STUB_DELAY the same direct way
// cmd/emsim already reads other leaf, feature-specific settings outside
// config.Worker's own validated surface (e.g. BLOB_ROOT in import.go/
// internal/training/http) — a demo/dev tuning knob for
// operator112.StubCallerReplier, not a structural concern like pool
// sizing or lease duration. It defaults to one second (RFC-001's own
// "заявитель отвечает не мгновенно" intent for 112-5a, see
// slice-112-5a-plan.md) so the queued/async protocol is visibly
// exercised even with no CALLER_STUB_DELAY set at all; e2e tests set it
// low (or 0) to stay fast. An unparsable non-empty value is a
// configuration mistake, not a silent fallback.
func callerStubDelay() (time.Duration, error) {
	raw := os.Getenv("CALLER_STUB_DELAY")
	if raw == "" {
		return time.Second, nil
	}
	delay, err := time.ParseDuration(raw)
	if err != nil || delay < 0 {
		return 0, fmt.Errorf("invalid CALLER_STUB_DELAY %q: %w", raw, err)
	}
	return delay, nil
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
	// trainingService is built here (not only inside composePools, once
	// role includes worker) because the Reaper — composeMaintenance,
	// which every role including a worker-less "maintenance" process
	// runs — needs training.KindCallerReply's Finalizer registered
	// regardless of whether this same process also claims its pool.
	trainingService := newTrainingService(pool, store)
	if err := recoveryStore.RegisterFinalizer(training.KindCallerReply, callerReplyFinalizer{pool: pool, store: store, trainingService: trainingService}); err != nil {
		return tasks.Components{}, errors.New("finalizer registration is invalid")
	}

	components := tasks.Components{}
	if processConfig.Role == tasks.RoleWorker || processConfig.Role == tasks.RoleAll {
		components.Worker, err = composePools(processConfig, policy, pool, store, recoveryStore, registry, metrics, assessmentService, trainingService)
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
	assessmentService *assessment.Service, trainingService *training.Service,
) (tasks.Supervisor, error) {
	handlers := tasks.NewHandlerRegistry()
	if err := handlers.Register(kindSystemNoop, noopHandler(pool, store)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(training.KindLessonClose, lessonCloseHandler(pool, store, trainingService)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(training.KindAssessmentEvaluate, assessmentService); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(reporting.KindBuild, reporting.NewBuilderFromEnvironment(reportingpg.NewStore(pool), store)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	// 112-5b/ADR-025: CALLER_REPLIER selects StubCallerReplier (default —
	// 112-5a's own behavior, no model dependency) or aicaller.Replier (a
	// model call over an OpenAI-compatible endpoint, falling back to the
	// very same stub for a scenario with no caller profile — see
	// aicaller.Replier's own doc comment). callerFallback stays nil for
	// the stub: only the model path can fail in a way ADR-025's neutral
	// reply is meant to cover.
	stubDelay, err := callerStubDelay()
	if err != nil {
		return nil, err
	}
	stubReplier := operator112.StubCallerReplier{Delay: stubDelay}
	var callerReplier operator112.CallerReplier = stubReplier
	var callerFallback func(operator112.CallerReplyRequest) operator112.CallerReply
	if processConfig.CallerReplier == config.CallerReplierLLM {
		aiReplier := aicaller.Replier{
			Chat: llm.NewClient(processConfig.CallerLLMURL), Model: processConfig.CallerLLMModel,
			Temperature: processConfig.CallerTemperature, TopP: processConfig.CallerTopP,
			RepeatPenalty: processConfig.CallerRepeatPenalty, MaxTokens: processConfig.CallerMaxTokens,
			Stub: stubReplier,
		}
		callerReplier = aiReplier
		callerFallback = aiReplier.Fallback
	}
	callerReplySpec, ok := registry.Lookup(training.KindCallerReply)
	if !ok {
		return nil, errors.New("caller.reply kind is not registered")
	}
	if err := handlers.Register(training.KindCallerReply, callerReplyHandler(
		pool, store, trainingService, callerReplier, processConfig.CallerReplyTimeout, callerReplySpec.MaxAttempts, callerFallback,
	)); err != nil {
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
		{"caller", processConfig.CallerConcurrency},
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
