// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/postgres/task_queue.go; adapted:
//   - Claim selects across a caller-supplied set of Kinds (a worker pool),
//     not one fixed Kind, and looks up the claimed task's lease duration
//     from the Registry instead of taking it as a request parameter — so
//     claim is a SELECT ... FOR UPDATE SKIP LOCKED followed by an UPDATE
//     inside one transaction, rather than core's single WITH-candidate
//     UPDATE (the row stays locked between the two statements, so the
//     "one current owner" guarantee core's tests check is unchanged).
//   - Terminal writes last_error_code + result instead of a terminal
//     digest (no terminal_digest column); replay is classified by
//     comparing (status, last_error_code) instead of a digest.
//   - Enqueue and Cancel are new (see queue.go): core created a run's
//     tasks inline in application/run and had no cancellation.
package tasks

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the PostgreSQL-backed implementation of the tasks queue: enqueue,
// claim, heartbeat, terminal, and cancel. It consults a Registry for
// per-kind lease duration and attempt budget, so those never need to be
// supplied — or duplicated — by a caller.
type Store struct {
	pool     *pgxpool.Pool
	registry *Registry
}

func NewStore(pool *pgxpool.Pool, registry *Registry) *Store {
	return &Store{pool: pool, registry: registry}
}

// Enqueue inserts a new pending task, or — if DedupKey already exists —
// returns the existing task's id with created=false. This is the same
// idempotent-insert pattern orchestration-core's postgres/run_create.go
// used for its idempotency key: attempt the insert, and on a unique-
// constraint miss look the existing row back up rather than erroring.
func (s *Store) Enqueue(ctx context.Context, request EnqueueRequest) (id uuid.UUID, created bool, err error) {
	if err := request.Validate(); err != nil {
		return uuid.Nil, false, err
	}
	spec, ok := s.registry.Lookup(request.Kind)
	if !ok {
		return uuid.Nil, false, ErrUnknownKind
	}

	dependencyIDs := request.DependencyTaskIDs
	if dependencyIDs == nil {
		dependencyIDs = []uuid.UUID{}
	}
	payload := request.Payload
	if payload == nil {
		payload = []byte(`{}`)
	}

	var insertedID uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO tasks (
			id, priority, dependency_task_ids, kind, scope_type, scope_id,
			dedup_key, payload, status, max_attempts, next_attempt_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10)
		ON CONFLICT (dedup_key) DO NOTHING
		RETURNING id
	`, request.TaskID, spec.Priority, dependencyIDs, string(request.Kind), request.ScopeType, request.ScopeID,
		request.DedupKey, payload, spec.MaxAttempts, request.NextAttemptAt,
	).Scan(&insertedID)
	if err == nil {
		return insertedID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, ErrStorage
	}

	var existingID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM tasks WHERE dedup_key = $1`, request.DedupKey).Scan(&existingID); err != nil {
		return uuid.Nil, false, ErrStorage
	}
	return existingID, false, nil
}

