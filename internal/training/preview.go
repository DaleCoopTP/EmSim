package training

import (
	"context"
	"errors"

	"emsim/internal/auth"
	"emsim/internal/content"
	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StartPreview is 112-7/ADR-027's own entry point: an instructor runs
// their own operator-112 scenario version (a draft they own, or any
// approved version) as the run's sole participant, with no workstation.
// It returns the new preview lesson's id and its single item's id — the
// web editor drives that item exactly the way a trainee drives their own
// (GET/POST /items/{id}...), just under an instructor actor.
//
// Unlike CreateLesson+ReplaceAssignments+Start (three separate calls,
// each its own transaction, meant for an instructor building up a real
// group lesson over several requests), this does the equivalent of all
// three atomically in one transaction — a preview has exactly one
// participant and starts immediately, so there is no draft-review step
// worth exposing.
func (s *Service) StartPreview(ctx context.Context, actorID, scenarioID, versionID uuid.UUID) (lessonID, itemID uuid.UUID, err error) {
	actor := auth.Principal{UserID: actorID, Role: auth.RoleInstructor}

	// A previous preview run of this same actor, if still active, is
	// stopped first — in its own transaction, via the same Stop path a
	// real instructor-initiated stop uses (RFC-001 §7.5's barrier
	// applies equally to a preview lesson). This never races a
	// concurrent StartPreview by the same actor in practice (one
	// instructor, one browser tab editing one scenario at a time); if it
	// somehow did, the loser's own runs_active_user_idx insert below
	// would simply fail with ErrConflict.
	var previous Run
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		r, err := s.store.ActiveRunByUser(ctx, tx, actorID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		previous = r
		return err
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if previous.ID != uuid.Nil {
		if _, err := s.Stop(ctx, actor, previous.LessonID, nil, ""); err != nil {
			return uuid.Nil, uuid.Nil, err
		}
	}

	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, err := s.scenarios.ScenarioByID(ctx, tx, scenarioID)
		if err != nil {
			return err
		}
		if sc.CreatedBy != actorID {
			return ErrNotFound
		}
		version, err := s.scenarios.VersionByID(ctx, tx, versionID)
		if err != nil {
			return err
		}
		if version.ScenarioID != scenarioID {
			return ErrNotFound
		}
		if version.Status != "approved" && version.Status != "draft" {
			return validationErr("version_id", "not_previewable")
		}
		if version.Body.ExerciseType != content.ExerciseTypeOperator112Intake || version.Body.Intake112 == nil {
			return validationErr("version_id", "preview_requires_operator112_intake")
		}

		rubricVersion, err := content.RubricVersionFor(content.ExerciseTypeOperator112Intake)
		if err != nil {
			return err
		}
		lesson := Lesson{
			ID: uuid.New(), ExerciseType: content.ExerciseTypeOperator112Intake, InstructorID: actorID,
			Title: "Предпросмотр: " + sc.Title, Mode: ModePreview, Level: auth.LevelEasy,
			State: LessonDraft, Timing: Timing{}, RubricVersion: rubricVersion, RecordingGraceS: 120,
		}
		lesson, err = s.store.InsertLesson(ctx, tx, lesson)
		if err != nil {
			return err
		}

		assignment := Assignment{LessonID: lesson.ID, WorkstationID: uuid.Nil, UserID: actorID, ScenarioVersionIDs: []uuid.UUID{versionID}}
		if err := s.store.ReplaceAssignments(ctx, tx, lesson.ID, []Assignment{assignment}); err != nil {
			return err
		}

		var catalogVersion *int
		if version.Body.Intake112.Mode == "card_only" || version.Body.Intake112.Mode == "full_case" {
			catalog, err := s.scenarios.LatestIntakeCatalog(ctx, tx)
			if err != nil {
				return validationErr("intake_catalog", "catalog is required for card_only/full_case")
			}
			catalogVersion = &catalog.Version
		}

		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}
		lesson, err = s.store.StartLesson(ctx, tx, lesson.ID, now, catalogVersion)
		if err != nil {
			return err
		}

		run := Run{
			ID: uuid.New(), ExerciseType: content.ExerciseTypeOperator112Intake, LessonID: lesson.ID,
			UserID: actorID, WorkstationID: uuid.Nil, WorkstationNo: 0,
			Mode: ModePreview, State: RunActive, LevelAtStart: auth.LevelEasy, QueueCursor: 0, StartedAt: now,
		}
		run, err = s.store.InsertRun(ctx, tx, run)
		if err != nil {
			return err
		}

		newItemID, err := s.offerQueueVersion(ctx, tx, lesson, run, assignment, 0, now, nil)
		if err != nil {
			return err
		}

		lessonID, itemID = lesson.ID, newItemID
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actorID, ActorRole: string(auth.RoleInstructor), Action: "lesson.preview_start",
			ResourceType: "lesson", ResourceID: &lesson.ID, Outcome: audit.OutcomeOK,
		})
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return lessonID, itemID, nil
}
