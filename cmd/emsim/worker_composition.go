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
	"log/slog"
	"os"
	"strings"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/assessment/dds/commentjudge"
	"emsim/internal/assessment/operator112/descjudge"
	"emsim/internal/content"
	"emsim/internal/platform/audit"
	"emsim/internal/platform/backup"
	"emsim/internal/platform/config"
	"emsim/internal/platform/llm"
	"emsim/internal/platform/observability"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/status"
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

// kindAuditPrune (ADR-033) deletes audit_log rows older than
// AUDIT_RETENTION_DAYS once a day; the maintenance scheduler enqueues it.
const kindAuditPrune tasks.Kind = "audit.prune"

// auditPruneBatch bounds one DELETE statement of audit.prune.
const auditPruneBatch = 5000

// kindBackupRun (ADR-033) makes one copy of the database and blob store
// into BACKUP_DIR; the scheduler enqueues it daily, POST /admin/backup on
// demand.
const kindBackupRun tasks.Kind = "backup.run"

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
	// ADR-033: daily audit cleanup, short pool, low priority — it never
	// competes with lesson.close. Lease covers a large first cleanup.
	if err := registry.Register(tasks.Spec{
		Name: kindAuditPrune, Pool: "short", MaxAttempts: 3,
		Lease: 10 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 5,
	}); err != nil {
		return err
	}
	// ADR-033: backup shares the single "report" slot with PDF builds, so
	// a long dump never holds a short worker lesson.close needs. The lease
	// is renewed by heartbeats while pg_dump runs.
	if err := registry.Register(tasks.Spec{
		Name: kindBackupRun, Pool: "report", MaxAttempts: 2,
		Lease: 10 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 10,
	}); err != nil {
		return err
	}
	// ADR-038: the integrity check reads every blob back, so it shares the
	// "report" slot with backups and PDFs and runs at low priority. One
	// attempt: the next daily run (or the admin's button) tries again.
	if err := registry.Register(tasks.Spec{
		Name: kindIntegrityCheck, Pool: "report", MaxAttempts: 1,
		Lease: 30 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 5,
	}); err != nil {
		return err
	}
	// ADR-029: best-effort prompt-cache warm-up, same "caller" pool but a
	// lower priority, so a real reply waiting for a free worker is always
	// claimed first. One attempt: a failed warm-up only means the next
	// reply starts cold, never worth a retry.
	if err := registry.Register(tasks.Spec{
		Name: training.KindCallerWarmup, Pool: "caller", MaxAttempts: 1,
		Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 50,
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

// auditPruneBackupMaxAge is how old the newest backup copy may be for
// audit.prune to delete anything (ADR-038).
const auditPruneBackupMaxAge = 24 * time.Hour

// recentBackupExists reports whether dir holds a complete copy made within
// auditPruneBackupMaxAge of now.
func recentBackupExists(dir string, now time.Time) bool {
	copies, err := backup.List(dir)
	if err != nil || len(copies) == 0 {
		return false
	}
	return now.Sub(copies[0].CreatedAt) <= auditPruneBackupMaxAge
}

// auditPruneHandler is kindAuditPrune's worker side (ADR-033). The
// batched delete commits as it goes (audit.Prune); the prune's own audit
// row and the task's terminal state commit together afterwards. A retry
// after a failure only finds what is left, so partial progress is safe.
//
// ADR-038: with a backup directory configured, rows are deleted only when
// a complete copy younger than auditPruneBackupMaxAge exists, so nothing is
// removed while the installation has no recent copy that still holds it. A
// missing copy fails the task retryably; the status screen shows it and the
// administrator's retry runs it again once a copy exists.
func auditPruneHandler(pool *pgxpool.Pool, store *tasks.Store, retentionDays int, backupDir string) tasks.Handler {
	return tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		if backupDir != "" && !recentBackupExists(backupDir, time.Now()) {
			failure, ferr := tasks.NewHandlerFailure(tasks.Retryable, "audit_prune_no_recent_backup")
			if ferr != nil {
				return ferr
			}
			return failure
		}
		deleted, err := audit.Prune(ctx, pool, retentionDays, auditPruneBatch)
		if err != nil {
			failure, ferr := tasks.NewHandlerFailure(tasks.Retryable, "audit_prune_failed")
			if ferr != nil {
				return ferr
			}
			return failure
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return errors.New("audit.prune transaction failed")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		details := map[string]any{"deleted": deleted, "retention_days": retentionDays}
		if err := audit.Record(ctx, tx, audit.Entry{
			ActorRole: "system", Action: "audit.prune", ResourceType: "audit_log", Outcome: audit.OutcomeOK, Details: details,
		}); err != nil {
			return errors.New("audit.prune record failed")
		}
		result, err := json.Marshal(details)
		if err != nil {
			return errors.New("audit.prune result encoding failed")
		}
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(result),
		}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("audit.prune commit failed")
		}
		return nil
	})
}