func (s *Store) Claim(ctx context.Context, request ClaimRequest) (Lease, bool, error) {
	if err := request.Validate(); err != nil {
		return Lease{}, false, err
	}
	kindNames := make([]string, len(request.Kinds))
	for i, kind := range request.Kinds {
		kindNames[i] = string(kind)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Lease{}, false, ErrStorage
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var taskID uuid.UUID
	var kindName string
	err = tx.QueryRow(ctx, `
		SELECT id, kind
		FROM tasks
		WHERE kind = ANY($1)
		  AND status = 'pending'
		  AND next_attempt_at <= $2
		ORDER BY priority DESC, next_attempt_at, created_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, kindNames, request.Now).Scan(&taskID, &kindName)
	if errors.Is(err, pgx.ErrNoRows) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, ErrStorage
	}

	spec, ok := s.registry.Lookup(Kind(kindName))
	if !ok {
		// A pool only ever claims kinds it was given by the registry
		// (kinds.go's Registry.Pool), so this row's kind not being
		// registered means the registry and the database have drifted —
		// a storage-layer integrity failure, not a normal empty claim.
		return Lease{}, false, ErrStorage
	}
	expiresAt := request.Now.Add(spec.Lease)

	var lease Lease
	var scopeID *uuid.UUID
	var token int64
	err = tx.QueryRow(ctx, `
		UPDATE tasks
		SET status = 'leased',
			attempts = attempts + 1,
			lease_token = lease_token + 1,
			leased_worker = $2,
			lease_started_at = $3,
			lease_expires_at = $4,
			next_attempt_at = NULL,
			updated_at = $3
		WHERE id = $1
		RETURNING id, kind, scope_type, scope_id, dedup_key, payload, priority,
			attempts, lease_token, lease_started_at, lease_expires_at
	`, taskID, request.WorkerID, request.Now, expiresAt).Scan(
		&lease.TaskID, &kindName, &lease.ScopeType, &scopeID, &lease.DedupKey, &lease.Payload, &lease.Priority,
		&lease.Attempt, &token, &lease.StartedAt, &lease.ExpiresAt,
	)
	if err != nil {
		return Lease{}, false, ErrStorage
	}
	if err := tx.Commit(ctx); err != nil {
		return Lease{}, false, ErrStorage
	}
	lease.Kind = Kind(kindName)
	lease.ScopeID = scopeID
	lease.WorkerID = request.WorkerID
	lease.Token = uint64(token)
	return lease, true, nil
}

func (s *Store) Heartbeat(ctx context.Context, request HeartbeatRequest) (time.Time, error) {
	if err := request.Validate(); err != nil {
		return time.Time{}, err
	}
	newExpiry := request.Now.Add(request.LeaseDuration)
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `
		UPDATE tasks
		SET lease_expires_at = $4, updated_at = $3
		WHERE id = $1
		  AND status = 'leased'
		  AND leased_worker = $2
		  AND lease_token = $5
		  AND lease_expires_at > $3
		  AND $4 > lease_expires_at
		RETURNING lease_expires_at
	`, request.Lease.TaskID, request.Lease.WorkerID, request.Now, newExpiry, int64(request.Lease.Token)).Scan(&expiresAt)
	if err == nil {
		return expiresAt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrStorage
	}
	return time.Time{}, s.classifyHeartbeat(ctx, request, newExpiry)
}

func (s *Store) classifyHeartbeat(ctx context.Context, request HeartbeatRequest, newExpiry time.Time) error {
	var status string
	var worker *string
	var token int64
	var expiry *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT status, leased_worker, lease_token, lease_expires_at
		FROM tasks WHERE id = $1
	`, request.Lease.TaskID).Scan(&status, &worker, &token, &expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return ErrStorage
	}
	if status == string(TaskLeased) && worker != nil && *worker == request.Lease.WorkerID &&
		token == int64(request.Lease.Token) && expiry != nil && expiry.After(request.Now) && !newExpiry.After(*expiry) {
		return ErrInvalidLeaseInterval
	}
	return ErrLeaseLost
}

// Terminal must run inside the caller's own transaction: committing the
// domain effect (writing a scenario version, an evidence row, ...) and the
// task's terminal write together is what makes the effect and the queue
// state agree even if the process crashes in between (docs/architecture/
// 02-core-reuse.md's execution contract).
func (s *Store) Terminal(ctx context.Context, tx pgx.Tx, request TerminalRequest) (TerminalResult, error) {
	if tx == nil {
		return "", ErrInvalidRequest
	}
	if err := request.Validate(); err != nil {
		return "", err
	}
	var code any
	if request.Outcome.Code != "" {
		code = string(request.Outcome.Code)
	}
	var result any
	if request.Outcome.Result != nil {
		result = request.Outcome.Result
	}
	var taskID uuid.UUID
	err := tx.QueryRow(ctx, `
		UPDATE tasks
		SET status = $4,
			leased_worker = NULL,
			lease_started_at = NULL,
			lease_expires_at = NULL,
			terminal_worker = $2,
			next_attempt_at = NULL,
			last_error_code = $6,
			result = $5,
			terminal_at = $3,
			updated_at = $3
		WHERE id = $1
		  AND status = 'leased'
		  AND leased_worker = $2
		  AND lease_token = $7
		  AND lease_expires_at > $3
		RETURNING id
	`, request.Lease.TaskID, request.Lease.WorkerID, request.Now, string(request.Outcome.Status),
		result, code, int64(request.Lease.Token)).Scan(&taskID)
	if err == nil {
		return TerminalApplied, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", ErrStorage
	}
	return s.classifyTerminal(ctx, tx, request)
}

func (s *Store) classifyTerminal(ctx context.Context, tx pgx.Tx, request TerminalRequest) (TerminalResult, error) {
	var status string
	var token int64
	var terminalWorker *string
	var code *string
	err := tx.QueryRow(ctx, `
		SELECT status, lease_token, terminal_worker, last_error_code
		FROM tasks WHERE id = $1
	`, request.Lease.TaskID).Scan(&status, &token, &terminalWorker, &code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", ErrStorage
	}
	if token != int64(request.Lease.Token) || terminalWorker == nil || *terminalWorker != request.Lease.WorkerID {
		return "", ErrLeaseLost
	}
	if status != string(TaskDone) && status != string(TaskFailed) {
		return "", ErrLeaseLost
	}
	if status == string(request.Outcome.Status) && sameCode(request.Outcome.Code, code) {
		return TerminalAlreadyApplied, nil
	}
	return "", &TerminalConflictError{}
}

func sameCode(want ErrorCode, got *string) bool {
	if want == "" {
		return got == nil
	}
	return got != nil && *got == string(want)
}

// Cancel moves a non-terminal task straight to cancelled. A leased task's
// lease fields are cleared along with it (tasks_lease_shape and
// tasks_state_shape both require that for status='cancelled'): its current
// owner has no fencing token to be told "cancelled" with, so it discovers
// the cancellation as ErrLeaseLost on its next heartbeat or terminal write,
// because status is no longer 'leased'.
func (s *Store) Cancel(ctx context.Context, request CancelRequest) (bool, error) {
	if err := request.Validate(); err != nil {
		return false, err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'cancelled',
			leased_worker = NULL,
			lease_started_at = NULL,
			lease_expires_at = NULL,
			next_attempt_at = NULL,
			terminal_at = $2,
			updated_at = $2
		WHERE id = $1
		  AND status IN ('waiting', 'pending', 'leased')
	`, request.TaskID, request.Now)
	if err != nil {
		return false, ErrStorage
	}
	return command.RowsAffected() == 1, nil
}
