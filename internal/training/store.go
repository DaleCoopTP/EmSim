package training

import (
	"context"
	"errors"
	"fmt"
	"time"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound/ErrStorage/ErrConflict are training's own port-level
// errors (mirroring content/auth's own Store error sentinels) — every
// Store method maps a driver-level failure to one of these so
// service.go never inspects a *pgconn.PgError itself.
var (
	ErrNotFound = errors.New("not found")
	ErrStorage  = errors.New("storage failure")
	// ErrConflict is a row that already exists where the caller expected
	// none — e.g. a second active run for a user/workstation the
	// partial unique indexes (migrations/00006) reject.
	ErrConflict   = errors.New("conflict")
	ErrValidation = errors.New("validation failed")
	// ErrCommandIDConflict is ADR-004's command_id_conflict: the same
	// command_id was already used for a different item, actor or body.
	// The original receipt is never disclosed for this case.
	ErrCommandIDConflict = errors.New("command id conflict")
	// ErrWorkstationMismatch is the trainee's session workstation not
	// matching the run's assigned workstation (slice-planning.md §4).
	ErrWorkstationMismatch = errors.New("workstation mismatch")
)

// ValidationError names the request field ReplaceAssignments/CreateLesson
// rejected and why — the same shape content.ValidationError/
// auth.ValidationError use, so the HTTP layer (slice 3's C5) maps all
// three modules' validation failures the same way.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }
func (e *ValidationError) Unwrap() error { return ErrValidation }

func validationErr(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

// Lock is the row-lock strength a read needs — RFC-001 §7.1/§8's
// explicit lock order (lessons FOR SHARE, or FOR UPDATE when the call
// will also touch runs, -> runs FOR UPDATE -> items FOR UPDATE).
type Lock int

const (
	LockNone Lock = iota
	LockShare
	LockUpdate
)

// Store is training's own PostgreSQL port — lessons/assignments/runs/
// items/actions/evidence (migrations/00006). Every method takes an
// explicit pgx.Tx; the caller owns commit/rollback (CLAUDE.md: "Use
// pgx.Tx for atomic domain and queue operations. The caller owns commit
// and rollback").
type Store interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error

	InsertLesson(ctx context.Context, tx pgx.Tx, l Lesson) (Lesson, error)
	LessonByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock Lock) (Lesson, error)
	ListLessonsByInstructor(ctx context.Context, tx pgx.Tx, instructorID uuid.UUID, state *LessonState) ([]Lesson, error)
	// StartLesson transitions draft -> running, setting started_at, under
	// the row lock the caller already holds (LessonByID with LockUpdate).
	StartLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, startedAt time.Time) (Lesson, error)

	// ReplaceAssignments deletes and re-inserts lessonID's assignments in
	// one statement pair — only ever called on a draft lesson (the
	// application service enforces that), so there is no concurrent
	// reader to race.
	ReplaceAssignments(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID, assignments []Assignment) error
	AssignmentsByLesson(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]Assignment, error)

	InsertRun(ctx context.Context, tx pgx.Tx, r Run) (Run, error)
	RunByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock Lock) (Run, error)
	// ActiveRunByUser returns ErrNotFound when the user has no active
	// run — GET /my/run's "204 no active lesson" case.
	ActiveRunByUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (Run, error)
	RunsByLesson(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]Run, error)
	FinishRun(ctx context.Context, tx pgx.Tx, id uuid.UUID, finishedAt time.Time) error

	InsertItem(ctx context.Context, tx pgx.Tx, it Item) (Item, error)
	ItemByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock Lock) (Item, error)
	ItemsByRun(ctx context.Context, tx pgx.Tx, runID uuid.UUID) ([]Item, error)
	ApplyItemDecision(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, patch ItemPatch) error

	// ActionByCommandID looks a command up by its globally-unique
	// command_id alone (ADR-004 §7.1) — the returned Action's ItemID
	// tells the caller whether it matches the item this request targets
	// (command_id_conflict when it does not).
	ActionByCommandID(ctx context.Context, tx pgx.Tx, commandID uuid.UUID) (Action, error)
	InsertAction(ctx context.Context, tx pgx.Tx, a Action) (Action, error)
	// ActionsByItem returns every action for itemID, sorted by LogSeq
	// ascending — Evidence's own precondition, and the monitor/instructor
	// action feed's natural order (RFC-001 §7.1: "Порядок определяется
	// log_seq").
	ActionsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]Action, error)

	InsertEvidence(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, ev Evidence) error

	AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error

	// Now returns PostgreSQL's own clock_timestamp() — the authoritative
	// time source RFC-001 §7.1's server_at and §7.2's offered_at/
	// deadlines are computed from, taken once per command/start and
	// reused for every timestamp that step writes, so they are never
	// subtly inconsistent with one another.
	Now(ctx context.Context, tx pgx.Tx) (time.Time, error)
}
