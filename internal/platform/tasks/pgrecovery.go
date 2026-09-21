// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/postgres/task_recovery.go; adapted: drop the failItem call at
// the end of the dead-letter/failed paths entirely. Core's version rolled
// a task's permanent failure into its owning run_item's status
// (docs/technical-discovery.md §3.1: "провал задачи оценки уронит карточку,
// если бездумно перенести failItem"). Here a task's failure changes only
// that task's own row — a training item, a scenario version, or whatever
// else owns the task learns about the failure by reading the task (or a
// domain callback registered against its kind), not by an FK-cascaded
// status flip. RetryDelay's base now comes from the claimed lease's Kind,
// looked up in the Registry, instead of a fixed Dialogue/Judge base.
package tasks

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const errCodeLeaseExpired ErrorCode = "lease_expired"

// errDelegateToFinalizer is ResolveFailureTx's own signal to ResolveFailure
// that this kind has a registered Finalizer: ResolveFailureTx has already
// verified the lease is genuinely exhausted (under the tasks row's own
// FOR UPDATE lock, same as every other outcome it computes), but must not
// itself write anything — the caller rolls this transaction back (freeing
// that lock) and calls the Finalizer instead, which needs to acquire its
// own domain locks before re-acquiring the task's row lock (RFC-001 §8).
// It never escapes this package.
var errDelegateToFinalizer = errors.New("tasks: exhaustion delegated to finalizer")

// Recovery resolves a task handler's classified failure (retry, fail, or
// dead-letter) and reaps tasks whose lease expired without a heartbeat.
type Recovery struct {
	pool     *pgxpool.Pool
	policy   Policy
	jitter   JitterSource
	registry *Registry

	mu         sync.RWMutex
	finalizers map[Kind]Finalizer
}

func NewRecovery(pool *pgxpool.Pool, policy Policy, jitter JitterSource, registry *Registry) (*Recovery, error) {
	if pool == nil || policy.Validate() != nil || jitter == nil || registry == nil {
		return nil, ErrInvalidPolicy
	}
	return &Recovery{pool: pool, policy: policy, jitter: jitter, registry: registry}, nil
}

func (r *Recovery) ResolveFailure(ctx context.Context, request FailureRequest) (Resolution, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", ErrStorage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	resolution, err := r.ResolveFailureTx(ctx, tx, request)
	if errors.Is(err, errDelegateToFinalizer) {
		_ = tx.Rollback(ctx) // release the tasks-row lock before the finalizer re-acquires domain-first
		return r.finalizeResolvedFailure(ctx, request)
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", ErrStorage
	}
	return resolution, nil
}

// finalizeResolvedFailure is ResolveFailure's delegation half: called only
// after ResolveFailureTx already verified, under the (now-released) tasks
// row lock, that this lease is genuinely exhausted and its kind has a
// registered Finalizer. terminalStatus/code are recomputed here — cheap,
// pure functions of request.Failure — rather than threaded back out of
// the rolled-back transaction.
func (r *Recovery) finalizeResolvedFailure(ctx context.Context, request FailureRequest) (Resolution, error) {
	finalizer, ok := r.finalizerFor(request.Lease.Kind)
	if !ok {
		return "", ErrStorage
	}
	terminalStatus := TaskFailed
	resolution := ResolutionFailed
	if request.Failure.Retryability() == Retryable {
		terminalStatus = TaskDeadLetter
		resolution = ResolutionDeadLetter
	}
	if err := finalizer.FinalizeExpired(ctx, request.Lease.TaskID, request.Lease.WorkerID, request.Lease.Token, terminalStatus, request.Failure.Code()); err != nil {
		return "", err
	}
	return resolution, nil
}

