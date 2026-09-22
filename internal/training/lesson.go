package training

import (
	"time"

	"emsim/internal/auth"
	"emsim/internal/content"

	"github.com/google/uuid"
)

// LessonState is lessons.state.
type LessonState string

const (
	LessonDraft    LessonState = "draft"
	LessonRunning  LessonState = "running"
	LessonStopped  LessonState = "stopped"
	LessonFinished LessonState = "finished"
)

// Lesson is one lessons row. Level reuses auth.Level (the same
// easy/medium/hard vocabulary users.level already carries) rather than
// redeclaring it — the same reasoning domain.go documents for reusing
// content.Reaction/content.Workflow.
type Lesson struct {
	ID              uuid.UUID
	ExerciseType    content.ExerciseType
	InstructorID    uuid.UUID
	Title           string
	Mode            Mode
	Level           auth.Level
	State           LessonState
	Epoch           int64
	Timing          Timing
	RubricVersion   string
	RecordingGraceS int
	CreatedAt       time.Time
	StartedAt       *time.Time
	StoppedAt       *time.Time
	StopReason      *string
	FinishedAt      *time.Time
}

// LessonCreate is CreateLesson's input (openapi.yaml's LessonCreate). A
// nil Timing gets RFC-001 §7.2's defaults (30/30/180, no spawn_every_s).
type LessonCreate struct {
	ExerciseType content.ExerciseType
	Title        string
	Mode         Mode
	Level        auth.Level
	Timing       *Timing
}

// AssignmentInput is what a caller (the HTTP layer, slice 3's C5)
// supplies to ReplaceAssignments — a workstation's number rather than
// its internal id, matching openapi.yaml's Assignment schema and the
// shape an instructor's UI naturally has.
type AssignmentInput struct {
	WorkstationNo      int
	UserID             uuid.UUID
	ScenarioVersionIDs []uuid.UUID
}

// Assignment is one assignments row: an AssignmentInput resolved to
// internal ids, as it is stored and read back.
type Assignment struct {
	LessonID           uuid.UUID
	WorkstationID      uuid.UUID
	WorkstationNo      int
	UserID             uuid.UUID
	ScenarioVersionIDs []uuid.UUID
}

// RunState is runs.state.
type RunState string

const (
	RunActive   RunState = "active"
	RunFinished RunState = "finished"
)

// Run is one runs row.
type Run struct {
	ID            uuid.UUID
	ExerciseType  content.ExerciseType
	LessonID      uuid.UUID
	UserID        uuid.UUID
	WorkstationID uuid.UUID
	WorkstationNo int
	Mode          Mode
	State         RunState
	LevelAtStart  auth.Level
	NextOfferAt   *time.Time
	QueueCursor   int
	StartedAt     time.Time
	FinishedAt    *time.Time
}

// ItemPatch is the full set of items columns Execute may need to change
// for one command attempt. LogSeq always increments, on both an accepted
// and a rejected attempt; Seq/Reaction/State/Card reflect the decision's
// resulting values unconditionally — on a rejected Decision these equal
// the item's own current values (Exercise.Decide's contract), so writing
// them back is a harmless no-op rather than something the caller must
// branch on. OpenedAt/PrimaryAt/CompleteAt/ClosedAt/CloseReason are
// merged with COALESCE by the Store (internal/training/postgres): each
// is a field that transitions from NULL to a value exactly once, so
// "keep the existing value if already set, else take the new one" is
// always correct for them.
type ItemPatch struct {
	LogSeq      int64
	Seq         int64
	Reaction    content.Reaction
	State       ItemState
	Card        content.CardPreview
	IntakeCard  *IntakeCard
	IntakeState *IntakeState
	OpenedAt    *time.Time
	PrimaryAt   *time.Time
	CompleteAt  *time.Time
	ClosedAt    *time.Time
	CloseReason *CloseReason
}
