package assessment

import (
	"context"

	"emsim/internal/content"
	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Store interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
	AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error

	// InsertInput seals one assessment_inputs row (RFC-001 §7.4's "assessment_inputs
	// создан один раз, до любой инференции"). itemID must be unique
	// (assessment_inputs.item_id UNIQUE) — a second call for the same
	// item is a bug, not a race the caller should ever trigger (the
	// coordinator checks InputByItem first).
	InsertInput(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, canonicalBody []byte, digest [32]byte) (uuid.UUID, error)
	// InputByItem returns the item's already-sealed input, if any — the
	// coordinator's own idempotent-retry guard: a crash between
	// InsertInput and PromoteWaitingTx must not seal a second input on
	// the next tick.
	InputByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (id uuid.UUID, body InputBody, found bool, err error)
	// InputByID reads one sealed input back — RecordAuto's own read of
	// the immutable snapshot its evaluator pass runs against.
	InputByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (InputBody, error)

	// InsertAssessment writes one assessments row (auto or expert) —
	// migrations/00009_assessment.sql's own triggers
	// (assessment_revision_guard, assessments_one_auto_idx) are the
	// backstop for "one auto per item"/"no auto after expert"/"stale
	// base_revision"; RecordAuto/CreateExpertRevision still check these
	// under the item lock themselves first, so the trigger firing here
	// is always evidence of a concurrent write this transaction's own
	// lock should have prevented, not an expected outcome to branch on.
	InsertAssessment(ctx context.Context, tx pgx.Tx, a Assessment) (uuid.UUID, error)
	// AutoByItem returns the item's auto assessment, if one already
	// exists — RecordAuto's own idempotent-retry guard (a crash between
	// InsertAssessment and the task's Terminal write must not attempt a
	// second INSERT, which assessments_one_auto_idx would reject anyway,
	// but this lets the handler recognize the retry and skip straight to
	// Terminal instead of surfacing that as an unexpected error).
	AutoByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (Assessment, bool, error)
	// HasExpert reports whether itemID already has an expert assessment —
	// RecordAuto's own pre-check (RFC-001 §7.4: "Worker перед фиксацией
	// проверяет отмену/отсутствие expert; поздняя auto не появляется").
	HasExpert(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (bool, error)
	// FinalByItem returns the item's current final assessment — the
	// latest expert revision if one exists, else the sole auto
	// (item_final_assessment view) — with its full Criteria/
	// RubricEffective, so CreateExpertRevision can both check
	// base_revision and copy forward any criterion the new revision
	// leaves unmentioned (ValidateRevision's own "partial copies from
	// current" rule).
	FinalByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (Assessment, bool, error)
	RevisionsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]Assessment, error)
	ClosedItemsByLesson(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]LessonAssessmentItem, error)

	// LockTraineeState upserts and locks (user_id, exercise_type)'s
	// trainee_assessment_state row, creating it at version=0 if this is
	// the trainee's first-ever assessment of this exercise_type — RFC-001
	// §8's lock order requires this before items, so callers take it
	// first, even though slice 6 never reads Version for anything but
	// BumpTraineeStateVersion's own increment (recommendations are slice
	// 10).
	LockTraineeState(ctx context.Context, tx pgx.Tx, userID uuid.UUID, exerciseType content.ExerciseType) (TraineeAssessmentState, error)
	// BumpTraineeStateVersion increments the already-locked row's version
	// by one — called once per new final revision (RFC-001 §7.4).
	BumpTraineeStateVersion(ctx context.Context, tx pgx.Tx, userID uuid.UUID, exerciseType content.ExerciseType) error

	// InsertTrainingExample records one "AI said / instructor said" pair
	// for a criterion an expert revision actually changed from the auto
	// result — only ever called when an auto assessment exists (RFC-001
	// §7.4: "Пара ИИ/эксперт сохраняется только если auto действительно
	// существовала").
	InsertTrainingExample(ctx context.Context, tx pgx.Tx, assessmentID uuid.UUID, criterionID string, auto, expert CriterionResult) error
}
