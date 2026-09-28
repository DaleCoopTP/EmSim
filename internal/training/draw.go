package training

import (
	"context"
	"fmt"
	"regexp"

	"emsim/internal/auth"
	"emsim/internal/content"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Draw bounds (ADR-035): queue length per workstation.
const drawMaxCount = 20

var categoryCodePattern = regexp.MustCompile(`^[0-9]{2}$`)

// DrawRow is one workstation/trainee pair a random queue is drawn for.
type DrawRow struct {
	WorkstationNo int
	UserID        uuid.UUID
}

// DrawInput is DrawAssignments' input (openapi.yaml's AssignmentDrawRequest).
type DrawInput struct {
	Categories []string
	Count      int
	Rows       []DrawRow
}

// NotEnoughScenariosError is the random fill finding fewer suitable
// scenarios for one trainee than the requested queue length.
type NotEnoughScenariosError struct {
	WorkstationNo int
	Available     int
	Requested     int
}

func (e *NotEnoughScenariosError) Error() string {
	return fmt.Sprintf("training: workstation %d: %d suitable scenarios, %d requested", e.WorkstationNo, e.Available, e.Requested)
}

// DrawAssignments proposes a random queue per row for a draft DDS lesson
// (ADR-035): scenarios of the trainee's own service, from the chosen
// classifier sections, in the lesson level's difficulty band, without
// repeats inside a queue and without spawn_card scenarios, in an
// independent order per row. It changes and audits nothing — the
// instructor reviews the proposal and saves it with ReplaceAssignments,
// which applies the very same checks.
func (s *Service) DrawAssignments(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, in DrawInput) ([]Assignment, error) {
	if len(in.Categories) == 0 {
		return nil, validationErr("categories", "at least one category is required")
	}
	wanted := make(map[string]bool, len(in.Categories))
	for _, code := range in.Categories {
		if !categoryCodePattern.MatchString(code) {
			return nil, validationErr("categories", "must be two-digit classifier section codes")
		}
		wanted[code] = true
	}
	if in.Count < 1 || in.Count > drawMaxCount {
		return nil, validationErr("count", "must be between 1 and 20")
	}
	if len(in.Rows) == 0 {
		return nil, validationErr("rows", "at least one row is required")
	}

	var drawn []Assignment
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, err := s.store.LessonByID(ctx, tx, lessonID, LockNone)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID || lesson.Mode == ModePreview {
			return ErrNotFound
		}
		if lesson.State != LessonDraft {
			return ErrConflict
		}
		if lesson.ExerciseType != content.ExerciseTypeDDSProcessing {
			return validationErr("exercise_type", "random fill is available for dds_processing only")
		}
		lo, hi, ok := content.LevelDifficultyBand(string(lesson.Level))
		if !ok {
			return validationErr("level", "unsupported")
		}
		if lesson.Level == auth.LevelHard && in.Count > 1 && (lesson.Timing.SpawnEveryS == nil || *lesson.Timing.SpawnEveryS <= 0) {
			return validationErr("timing.spawn_every_s", "is required for a hard queue with multiple scenarios")
		}

		byService := map[string][]content.ScenarioVersionRecord{}
		workstations := make(map[int]struct{}, len(in.Rows))
		users := make(map[uuid.UUID]struct{}, len(in.Rows))
		for _, row := range in.Rows {
			if _, dup := workstations[row.WorkstationNo]; dup {
				return validationErr("workstation_no", "must be unique within a lesson")
			}
			workstations[row.WorkstationNo] = struct{}{}
			if _, dup := users[row.UserID]; dup {
				return validationErr("user_id", "must be unique within a lesson")
			}
			users[row.UserID] = struct{}{}

			ws, trainee, serviceCode, err := s.loadAssignee(ctx, tx, lesson.ExerciseType, row.WorkstationNo, row.UserID)
			if err != nil {
				return err
			}
			versions, cached := byService[serviceCode]
			if !cached {
				if versions, err = s.scenarios.ListApprovedDDSVersions(ctx, tx, serviceCode); err != nil {
					return err
				}
				byService[serviceCode] = versions
			}
			var pool []uuid.UUID
			for _, v := range versions {
				if !wanted[content.CategoryOf(v.Body)] || v.Difficulty < lo || v.Difficulty > hi || !content.DrawableWithoutSpawn(v.Body) {
					continue
				}
				if s.checkAssignableVersion(v, lesson.ExerciseType, serviceCode) != nil {
					continue
				}
				pool = append(pool, v.ID)
			}
			if len(pool) < in.Count {
				return &NotEnoughScenariosError{WorkstationNo: row.WorkstationNo, Available: len(pool), Requested: in.Count}
			}
			s.shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
			drawn = append(drawn, Assignment{LessonID: lessonID, WorkstationID: ws.ID, WorkstationNo: ws.Number,
				UserID: trainee.ID, ScenarioVersionIDs: append([]uuid.UUID(nil), pool[:in.Count]...)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return drawn, nil
}