func (r *Recovery) ResolveFailureTx(ctx context.Context, tx pgx.Tx, request FailureRequest) (Resolution, error) {
	if tx == nil {
		return "", ErrInvalidRequest
	}
	if err := request.Validate(); err != nil {
		return "", err
	}
	var status string
	var worker *string
	var token int64
	var expiresAt *time.Time
	var attempts int
	var maxAttempts int
	err := tx.QueryRow(ctx, `
		SELECT status, leased_worker, lease_token, lease_expires_at, attempts, max_attempts
		FROM tasks
		WHERE id = $1
		FOR UPDATE
	`, request.Lease.TaskID).Scan(&status, &worker, &token, &expiresAt, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", ErrStorage
	}
	now, err := databaseTime(ctx, tx)
	if err != nil {
		return "", err
	}
	// Preserve the requested backoff duration, anchored to the database clock.
	delay := request.NextAttemptAt.Sub(request.Now)
	request.Now = now
	request.NextAttemptAt = now.Add(delay)
	if status != string(TaskLeased) || worker == nil || *worker != request.Lease.WorkerID ||
		token != int64(request.Lease.Token) || expiresAt == nil || !expiresAt.After(request.Now) {
		return "", ErrLeaseLost
	}

	code := request.Failure.Code()
	if request.Failure.Retryability() == Retryable && attempts < maxAttempts {
		command, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status = 'pending', leased_worker = NULL, lease_started_at = NULL,
				lease_expires_at = NULL, next_attempt_at = $4,
				last_error_code = $5, updated_at = $3
			WHERE id = $1 AND status = 'leased' AND leased_worker = $2 AND lease_token = $6
		`, request.Lease.TaskID, request.Lease.WorkerID, request.Now, request.NextAttemptAt,
			string(code), int64(request.Lease.Token))
		if err != nil || command.RowsAffected() != 1 {
			return "", ErrStorage
		}
		return ResolutionRequeued, nil
	}

	if _, ok := r.finalizerFor(request.Lease.Kind); ok {
		return "", errDelegateToFinalizer
	}

	terminalStatus := TaskFailed
	resolution := ResolutionFailed
	if request.Failure.Retryability() == Retryable {
		terminalStatus = TaskDeadLetter
		resolution = ResolutionDeadLetter
	}
	command, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status = $4, leased_worker = NULL, lease_started_at = NULL,
			lease_expires_at = NULL, terminal_worker = $2, next_attempt_at = NULL,
			last_error_code = $5,
			terminal_at = $3, updated_at = $3
		WHERE id = $1 AND status = 'leased' AND leased_worker = $2 AND lease_token = $6
	`, request.Lease.TaskID, request.Lease.WorkerID, request.Now, string(terminalStatus),
		string(code), int64(request.Lease.Token))
	if err != nil || command.RowsAffected() != 1 {
		return "", ErrStorage
	}
	return resolution, nil
}

// ReapExpired reclaims leases whose worker vanished without a heartbeat.
// Its fast path — a single batch transaction, FOR UPDATE SKIP LOCKED,
// generic requeue/dead_letter writes — is exactly what this method has
// always done, unchanged, for every kind with no registered Finalizer.
// A finalizer kind (Finalizer's own doc comment) is excluded from that
// batch entirely and reaped one at a time by reapFinalizerCandidate
// instead, which reads its candidate without a lock first (RFC-001 §8:
// "Reaper сначала читает кандидатов без locks") and only then opens its
// own domain-first transaction — never inside the batch transaction
// above, which would lock the tasks row before any domain lock.
func (r *Recovery) ReapExpired(ctx context.Context) (ReapSummary, error) {
	finalizerKinds := r.finalizerKindNames()

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ReapSummary{}, ErrStorage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
		return ReapSummary{}, ErrStorage
	}
	threshold := databaseNow.Add(-r.policy.ReclaimGrace)

	type expiredTask struct {
		id          uuid.UUID
		kind        Kind
		worker      string
		token       int64
		attempts    int
		maxAttempts int
	}
	rows, err := tx.Query(ctx, `
		SELECT id, kind, leased_worker, lease_token, attempts, max_attempts
		FROM tasks
		WHERE status = 'leased' AND lease_expires_at <= $1 AND NOT (kind = ANY($3::text[]))
		ORDER BY lease_expires_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $2
	`, threshold, r.policy.ReaperBatch, finalizerKinds)
	if err != nil {
		return ReapSummary{}, ErrStorage
	}
	var expired []expiredTask
	for rows.Next() {
		var task expiredTask
		var kindName string
		if err := rows.Scan(&task.id, &kindName, &task.worker, &task.token, &task.attempts, &task.maxAttempts); err != nil {
			rows.Close()
			return ReapSummary{}, ErrStorage
		}
		task.kind = Kind(kindName)
		expired = append(expired, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ReapSummary{}, ErrStorage
	}
	rows.Close()

	var summary ReapSummary
	for _, task := range expired {
		spec, ok := r.registry.Lookup(task.kind)
		if !ok {
			return ReapSummary{}, ErrStorage
		}
		if task.attempts < task.maxAttempts {
			delay, err := r.policy.RetryDelay(spec.RetryBase, task.attempts, r.jitter)
			if err != nil {
				return ReapSummary{}, ErrStorage
			}
			command, err := tx.Exec(ctx, `
				UPDATE tasks
				SET status = 'pending', leased_worker = NULL, lease_started_at = NULL,
					lease_expires_at = NULL, next_attempt_at = $4,
					last_error_code = $5, updated_at = $3
				WHERE id = $1 AND status = 'leased' AND leased_worker = $2 AND lease_token = $6
			`, task.id, task.worker, databaseNow, databaseNow.Add(delay), string(errCodeLeaseExpired), task.token)
			if err != nil || command.RowsAffected() != 1 {
				return ReapSummary{}, ErrStorage
			}
			summary.Requeued++
			continue
		}
		command, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status = 'dead_letter', leased_worker = NULL, lease_started_at = NULL,
				lease_expires_at = NULL, terminal_worker = $2, next_attempt_at = NULL,
				last_error_code = $4,
				terminal_at = $3, updated_at = $3
			WHERE id = $1 AND status = 'leased' AND leased_worker = $2 AND lease_token = $5
		`, task.id, task.worker, databaseNow, string(errCodeLeaseExpired), task.token)
		if err != nil || command.RowsAffected() != 1 {
			return ReapSummary{}, ErrStorage
		}
		summary.DeadLetter++
	}
	if err := tx.Commit(ctx); err != nil {
		return ReapSummary{}, ErrStorage
	}

	if len(finalizerKinds) == 0 {
		return summary, nil
	}
	remaining := r.policy.ReaperBatch - len(expired)
	if remaining <= 0 {
		return summary, nil
	}
	candidates, err := r.finalizerCandidates(ctx, threshold, finalizerKinds, remaining)
	if err != nil {
		return summary, err
	}
	for _, c := range candidates {
		resolution, err := r.reapFinalizerCandidate(ctx, c.id, c.kind, threshold)
		if err != nil {
			return summary, err
		}
		switch resolution {
		case ResolutionRequeued:
			summary.Requeued++
		case ResolutionDeadLetter:
			summary.DeadLetter++
		}
	}
	return summary, nil
}

type reapCandidate struct {
	id   uuid.UUID
	kind Kind
}

// finalizerCandidates reads without any lock — the row lock a finalizer
// kind needs comes only after its own domain locks (reapFinalizerCandidate).
func (r *Recovery) finalizerCandidates(ctx context.Context, threshold time.Time, finalizerKinds []string, limit int) ([]reapCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, kind
		FROM tasks
		WHERE status = 'leased' AND lease_expires_at <= $1 AND kind = ANY($3::text[])
		ORDER BY lease_expires_at, id
		LIMIT $2
	`, threshold, limit, finalizerKinds)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	var candidates []reapCandidate
	for rows.Next() {
		var c reapCandidate
		var kindName string
		if err := rows.Scan(&c.id, &kindName); err != nil {
			return nil, ErrStorage
		}
		c.kind = Kind(kindName)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrStorage
	}
	return candidates, nil
}