// backupHandler is kindBackupRun's worker side (ADR-033). The copy is made
// outside any transaction; its audit row and the task's terminal state
// then commit together. A copy made by an attempt whose terminal commit
// fails stays on disk and is simply one more copy that rotation handles.
func backupHandler(pool *pgxpool.Pool, store *tasks.Store, cfg backup.Config) tasks.Handler {
	return tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		made, err := backup.Run(ctx, cfg, pgstore.ExpectedSchemaVersion, time.Now())
		if err != nil {
			retryability, code := tasks.Retryable, tasks.ErrorCode("backup_write_failed")
			switch {
			case errors.Is(err, backup.ErrNotConfigured):
				retryability, code = tasks.Permanent, "backup_not_configured"
			case errors.Is(err, backup.ErrDump):
				code = "backup_dump_failed"
			case errors.Is(err, backup.ErrBlobs):
				code = "backup_blobs_failed"
			}
			failure, ferr := tasks.NewHandlerFailure(retryability, code)
			if ferr != nil {
				return ferr
			}
			return failure
		}
		result, err := json.Marshal(made)
		if err != nil {
			return errors.New("backup.run result encoding failed")
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return errors.New("backup.run transaction failed")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := audit.Record(ctx, tx, audit.Entry{
			ActorRole: "system", Action: "backup.run", ResourceType: "backup", Outcome: audit.OutcomeOK,
			Details: map[string]any{"name": made.Name, "size_bytes": made.SizeBytes},
		}); err != nil {
			return errors.New("backup.run record failed")
		}
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(result),
		}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("backup.run commit failed")
		}
		probe := backupProbe(cfg.Dir)
		copyStatus, detail := probe.Check(ctx)
		_ = status.NewStore(pool).UpsertHeartbeat(ctx, probe.Component, copyStatus, detail)
		return nil
	})
}

