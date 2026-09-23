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
	// Recording conflicts have stable public API codes distinct from
	// generic lesson/assignment conflicts.
	ErrRecordingConflict       = errors.New("recording conflict")
	ErrRecordingDeadlinePassed = errors.New("recording deadline passed")
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
	StartLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, startedAt time.Time, intakeCatalogVersion *int) (Lesson, error)
	// StopLesson transitions running -> stopped under the row lock the
	// caller already holds, setting stopped_at/stop_reason and the new
	// epoch (RFC-001 §7.5's barrier). The WHERE clause's own state='running'
	// guard is defense in depth — the caller has already checked this
	// under LockUpdate — not a substitute for that check.
	StopLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, stoppedAt time.Time, reason *string, epoch int64) (Lesson, error)
	// SetStopCutoffForOpenItems freezes stop_cutoff_log_seq at each open
	// item's current log_seq for every item of lessonID still in
	// offered/opened/in_progress, returning their ids. A second call (e.g.
	// a retried Stop) is a no-op for any item this already touched — the
	// WHERE clause only matches stop_cutoff_log_seq IS NULL.
	SetStopCutoffForOpenItems(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]uuid.UUID, error)

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
	RunsDueForOffer(ctx context.Context, tx pgx.Tx, now time.Time) ([]Run, error)
	SetRunQueueCursor(ctx context.Context, tx pgx.Tx, id uuid.UUID, cursor int) error
	SetRunNextOfferAt(ctx context.Context, tx pgx.Tx, id uuid.UUID, nextOfferAt *time.Time) error
	// ClearNextOfferForActiveRuns is Stop's own barrier stopping a
	// hard-level run's future offer immediately, rather than only once
	// the durable lesson.close task later finishes the run.
	ClearNextOfferForActiveRuns(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) error
	FinishRun(ctx context.Context, tx pgx.Tx, id uuid.UUID, finishedAt time.Time) error
	// FinishLesson marks the running lesson complete at the same server
	// timestamp as its last run/item. Slice 3 has exactly one run, so a
	// successful close always exhausts the whole lesson.
	FinishLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, finishedAt time.Time) error

	InsertItem(ctx context.Context, tx pgx.Tx, it Item) (Item, error)
	ItemByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock Lock) (Item, error)
	ItemsByRun(ctx context.Context, tx pgx.Tx, runID uuid.UUID) ([]Item, error)
	ApplyItemDecision(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, patch ItemPatch) error
	InsertIntakeDispatch(ctx context.Context, tx pgx.Tx, dispatch IntakeDispatch) error
	IntakeDispatchByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (IntakeDispatch, error)
	// LastActionByRun returns the most recent action across every item of
	// runID (by server_at) — the instructor monitor's "last_action"
	// column (ADR-018 §Монитор). ErrNotFound when the run has no actions
	// at all (a freshly offered run before its trainee opens anything).
	LastActionByRun(ctx context.Context, tx pgx.Tx, runID uuid.UUID) (Action, error)

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
	// EvidenceByItem reads a closed item's sealed evidence back, decoded
	// into its typed EvidenceBody, plus the digest stored alongside the
	// canonical bytes — assessment's own EvidenceReader port (slice 6's
	// C6) is satisfied structurally by this same method. ErrNotFound if
	// the item never closed (evidence is only ever written once, at
	// close, and never afterward).
	EvidenceByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (EvidenceBody, [32]byte, error)

	InsertBlob(ctx context.Context, tx pgx.Tx, blob Blob) (Blob, bool, error)
	BlobBySHA256(ctx context.Context, tx pgx.Tx, sha256 [32]byte) (Blob, error)
	BlobByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Blob, error)
	VoiceAssetByKey(ctx context.Context, tx pgx.Tx, scenarioVersionID uuid.UUID, key string) (VoiceAsset, error)
	InsertVoiceAsset(ctx context.Context, tx pgx.Tx, asset VoiceAsset) (VoiceAsset, error)
	InsertCall(ctx context.Context, tx pgx.Tx, call Call) (Call, error)
	CallByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock Lock) (Call, error)
	CallsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]Call, error)
	EndCall(ctx context.Context, tx pgx.Tx, id uuid.UUID, endedAt time.Time, acceptedBy, summary string, recording *RecordingManifest) error
	SetCallRecordingDeadline(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, deadline time.Time) error
	SetCallRecordingReady(ctx context.Context, tx pgx.Tx, id, blobID uuid.UUID, receivedAt time.Time) error

	InsertItemEvent(ctx context.Context, tx pgx.Tx, event ItemEvent) (ItemEvent, error)
	ItemEventByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (ItemEvent, error)
	ItemEventsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]ItemEvent, error)
	ScheduledItemEventsDue(ctx context.Context, tx pgx.Tx, now time.Time) ([]ItemEvent, error)
	DeliverItemEvent(ctx context.Context, tx pgx.Tx, id uuid.UUID, deliveredAt time.Time, late bool) error
	SkipItemEvent(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) error
	// SkipRemainingItemEvents cancels every still-scheduled event of
	// itemID in one statement (RFC-001 §7.4/§7.5: close/stop cancel
	// scheduled events before evidence is assembled). A no-op, not an
	// error, when the item has none.
	SkipRemainingItemEvents(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, reason string) error
	InsertControlReport(ctx context.Context, tx pgx.Tx, report ControlReport) (ControlReport, error)
	ControlReportsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]ControlReport, error)

	// RunningLessonIDs lists every lesson currently in state='running' —
	// an unlocked enumeration read Service.Recover uses to know which
	// lessons to individually lock and recover (RFC-001 §7.2: "в
	// транзакции под барьером занятия").
	RunningLessonIDs(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error)
	// RecoverOpenItems is RFC-001 §7.2's server-restart marker for one
	// lesson's own items: every still-open item (offered/opened/
	// in_progress) of lessonID gets entry appended to its
	// items.interruptions, without touching offered_at/deadlines/due_at.
	// Idempotent on entry.RecoveryID — an item that already carries this
	// recovery_id is left alone, so a retried recovery call cannot
	// append a duplicate marker. The caller is expected to already hold
	// lessonID's own lessons row FOR UPDATE (Service.Recover's own
	// per-lesson barrier) — this method does not itself check or lock
	// the lesson's state. Returns the affected item ids for the
	// caller's audit record.
	RecoverOpenItems(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID, entry Interruption) ([]uuid.UUID, error)

	AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error

	// Now returns PostgreSQL's own clock_timestamp() — the authoritative
	// time source RFC-001 §7.1's server_at and §7.2's offered_at/
	// deadlines are computed from, taken once per command/start and
	// reused for every timestamp that step writes, so they are never
	// subtly inconsistent with one another.
	Now(ctx context.Context, tx pgx.Tx) (time.Time, error)
}