// reapFinalizerCandidate re-locks one finalizer-kind candidate (FOR
// UPDATE SKIP LOCKED, so a concurrent reaper or the owning worker's own
// terminal write racing the same row is never blocked on) and either
// requeues it under the same lock (no domain effect involved in a mere
// retry) or, once genuinely exhausted, hands off to its Finalizer —
// which re-acquires everything domain-first, in its own transaction, per
// Finalizer's own doc comment.
func (r *Recovery) reapFinalizerCandidate(ctx context.Context, taskID uuid.UUID, kind Kind, threshold time.Time) (Resolution, error) {
	spec, ok := r.registry.Lookup(kind)
	if !ok {
		return "", ErrStorage
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", ErrStorage
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback(ctx)
		}
	}()

	databaseNow, err := databaseTime(ctx, tx)
	if err != nil {
		return "", err
	}
	var worker string
	var token int64
	var attempts, maxAttempts int
	err = tx.QueryRow(ctx, `
		SELECT leased_worker, lease_token, attempts, max_attempts
		FROM tasks
		WHERE id = $1 AND status = 'leased' AND lease_expires_at <= $2
		FOR UPDATE SKIP LOCKED
	`, taskID, threshold).Scan(&worker, &token, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already reaped, renewed, or terminated by someone else since
		// the unlocked read, or a concurrent reaper holds it right now —
		// none of these are an error.
		return "", nil
	}
	if err != nil {
		return "", ErrStorage
	}

	if attempts < maxAttempts {
		delay, err := r.policy.RetryDelay(spec.RetryBase, attempts, r.jitter)
		if err != nil {
			return "", ErrStorage
		}
		command, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status = 'pending', leased_worker = NULL, lease_started_at = NULL,
				lease_expires_at = NULL, next_attempt_at = $4,
				last_error_code = $5, updated_at = $3
			WHERE id = $1 AND status = 'leased' AND leased_worker = $2 AND lease_token = $6
		`, taskID, worker, databaseNow, databaseNow.Add(delay), string(errCodeLeaseExpired), token)
		if err != nil || command.RowsAffected() != 1 {
			return "", ErrStorage
		}
		if err := tx.Commit(ctx); err != nil {
			return "", ErrStorage
		}
		finished = true
		return ResolutionRequeued, nil
	}

	finalizer, ok := r.finalizerFor(kind)
	if !ok {
		// Registered kinds only ever reach finalizerCandidates via
		// finalizerKindNames, so this should not happen; fail closed
		// rather than silently falling back to a generic dead-letter that
		// would skip the domain effect entirely.
		return "", ErrStorage
	}
	if err := tx.Rollback(ctx); err != nil {
		return "", ErrStorage
	}
	finished = true // released above; the deferred Rollback must not run a second time
	if err := finalizer.FinalizeExpired(ctx, taskID, worker, uint64(token), TaskDeadLetter, errCodeLeaseExpired); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			return "", nil
		}
		return "", err
	}
	return ResolutionDeadLetter, nil
}
