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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const errCodeLeaseExpired ErrorCode = "lease_expired"

// Recovery resolves a task handler's classified failure (retry, fail, or
// dead-letter) and reaps tasks whose lease expired without a heartbeat.
type Recovery struct {
	pool     *pgxpool.Pool
	policy   Policy
	jitter   JitterSource
	registry *Registry
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
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", ErrStorage
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

func (r *Recovery) ReapExpired(ctx context.Context) (ReapSummary, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ReapSummary{}, ErrStorage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
		return ReapSummary{}, ErrStorage
	}

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
		WHERE status = 'leased' AND lease_expires_at <= $1
		ORDER BY lease_expires_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $2
	`, databaseNow.Add(-r.policy.ReclaimGrace), r.policy.ReaperBatch)
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
	return summary, nil
}