// backupConfig is backup.Config for this process; BLOB_ROOT is read from
// the environment like every other blob user in this binary.
func backupConfig(processConfig config.Worker) backup.Config {
	return backup.Config{
		Dir: processConfig.BackupDir, BlobRoot: os.Getenv("BLOB_ROOT"),
		DatabaseURL: processConfig.DatabaseURL, Keep: processConfig.BackupKeep,
	}
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

// callerWarmupPrefix is the transcript a caller.warmup task of stage
// should warm (training.KindCallerWarmup), or the reason it should not
// run: once the dialogue has moved past what the stage covers, the next
// reply is already on its way and warming an older prefix would only
// compete with it for the model.
//   - stage system (answer_incoming): only while the operator has not
//     written yet; the prefix is the system prompt alone.
//   - stage opening (first operator line): only until the first model
//     reply is requested; the prefix is that line and the scenario's
//     opening — added here if the delayed opening is not applied yet,
//     it is fixed text, so the prefix matches either way.
//   - "" (enqueued before stages existed): the whole transcript, while
//     the caller's line is the last one.
func callerWarmupPrefix(stage string, caller *content.Intake112CallerProfile, transcript []training.IntakeLine) ([]training.IntakeLine, string) {
	if caller == nil {
		return nil, "no_caller"
	}
	switch stage {
	case training.CallerWarmupStageSystem:
		if len(transcript) == 0 {
			return nil, ""
		}
	case training.CallerWarmupStageOpening:
		switch {
		case len(transcript) == 1 && transcript[0].Speaker == "operator":
			return append(append([]training.IntakeLine(nil), transcript...), training.IntakeLine{Speaker: "caller", Text: caller.Opening.Text}), ""
		case len(transcript) == 2 && transcript[1].Speaker == "caller":
			return transcript, ""
		}
	case "":
		if len(transcript) > 0 && transcript[len(transcript)-1].Speaker == "caller" {
			return transcript, ""
		}
	}
	return nil, "stale"
}

// callerWarmupHandler is training.KindCallerWarmup's worker side. It
// reads the dialogue without any lock, calls warm outside any
// transaction (bounded by timeout, like a reply), and always finishes
// the task as done — the result only records whether the model was
// actually warmed and, if not, why, since the warm-up is best-effort and
// has no domain effect to retry for.
func callerWarmupHandler(pool *pgxpool.Pool, store *tasks.Store, trainingService *training.Service, warm func(context.Context, operator112.CallerReplyRequest) error, timeout time.Duration) tasks.Handler {
	return tasks.HandlerFunc(func(ctx context.Context, lease tasks.Lease) error {
		var payload struct {
			ItemID uuid.UUID `json:"item_id"`
			Stage  string    `json:"stage"`
		}
		if err := json.Unmarshal(lease.Payload, &payload); err != nil {
			return fmt.Errorf("caller.warmup: decode payload: %w", err)
		}
		result := `{"warmed":false,"reason":"disabled"}`
		if warm != nil {
			replyCtx, err := trainingService.CallerReplyContext(ctx, payload.ItemID)
			if err != nil {
				result = `{"warmed":false,"reason":"no_dialogue"}`
			} else if prefix, reason := callerWarmupPrefix(payload.Stage, replyCtx.Dialogue.Caller, replyCtx.Transcript); reason != "" {
				result = `{"warmed":false,"reason":"` + reason + `"}`
			} else {
				callCtx, cancel := context.WithTimeout(ctx, timeout)
				err = warm(callCtx, operator112.CallerReplyRequest{
					Facts: replyCtx.Dialogue.Facts, Caller: replyCtx.Dialogue.Caller, Transcript: prefix,
				})
				cancel()
				result = `{"warmed":true}`
				if err != nil {
					result = `{"warmed":false,"reason":"model_unavailable"}`
				}
			}
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return errors.New("caller.warmup transaction failed")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := store.Terminal(ctx, tx, tasks.TerminalRequest{
			Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done([]byte(result)),
		}); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return errors.New("caller.warmup commit failed")
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

// judgeConfigFor is ADR-028's own worker-side wiring: nil when
// ASSESSMENT_JUDGE is off (explicit since ADR-029 — no judge at all,
// matching 112-6's pre-ADR-028 behavior exactly), otherwise a JudgeConfig
// whose Registry has one entry per judged prompt version — descjudge's
// (112, ADR-028) and, since ADR-034, commentjudge's two DDS handlers —
// all sharing one llm.Client; the same "one prompt version, one
// handler" convention. Model/Parameters are sealed
// into assessment_inputs.judge verbatim by sealInputForItem, so a later
// config change is visible in old evidence without diffing deployed
// code against a timestamp, the same reasoning aicaller.PromptVersion's
// own doc comment gives for caller.reply.
func judgeConfigFor(processConfig config.Worker) *assessment.JudgeConfig {
	if processConfig.AssessmentJudge != config.AssessmentJudgeLLM {
		return nil
	}
	chat := llm.NewClientWith(processConfig.JudgeLLMURL, llm.Options{APIKey: processConfig.JudgeLLMAPIKey, Dialect: processConfig.LLMDialect})
	return &assessment.JudgeConfig{
		Model:      processConfig.JudgeLLMModel,
		Parameters: map[string]any{"temperature": 0, "max_tokens": processConfig.JudgeMaxTokens},
		Registry: assessment.SemanticJudgeRegistry{
			descjudge.PromptVersion:           descjudge.Handler{Chat: chat},
			commentjudge.FactsPromptVersion:   commentjudge.FactsHandler{Chat: chat},
			commentjudge.GrammarPromptVersion: commentjudge.GrammarHandler{Chat: chat},
		},
		Timeout: processConfig.JudgeTimeout,
	}
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

	assessmentService := newAssessmentService(pool, store, judgeConfigFor(processConfig))
	if err := recoveryStore.RegisterFinalizer(training.KindAssessmentEvaluate, assessmentService); err != nil {
		return tasks.Components{}, errors.New("finalizer registration is invalid")
	}
	// trainingService is built here (not only inside composePools, once
	// role includes worker) because the Reaper — composeMaintenance,
	// which every role including a worker-less "maintenance" process
	// runs — needs training.KindCallerReply's Finalizer registered
	// regardless of whether this same process also claims its pool.
	trainingService := newTrainingService(pool, store, processConfig.AssessmentJudge == config.AssessmentJudgeLLM)
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
		components.Maintenance, err = composeMaintenance(processConfig, policy, pool, store, recoveryStore, metrics, logger)
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
	if err := handlers.Register(kindAuditPrune, auditPruneHandler(pool, store, processConfig.AuditRetentionDays, processConfig.BackupDir)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(kindBackupRun, backupHandler(pool, store, backupConfig(processConfig))); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(kindIntegrityCheck, integrityHandler(pool, store, processConfig)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(training.KindAssessmentEvaluate, assessmentService); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	if err := handlers.Register(reporting.KindBuild, reporting.NewBuilderFromEnvironment(reportingpg.NewStore(pool), store)); err != nil {
		return nil, errors.New("handler configuration is invalid")
	}
	// 112-5b/ADR-025: CALLER_REPLIER selects aicaller.Replier (default
	// since ADR-029 — a model call over an OpenAI-compatible endpoint,
	// compose's own llm service, falling back to the
	// very same stub for a scenario with no caller profile — see
	// aicaller.Replier's own doc comment) or StubCallerReplier (explicit
	// CALLER_REPLIER=stub — 112-5a's own behavior, no model dependency).
	// callerFallback stays nil for the stub: only the model path can fail
	// in a way ADR-025's neutral reply is meant to cover.
	stubDelay, err := callerStubDelay()
	if err != nil {
		return nil, err
	}
	stubReplier := operator112.StubCallerReplier{Delay: stubDelay}
	var callerReplier operator112.CallerReplier = stubReplier
	var callerFallback func(operator112.CallerReplyRequest) operator112.CallerReply
	var callerWarm func(context.Context, operator112.CallerReplyRequest) error
	if processConfig.CallerReplier == config.CallerReplierLLM {
		aiReplier := aicaller.Replier{
			Chat: llm.NewClientWith(processConfig.CallerLLMURL, llm.Options{APIKey: processConfig.CallerLLMAPIKey, Dialect: processConfig.LLMDialect}), Model: processConfig.CallerLLMModel,
			Temperature: processConfig.CallerTemperature, TopP: processConfig.CallerTopP,
			RepeatPenalty: processConfig.CallerRepeatPenalty, MaxTokens: processConfig.CallerMaxTokens,
			Stub: stubReplier,
		}
		callerReplier = aiReplier
		callerFallback = aiReplier.Fallback
		if processConfig.CallerWarmup {
			callerWarm = aiReplier.Warm
		}
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
	// Registered whatever CALLER_REPLIER says: a warm-up already queued
	// before a switch to the stub still has a handler, which then just
	// finishes it without a model call.
	if err := handlers.Register(training.KindCallerWarmup, callerWarmupHandler(pool, store, trainingService, callerWarm, processConfig.CallerReplyTimeout)); err != nil {
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
	processConfig config.Worker, policy tasks.Policy, pool *pgxpool.Pool, store *tasks.Store,
	recoveryStore *tasks.Recovery, metrics *observability.Metrics, logger observability.Logger,
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
	// ADR-033: daily maintenance tasks. A failed enqueue is logged and
	// retried on the next tick; it never stops the process.
	scheduler, err := tasks.NewScheduler(maintenanceSchedules(processConfig), processConfig.ScheduleLocation, time.Minute,
		tasks.SystemClock{}, tasks.SystemTickerFactory{}, store,
		func(ctx context.Context, kind tasks.Kind, _ error) {
			logger.Operation(ctx, slog.LevelWarn, "task_schedule", "failed", "", "enqueue_failed_"+strings.ReplaceAll(string(kind), ".", "_"))
		})
	if err != nil {
		return nil, errors.New("maintenance configuration is invalid")
	}
	prober := status.NewProber(statusProbes(processConfig), status.NewStore(pool), 30*time.Second, 5*time.Second,
		func(interval time.Duration) status.Ticker { return tasks.SystemTickerFactory{}.NewTicker(interval) })
	return tasks.Composite(reaper, sampler, scheduler, prober)
}

// statusProbes are what this worker reports for the admin status screen
// (ADR-033): that it is alive, whether its model answers, and the backup
// directory's free space and copies — things the api cannot see itself.
func statusProbes(processConfig config.Worker) []status.Probe {
	probes := []status.Probe{{
		Component: status.ComponentWorkerPrefix + heartbeatID(processConfig.WorkerID),
		Check: func(context.Context) (string, map[string]any) {
			return status.StatusOK, map[string]any{"role": string(processConfig.Role), "version": buildVersion}
		},
	}}
	// ADR-038: the settings this worker runs with, for the api's read-only
	// configuration screen. Rewritten on every probe round, so a restart
	// with a changed .env shows the new values within half a minute.
	probes = append(probes, status.Probe{
		Component: status.ComponentConfigWorkerPrefix + shortHeartbeatID(processConfig.WorkerID),
		Check: func(context.Context) (string, map[string]any) {
			return status.StatusOK, map[string]any{"params": processConfig.Public()}
		},
	})
	if url, model, key := modelEndpoint(processConfig); url != "" {
		probeURL := strings.TrimSuffix(strings.TrimSuffix(url, "/"), "/v1") + "/health"
		if processConfig.LLMDialect == llm.DialectOpenAI {
			probeURL = strings.TrimSuffix(url, "/") + "/models"
		}
		probes = append(probes, status.Probe{Component: status.ComponentLLM, Check: status.HTTPCheck(probeURL, key, map[string]any{"model": model})})
	}
	if processConfig.BackupDir != "" {
		probes = append(probes, backupProbe(processConfig.BackupDir))
	}
	return probes
}

// backupProbe reports the backup directory's free space and its copies.
// backupHandler also runs it right after a copy, so the status screen
// lists a new copy without waiting for the next probe round.
func backupProbe(dir string) status.Probe {
	return status.Probe{Component: status.ComponentBackup, Check: func(context.Context) (string, map[string]any) {
		free, err := status.FreeBytes(dir)
		if err != nil {
			return status.StatusUnavailable, map[string]any{}
		}
		copies, err := backup.List(dir)
		if err != nil {
			return status.StatusUnavailable, map[string]any{"free_bytes": free}
		}
		if copies == nil {
			copies = []backup.Copy{}
		}
		return status.StatusOK, map[string]any{"free_bytes": free, "copies": copies}
	}}
}

// modelEndpoint is the model this worker calls — the caller's if the AI
// caller is on, otherwise the judge's — or empty when neither is.
func modelEndpoint(processConfig config.Worker) (url, model, key string) {
	switch {
	case processConfig.CallerReplier == config.CallerReplierLLM:
		return processConfig.CallerLLMURL, processConfig.CallerLLMModel, processConfig.CallerLLMAPIKey
	case processConfig.AssessmentJudge == config.AssessmentJudgeLLM:
		return processConfig.JudgeLLMURL, processConfig.JudgeLLMModel, processConfig.JudgeLLMAPIKey
	}
	return "", "", ""
}

// heartbeatID fits a WORKER_ID into platform_heartbeats.component's
// alphabet.
// shortHeartbeatID keeps "config.worker.<id>" within the 64-character
// component limit of platform_heartbeats.
func shortHeartbeatID(workerID string) string {
	id := heartbeatID(workerID)
	if len(id) > 40 {
		id = id[:40]
	}
	return id
}

func heartbeatID(workerID string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(workerID) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
		if b.Len() >= 56 {
			break
		}
	}
	if b.Len() == 0 {
		return "worker"
	}
	return b.String()
}

// maintenanceSchedules lists the daily tasks the maintenance scheduler
// enqueues (ADR-033).
func maintenanceSchedules(processConfig config.Worker) []tasks.Schedule {
	schedules := []tasks.Schedule{{Kind: kindAuditPrune, At: processConfig.AuditPruneAt}, {Kind: kindIntegrityCheck, At: integrityCheckAt}}
	// No BACKUP_DIR (development, tests): no daily backup is scheduled.
	if processConfig.BackupDir != "" {
		schedules = append(schedules, tasks.Schedule{Kind: kindBackupRun, At: processConfig.BackupAt})
	}
	return schedules
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
