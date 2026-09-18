// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/queue/queue.go; adapted: Kind changes from a closed
// dialogue|judge enum to an open, dot-namespaced string validated against
// the pattern in migrations/00001_platform_tasks.sql's tasks_kind_shape
// CHECK (the registry in kinds.go tracks which kinds actually exist).
// Lease drops RunID/RunItemID/DialogueID (no FK to a domain table — see
// docs/technical-discovery.md §3.1/§4) and gains the polymorphic scope
// fields plus Payload/Priority. TerminalOutcome drops the digest (no
// terminal_digest column in the new schema) in favour of a Code +
// arbitrary Result payload, and terminal replay is classified without a
// digest (see pgstore.go, added in the following commit). TaskStatus,
// EnqueueRequest, and CancelRequest are new: core had a closed
// pending/leased/done/failed/dead_letter machine with no cancellation and
// no Enqueue contract (a run's tasks were created inline by
// application/run); this schema adds waiting and cancelled
// (ADR-016 A6/B2) and Enqueue/Cancel are first-class operations.
package tasks

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrLeaseLost            = errors.New("task lease lost")
	ErrInvalidLeaseInterval = errors.New("invalid lease interval")
	ErrTerminalConflict     = errors.New("terminal outcome conflict")
	ErrStorage              = errors.New("task storage failure")
)

// Kind is a dot-namespaced task kind name registered in a Registry, e.g.
// "scenario.generate", "voice.render", "assessment.evaluate".
type Kind string

var kindPattern = regexp.MustCompile(`^[a-z]+(\.[a-z_]+)+$`)

func (k Kind) Valid() bool { return kindPattern.MatchString(string(k)) }

// TaskStatus is the tasks.status state machine from
// migrations/00001_platform_tasks.sql.
type TaskStatus string

const (
	TaskWaiting    TaskStatus = "waiting"
	TaskPending    TaskStatus = "pending"
	TaskLeased     TaskStatus = "leased"
	TaskDone       TaskStatus = "done"
	TaskFailed     TaskStatus = "failed"
	TaskDeadLetter TaskStatus = "dead_letter"
	TaskCancelled  TaskStatus = "cancelled"
)

type TerminalConflictError struct{}

func (*TerminalConflictError) Error() string { return ErrTerminalConflict.Error() }

func (*TerminalConflictError) Unwrap() error { return ErrTerminalConflict }

// ClaimRequest claims one due, pending task whose kind is in Kinds — the
// set of kinds a worker pool handles (kinds.go's Registry.Pool). Unlike
// core, it carries no LeaseDuration: the lease length is per-kind
// (Spec.Lease), looked up by the storage layer once it knows which task it
// claimed.
type ClaimRequest struct {
	Kinds    []Kind
	WorkerID string
	Now      time.Time
}

func (r ClaimRequest) Validate() error {
	if len(r.Kinds) == 0 || !validWorker(r.WorkerID) || r.Now.IsZero() {
		return ErrInvalidRequest
	}
	for _, kind := range r.Kinds {
		if !kind.Valid() {
			return ErrInvalidRequest
		}
	}
	return nil
}

type Lease struct {
	TaskID    uuid.UUID
	Kind      Kind
	ScopeType string
	ScopeID   *uuid.UUID
	DedupKey  string
	Payload   []byte
	Priority  int16
	WorkerID  string
	Token     uint64
	Attempt   int
	StartedAt time.Time
	ExpiresAt time.Time
}

func (l Lease) ValidateIdentity() error {
	if l.TaskID == uuid.Nil || !validWorker(l.WorkerID) || l.Token == 0 || l.Token > math.MaxInt64 {
		return ErrInvalidRequest
	}
	return nil
}

type HeartbeatRequest struct {
	Lease         Lease
	Now           time.Time
	LeaseDuration time.Duration
}

func (r HeartbeatRequest) Validate() error {
	if r.Lease.ValidateIdentity() != nil || r.Now.IsZero() || !validExpiry(r.Now, r.LeaseDuration) {
		return ErrInvalidRequest
	}
	return nil
}

type TerminalOutcome struct {
	Status TaskStatus
	Code   ErrorCode
	Result []byte
}

func Done(result []byte) TerminalOutcome {
	return TerminalOutcome{Status: TaskDone, Result: result}
}

func Failed(code ErrorCode, result []byte) (TerminalOutcome, error) {
	if !code.Valid() {
		return TerminalOutcome{}, ErrInvalidRequest
	}
	return TerminalOutcome{Status: TaskFailed, Code: code, Result: result}, nil
}

func (o TerminalOutcome) Validate() error {
	switch o.Status {
	case TaskDone:
		if o.Code != "" {
			return ErrInvalidRequest
		}
	case TaskFailed:
		if !o.Code.Valid() {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

type TerminalRequest struct {
	Lease   Lease
	Now     time.Time
	Outcome TerminalOutcome
}

func (r TerminalRequest) Validate() error {
	if r.Lease.ValidateIdentity() != nil || r.Now.IsZero() || r.Outcome.Validate() != nil {
		return ErrInvalidRequest
	}
	return nil
}

type TerminalResult string

const (
	TerminalApplied        TerminalResult = "applied"
	TerminalAlreadyApplied TerminalResult = "already_applied"
)

// EnqueueRequest inserts a new task in "pending" status, immediately
// eligible at NextAttemptAt. Priority and MaxAttempts are not caller
// inputs: the storage layer fills them from the task's registered Spec,
// so the registry stays the single source of truth for a kind's budget
// (docs/technical-discovery.md §6).
type EnqueueRequest struct {
	TaskID            uuid.UUID
	Kind              Kind
	ScopeType         string
	ScopeID           *uuid.UUID
	DedupKey          string
	Payload           []byte
	DependencyTaskIDs []uuid.UUID
	NextAttemptAt     time.Time
}

func (r EnqueueRequest) Validate() error {
	if r.TaskID == uuid.Nil || !r.Kind.Valid() || !validScopeType(r.ScopeType) ||
		strings.TrimSpace(r.DedupKey) == "" || r.NextAttemptAt.IsZero() {
		return ErrInvalidRequest
	}
	return nil
}

func validScopeType(value string) bool {
	switch value {
	case "scenario", "item", "lesson", "call", "system", "user":
		return true
	default:
		return false
	}
}

// CancelRequest cancels a task that has not reached a terminal state.
// There is no fencing token here (cancellation is not the current lease
// owner's action): pending/waiting tasks move straight to cancelled;
// leased tasks are marked cancelled and their owner discovers this as
// ErrLeaseLost on its next heartbeat or terminal write.
type CancelRequest struct {
	TaskID uuid.UUID
	Now    time.Time
}

func (r CancelRequest) Validate() error {
	if r.TaskID == uuid.Nil || r.Now.IsZero() {
		return ErrInvalidRequest
	}
	return nil
}

func validWorker(worker string) bool {
	return worker == strings.TrimSpace(worker) && len(worker) > 0 && len(worker) <= 256
}

func validExpiry(now time.Time, duration time.Duration) bool {
	return duration > 0 && now.Add(duration).After(now)
}
