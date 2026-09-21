package training

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"emsim/internal/auth"
	"emsim/internal/content"
	"emsim/internal/platform/audit"
	"emsim/internal/platform/realtime"
	"emsim/internal/platform/tasks"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// KindLessonClose is platform/tasks' kind for the durable worker-side
// half of stop (RFC-001 §7.5/ADR-018): closing whatever items a stop
// barrier left open, as interrupted, once the domain transaction that
// set the barrier has committed. Exported so cmd/emsim's composition can
// register its Spec (pool/priority/lease/attempts) and handler under the
// exact same Kind this package enqueues — a mismatch there would leave
// enqueued tasks with no worker ever claiming them.
const KindLessonClose tasks.Kind = "lesson.close"

// allowedFieldCorrectionPath mirrors internal/training/dds's own
// constant — the application service needs it once, at assignment time,
// to reject a scenario whose reference declares a field correction this
// slice cannot execute; duplicating one string constant here is simpler
// and safer than importing dds (which would cycle back to this package).
const allowedFieldCorrectionPath = "/card/address/okrug"

// Service is training's application layer — the "общий исполнитель"
// ADR-015 describes: it owns transactions, locking, replay and
// authorization/ownership checks common to any exercise type, and
// delegates exercise-specific decisions to whichever Exercise
// exerciseTypes maps the lesson's exercise_type to. It never imports
// internal/training/dds directly (that would cycle back here) — the
// caller (cmd/emsim's composition, slice 3's C5, or a test) supplies the
// registry.
type Service struct {
	store         Store
	users         UserDirectory
	workstations  WorkstationDirectory
	scenarios     ScenarioReader
	services      ServiceReader
	tasks         TaskEnqueuer
	exerciseTypes map[content.ExerciseType]Exercise
}

func NewService(store Store, users UserDirectory, workstations WorkstationDirectory, scenarios ScenarioReader, services ServiceReader, taskEnqueuer TaskEnqueuer, exerciseTypes map[content.ExerciseType]Exercise) *Service {
	return &Service{
		store: store, users: users, workstations: workstations,
		scenarios: scenarios, services: services, tasks: taskEnqueuer, exerciseTypes: exerciseTypes,
	}
}

// notify publishes one SSE invalidation from inside the caller's own
// transaction (realtime.NotifyTx — a plain pg_notify call, not a table
// write, so it needs no consumer-owned port; CLAUDE.md's module
// boundary is about who writes which tables). userID/itemID of uuid.Nil
// mean "not scoped to this" (e.g. Stop's own barrier touches no single
// item); lessonID is always set — every training event belongs to
// exactly one lesson.
func (s *Service) notify(ctx context.Context, tx pgx.Tx, lessonID, userID, itemID uuid.UUID) error {
	event := realtime.Event{LessonID: &lessonID}
	if userID != uuid.Nil {
		event.UserID = &userID
	}
	if itemID != uuid.Nil {
		event.ItemID = &itemID
	}
	return realtime.NotifyTx(ctx, tx, event)
}

func (s *Service) exerciseFor(et content.ExerciseType) (Exercise, error) {
	ex, ok := s.exerciseTypes[et]
	if !ok {
		return nil, fmt.Errorf("training: no Exercise registered for exercise_type %q", et)
	}
	return ex, nil
}

// defaultTiming is RFC-001 §7.2's "Единая timing policy ДДС".
func defaultTiming() Timing {
	return Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180}
}

// CreateLesson creates a draft lesson. Hard lessons may opt into a positive
// spawn interval; C5 uses it for parallel offers, while C4's ordinary queue
// still waits for a normal close before issuing the next card.
func (s *Service) CreateLesson(ctx context.Context, actor auth.Principal, in LessonCreate, requestID string) (Lesson, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Lesson{}, validationErr("title", "required")
	}
	if in.Mode != ModeIntro && in.Mode != ModeTraining {
		return Lesson{}, validationErr("mode", "must be intro or training")
	}
	if !in.Level.Valid() {
		return Lesson{}, validationErr("level", "must be easy, medium or hard")
	}
	if in.ExerciseType != content.ExerciseTypeDDSProcessing {
		return Lesson{}, validationErr("exercise_type", "unsupported")
	}

	timing := defaultTiming()
	if in.Timing != nil {
		timing = *in.Timing
		if timing.OpenS <= 0 || timing.PrimaryS <= 0 || timing.CompleteS <= 0 {
			return Lesson{}, validationErr("timing", "open_s/primary_s/complete_s must be positive")
		}
		if timing.SpawnEveryS != nil {
			if in.Level != auth.LevelHard {
				return Lesson{}, validationErr("timing.spawn_every_s", "is allowed only for hard lessons")
			}
			if *timing.SpawnEveryS <= 0 {
				return Lesson{}, validationErr("timing.spawn_every_s", "must be positive")
			}
		}
	}

	rubricVersion, err := content.RubricVersion()
	if err != nil {
		return Lesson{}, fmt.Errorf("training: read rubric version: %w", err)
	}

	lesson := Lesson{
		ID:              uuid.New(),
		ExerciseType:    in.ExerciseType,
		InstructorID:    actor.UserID,
		Title:           title,
		Mode:            in.Mode,
		Level:           in.Level,
		State:           LessonDraft,
		Timing:          timing,
		RubricVersion:   rubricVersion,
		RecordingGraceS: 120,
	}

	var created Lesson
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = s.store.InsertLesson(ctx, tx, lesson)
		if err != nil {
			return err
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "lesson.create",
			ResourceType: "lesson", ResourceID: &created.ID, Outcome: audit.OutcomeOK, RequestID: requestID,
		})
	})
	if err != nil {
		return Lesson{}, err
	}
	return created, nil
}

// ReplaceAssignments replaces the full group plan while a lesson remains a
// draft. Each workstation/user pair owns one ordered nonempty queue; a
// user and a workstation may each occur only once in the plan.
func (s *Service) ReplaceAssignments(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, inputs []AssignmentInput, requestID string) (Lesson, error) {
	if len(inputs) == 0 {
		return Lesson{}, validationErr("assignments", "at least one assignment is required")
	}

	var lesson Lesson
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		lesson, err = s.store.LessonByID(ctx, tx, lessonID, LockUpdate)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID {
			return ErrNotFound
		}
		if lesson.State != LessonDraft {
			return ErrConflict
		}

		assignments := make([]Assignment, 0, len(inputs))
		workstations := make(map[int]struct{}, len(inputs))
		users := make(map[uuid.UUID]struct{}, len(inputs))
		for _, in := range inputs {
			if _, exists := workstations[in.WorkstationNo]; exists {
				return validationErr("workstation_no", "must be unique within a lesson")
			}
			workstations[in.WorkstationNo] = struct{}{}
			if _, exists := users[in.UserID]; exists {
				return validationErr("user_id", "must be unique within a lesson")
			}
			users[in.UserID] = struct{}{}
			if len(in.ScenarioVersionIDs) == 0 {
				return validationErr("scenario_version_ids", "at least one scenario version is required")
			}
			if lesson.Level == auth.LevelHard && len(in.ScenarioVersionIDs) > 1 &&
				(lesson.Timing.SpawnEveryS == nil || *lesson.Timing.SpawnEveryS <= 0) {
				return validationErr("timing.spawn_every_s", "is required for a hard queue with multiple scenarios")
			}

			ws, err := s.workstations.WorkstationByNumber(ctx, tx, in.WorkstationNo)
			if errors.Is(err, auth.ErrNotFound) {
				return validationErr("workstation_no", "unknown")
			} else if err != nil {
				return err
			}
			if !ws.Active {
				return validationErr("workstation_no", "inactive")
			}
			trainee, err := s.users.UserByID(ctx, tx, in.UserID)
			if errors.Is(err, auth.ErrNotFound) {
				return validationErr("user_id", "unknown")
			} else if err != nil {
				return err
			}
			if trainee.Role != auth.RoleTrainee {
				return validationErr("user_id", "must be a trainee")
			}
			if !trainee.Active {
				return validationErr("user_id", "inactive")
			}
			if trainee.ServiceCode == nil {
				return validationErr("user_id", "trainee has no service_code")
			}
			for _, versionID := range in.ScenarioVersionIDs {
				version, err := s.scenarios.VersionByID(ctx, tx, versionID)
				if errors.Is(err, content.ErrNotFound) {
					return validationErr("scenario_version_ids", "unknown")
				} else if err != nil {
					return err
				}
				if err := s.checkAssignableVersion(version, lesson.ExerciseType, *trainee.ServiceCode); err != nil {
					return err
				}
			}
			if err := s.checkSpawnQueuePlan(ctx, tx, in.ScenarioVersionIDs); err != nil {
				return err
			}
			assignments = append(assignments, Assignment{LessonID: lessonID, WorkstationID: ws.ID, WorkstationNo: ws.Number,
				UserID: trainee.ID, ScenarioVersionIDs: append([]uuid.UUID(nil), in.ScenarioVersionIDs...)})
		}
		if err := s.store.ReplaceAssignments(ctx, tx, lessonID, assignments); err != nil {
			return err
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "lesson.assign",
			ResourceType: "lesson", ResourceID: &lessonID, Outcome: audit.OutcomeOK, RequestID: requestID,
		})
	})
	if err != nil {
		return Lesson{}, err
	}
	return lesson, nil
}

// checkAssignableVersion is the compatibility check both
// ReplaceAssignments and Start run (slice-planning.md §4: "на назначении
// и старте проверяется совпадение exercise_type сценария и занятия") —
// approved status, matching exercise_type, the trainee's service_code
// matching the scenario's target_service, and none of the not-yet-
// supported content this slice cannot execute (events, a required call,
// or a field correction outside the one path ADR-017 allows).
func (s *Service) checkAssignableVersion(version content.ScenarioVersionRecord, lessonExerciseType content.ExerciseType, traineeServiceCode string) error {
	if version.Status != "approved" {
		return validationErr("scenario_version_ids", "not approved")
	}
	if version.Body.ExerciseType != lessonExerciseType {
		return validationErr("scenario_version_ids", "exercise_type does not match the lesson")
	}
	if version.Body.TargetService != traineeServiceCode {
		return validationErr("user_id", "service_code does not match the scenario's target_service")
	}
	if version.Body.Reference.Call.Required {
		return validationErr("scenario_version_ids", "a required call is not supported until slice 5")
	}
	for _, fc := range version.Body.Reference.FieldCorrections {
		if fc.Path != allowedFieldCorrectionPath {
			return validationErr("scenario_version_ids", "an unsupported field correction path is referenced")
		}
	}
	return nil
}

// Start transitions a draft lesson to running, creating one run and its
// first item per assignment (slice 3: exactly one). A repeat Start on an
// already-running lesson is idempotent (returns the same Lesson, creates
// nothing); Start on a stopped or finished lesson is a conflict.
func (s *Service) Start(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, requestID string) (Lesson, error) {
	var result Lesson
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, err := s.store.LessonByID(ctx, tx, lessonID, LockUpdate)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID {
			return ErrNotFound
		}
		switch lesson.State {
		case LessonRunning:
			result = lesson
			return nil
		case LessonStopped, LessonFinished:
			return ErrConflict
		}

		// Validated now, before creating anything, rather than only
		// discovered later at the first Execute call on this lesson.
		if _, err := s.exerciseFor(lesson.ExerciseType); err != nil {
			return err
		}

		assignments, err := s.store.AssignmentsByLesson(ctx, tx, lessonID)
		if err != nil {
			return err
		}
		if len(assignments) == 0 {
			return validationErr("assignments", "at least one assignment is required before start")
		}

		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}

		for _, a := range assignments {
			if err := s.startOneAssignment(ctx, tx, lesson, a, now); err != nil {
				return err
			}
		}

		result, err = s.store.StartLesson(ctx, tx, lessonID, now)
		return err
	})
	if err != nil {
		return Lesson{}, err
	}
	return result, nil
}

// Stop is RFC-001 §7.5's barrier, under the same lessons FOR UPDATE lock
// Start uses: it freezes stopped_at/epoch and every open item's
// stop_cutoff_log_seq, cancels their remaining scheduled events, and
// atomically enqueues the durable worker-side close (KindLessonClose,
// closeStoppedLessonTx) that actually interrupts them and finishes their
// runs/lesson — this transaction never touches items beyond the cutoff
// column, so it stays a short, low-contention barrier rather than a bulk
// close. A repeat call on an already-stopped/finished lesson returns the
// current row unchanged: no new epoch, no new task (ADR-018: "Повторный
// stop ничего не меняет").
func (s *Service) Stop(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, reason *string, requestID string) (Lesson, error) {
	var result Lesson
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, err := s.store.LessonByID(ctx, tx, lessonID, LockUpdate)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID {
			return ErrNotFound
		}
		switch lesson.State {
		case LessonStopped, LessonFinished:
			result = lesson
			return nil
		case LessonDraft:
			return ErrConflict
		}

		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}
		newEpoch := lesson.Epoch + 1

		stopped, err := s.store.StopLesson(ctx, tx, lessonID, now, reason, newEpoch)
		if err != nil {
			return err
		}

		affected, err := s.store.SetStopCutoffForOpenItems(ctx, tx, lessonID)
		if err != nil {
			return err
		}
		for _, itemID := range affected {
			if err := s.store.SkipRemainingItemEvents(ctx, tx, itemID, SkipReasonLessonStopped); err != nil {
				return err
			}
		}
		// A hard-level run's own next_offer_at must stop being
		// actionable at the same barrier as everything else Stop
		// freezes — otherwise tickHardRun's own every-500ms poll keeps
		// re-selecting it (RunsDueForOffer only filters on the run's
		// own state, not its lesson's) until the durable lesson.close
		// task later finishes the run, which may be delayed or, if the
		// worker is unavailable, may never happen at all.
		if err := s.store.ClearNextOfferForActiveRuns(ctx, tx, lessonID); err != nil {
			return err
		}

		if err := s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "lesson.stop",
			ResourceType: "lesson", ResourceID: &lessonID, Outcome: audit.OutcomeOK, RequestID: requestID,
			Details: map[string]any{"epoch": newEpoch, "open_item_count": len(affected)},
		}); err != nil {
			return err
		}

		dedupKey := fmt.Sprintf("lesson.close:%s:%d", lessonID, newEpoch)
		if _, _, err := s.tasks.EnqueueTx(ctx, tx, tasks.EnqueueRequest{
			TaskID: uuid.New(), Kind: KindLessonClose, ScopeType: "lesson", ScopeID: &lessonID,
			DedupKey: dedupKey, NextAttemptAt: now,
		}); err != nil {
			return err
		}
		// The lesson-scoped notify (no user_id) is what the
		// instructor's own /lessons/{id}/stream matches on
		// (streamLesson filters by e.LessonID). /my/stream filters by
		// e.UserID instead (streamMy: "a trainee only ever has one
		// active run, but that run's lesson id is not known up front"),
		// so a userID=nil notify never reaches a trainee at all — before
		// this fix, a trainee only learned their lesson had stopped once
		// the durable lesson.close task actually interrupted their open
		// item (or, with no open item, only via the next unrelated
		// refetch). Notify every affected run's own trainee too, so
		// every open workplace refreshes and shows the stop immediately.
		runs, err := s.store.RunsByLesson(ctx, tx, lessonID)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run.State != RunActive {
				continue
			}
			if err := s.notify(ctx, tx, lessonID, run.UserID, uuid.Nil); err != nil {
				return err
			}
		}
		if err := s.notify(ctx, tx, lessonID, uuid.Nil, uuid.Nil); err != nil {
			return err
		}

		result = stopped
		return nil
	})
	if err != nil {
		return Lesson{}, err
	}
	return result, nil
}

// CloseStoppedLesson is KindLessonClose's domain half (RFC-001 §7.5): it
// interrupts every still-open item of a stopped lesson (closed_at =
// lessons.stopped_at, close_reason=interrupted), seals evidence for each
// at its own frozen stop_cutoff_log_seq, finishes each run once all its
// items are terminal, and finishes the lesson once all its runs are.
// Idempotent by construction — every write is either a conditional
// UPDATE (ApplyItemDecision's own closed_at/close_reason COALESCE) or
// guarded by the item/run/lesson's own current state, so a retried
// attempt (this task's own at-least-once delivery, or a worker crash
// mid-way through a prior attempt) safely picks up wherever the last one
// left off. The caller (cmd/emsim's lessonCloseHandler) owns the
// transaction so it can commit tasks.Terminal atomically with this —
// CLAUDE.md: "domain effect and tasks.done fixed as one transaction."
func (s *Service) CloseStoppedLesson(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) error {
	lesson, err := s.store.LessonByID(ctx, tx, lessonID, LockUpdate)
	if err != nil {
		return err
	}
	if lesson.State != LessonStopped {
		// Already finished by an earlier attempt at this same task (or a
		// lesson that somehow never reached stopped) — nothing left to do.
		return nil
	}
	if lesson.StoppedAt == nil {
		return fmt.Errorf("training: lesson %s is stopped with no stopped_at", lessonID)
	}
	exercise, err := s.exerciseFor(lesson.ExerciseType)
	if err != nil {
		return err
	}
	stoppedAt := *lesson.StoppedAt

	runs, err := s.store.RunsByLesson(ctx, tx, lessonID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.State == RunFinished {
			continue
		}
		items, err := s.store.ItemsByRun(ctx, tx, run.ID)
		if err != nil {
			return err
		}
		for _, peek := range items {
			if peek.State == ItemClosed || peek.State == ItemInterrupted {
				continue
			}
			item, err := s.store.ItemByID(ctx, tx, peek.ID, LockUpdate)
			if err != nil {
				return err
			}
			if item.State == ItemClosed || item.State == ItemInterrupted {
				continue
			}
			if err := s.closeInterruptedItem(ctx, tx, exercise, item, stoppedAt, lesson.RecordingGraceS); err != nil {
				return err
			}
		}
		items, err = s.store.ItemsByRun(ctx, tx, run.ID)
		if err != nil {
			return err
		}
		runFinished := true
		for _, it := range items {
			if it.State != ItemClosed && it.State != ItemInterrupted {
				runFinished = false
				break
			}
		}
		if runFinished {
			if err := s.store.FinishRun(ctx, tx, run.ID, stoppedAt); err != nil {
				return err
			}
		}
	}

	runs, err = s.store.RunsByLesson(ctx, tx, lessonID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.State != RunFinished {
			// A run this attempt could not finish (should not happen —
			// every item was just closed above — but staying conservative
			// costs nothing and a later retry will pick it up).
			return nil
		}
	}
	return s.store.FinishLesson(ctx, tx, lessonID, stoppedAt)
}

// closeInterruptedItem seals one stop-interrupted item: closed_at is the
// lesson's own stopped_at (not clock_timestamp() — the worker's actual
// delay must not count against the trainee, RFC-001 §7.5), the cutoff is
// the log_seq Stop already froze (falling back to the item's current
// log_seq only if that never got set — defensive, should not happen for
// an item Stop's own SetStopCutoffForOpenItems already touched), and no
// new action row is written: this is a server-side interruption, not a
// client command.
func (s *Service) closeInterruptedItem(ctx context.Context, tx pgx.Tx, exercise Exercise, item Item, stoppedAt time.Time, recordingGraceS int) error {
	cutoff := item.LogSeq
	if item.StopCutoffLogSeq != nil {
		cutoff = *item.StopCutoffLogSeq
	}
	closeReason := CloseInterrupted
	patch := ItemPatch{
		LogSeq: item.LogSeq, Seq: item.Seq, Reaction: item.Reaction, State: ItemInterrupted, Card: item.Card,
		ClosedAt: &stoppedAt, CloseReason: &closeReason,
	}
	if err := s.store.ApplyItemDecision(ctx, tx, item.ID, patch); err != nil {
		return err
	}
	if err := s.store.SkipRemainingItemEvents(ctx, tx, item.ID, SkipReasonLessonStopped); err != nil {
		return err
	}
	if err := s.store.SetCallRecordingDeadline(ctx, tx, item.ID, stoppedAt.Add(time.Duration(recordingGraceS)*time.Second)); err != nil {
		return err
	}

	actions, err := s.store.ActionsByItem(ctx, tx, item.ID)
	if err != nil {
		return err
	}
	events, err := s.store.ItemEventsByItem(ctx, tx, item.ID)
	if err != nil {
		return err
	}

	closedItem := item
	closedItem.State = ItemInterrupted
	closedItem.ClosedAt = &stoppedAt
	closedItem.CloseReason = &closeReason
	calls, err := s.store.CallsByItem(ctx, tx, item.ID)
	if err != nil {
		return err
	}
	closedItem.Calls = calls

	evidence, err := exercise.Evidence(closedItem, actions, events, cutoff, stoppedAt)
	if err != nil {
		return err
	}
	if err := s.store.InsertEvidence(ctx, tx, item.ID, evidence); err != nil {
		return err
	}
	return s.notify(ctx, tx, item.LessonID, item.UserID, item.ID)
}

func (s *Service) startOneAssignment(ctx context.Context, tx pgx.Tx, lesson Lesson, a Assignment, now time.Time) error {
	trainee, err := s.users.UserByID(ctx, tx, a.UserID)
	if err != nil {
		return err
	}
	if !trainee.Active {
		return validationErr("user_id", "inactive")
	}
	ws, err := s.workstations.WorkstationByID(ctx, tx, a.WorkstationID)
	if err != nil {
		return err
	}
	if !ws.Active {
		return validationErr("workstation_no", "inactive")
	}
	if trainee.ServiceCode == nil {
		return validationErr("user_id", "trainee has no service_code")
	}

	for _, versionID := range a.ScenarioVersionIDs {
		version, err := s.scenarios.VersionByID(ctx, tx, versionID)
		if err != nil {
			return err
		}
		if err := s.checkAssignableVersion(version, lesson.ExerciseType, *trainee.ServiceCode); err != nil {
			return err
		}
	}

	if _, err := s.store.ActiveRunByUser(ctx, tx, trainee.ID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	run := Run{
		ID: uuid.New(), ExerciseType: lesson.ExerciseType, LessonID: lesson.ID,
		UserID: trainee.ID, WorkstationID: ws.ID, WorkstationNo: ws.Number,
		Mode: lesson.Mode, State: RunActive, LevelAtStart: lesson.Level,
		QueueCursor: 0, StartedAt: now,
	}
	if lesson.Level == auth.LevelHard && lesson.Timing.SpawnEveryS != nil {
		nextOfferAt := now.Add(time.Duration(*lesson.Timing.SpawnEveryS) * time.Second)
		run.NextOfferAt = &nextOfferAt
	}
	run, err = s.store.InsertRun(ctx, tx, run)
	if err != nil {
		return err
	}

	return s.offerQueueVersion(ctx, tx, lesson, run, a, 0, now, nil)
}

// offerQueueVersion snapshots the next approved scenario into an offered
// item. The caller owns the run lock (or has just inserted the run), so the
// ordinal and queue cursor cannot race another normal-close offer.
// spawnOrigin carries where a spawn_card-created item came from
// (ADR-018: "variation добавляется к описанию экземпляра новой
// карточки") — nil for every ordinary queue offer (start, a normal
// close, a hard-scheduler tick), non-nil only from spawnEventCard.
type spawnOrigin struct {
	itemID    uuid.UUID
	variation string
}

func (s *Service) offerQueueVersion(ctx context.Context, tx pgx.Tx, lesson Lesson, run Run, assignment Assignment, queueIndex int, now time.Time, origin *spawnOrigin) error {
	if queueIndex < 0 || queueIndex >= len(assignment.ScenarioVersionIDs) {
		return fmt.Errorf("training: queue index %d out of range", queueIndex)
	}
	versionID := assignment.ScenarioVersionIDs[queueIndex]
	version, err := s.scenarios.VersionByID(ctx, tx, versionID)
	if err != nil {
		return err
	}
	svc, err := s.services.ServiceByCode(ctx, tx, version.Body.TargetService)
	if err != nil {
		return err
	}
	openAt := now.Add(time.Duration(lesson.Timing.OpenS) * time.Second)
	card := content.ProjectCard(version.Body.Card)
	var spawnedFrom *uuid.UUID
	if origin != nil {
		spawnedFrom = &origin.itemID
		if origin.variation != "" {
			card.Incident.Description = appendVariation(card.Incident.Description, origin.variation)
		}
	}
	item := Item{
		ID: uuid.New(), RunID: run.ID, LessonID: lesson.ID, UserID: run.UserID,
		WorkstationNo: run.WorkstationNo, ScenarioVersionID: versionID,
		ScenarioDigest: fmt.Sprintf("%x", version.Digest), TargetService: version.Body.TargetService,
		Ordinal: queueIndex + 1, SpawnedFrom: spawnedFrom, State: ItemOffered, Reaction: content.ReactionAdded,
		Card: card, Workflow: svc.Workflow,
		PilotGoal: version.Body.Reference.PilotGoal, Mode: lesson.Mode,
		TimingEffective: lesson.Timing,
		Deadlines:       Deadlines{OpenAt: openAt, PrimaryAt: openAt},
		OfferedAt:       now,
	}
	if _, err := s.store.InsertItem(ctx, tx, item); err != nil {
		return err
	}
	if err := s.store.SetRunQueueCursor(ctx, tx, run.ID, queueIndex+1); err != nil {
		return err
	}
	if err := s.scheduleEventsForAnchor(ctx, tx, item, version.Body.Events, "offered", now); err != nil {
		return err
	}
	return s.notify(ctx, tx, lesson.ID, run.UserID, item.ID)
}

// checkSpawnQueuePlan makes event-driven cards deterministic: the next queue
// slot is the only card an event may consume. The stable source key is
// resolved inside the same transaction, never persisted as an installation-
// specific UUID in seed content.
func (s *Service) checkSpawnQueuePlan(ctx context.Context, tx pgx.Tx, queue []uuid.UUID) error {
	for index, versionID := range queue {
		version, err := s.scenarios.VersionByID(ctx, tx, versionID)
		if err != nil {
			return err
		}
		spawnCount := 0
		for _, event := range version.Body.Events {
			if event.Delivery != "spawn_card" {
				continue
			}
			spawnCount++
			if spawnCount > 1 {
				return validationErr("scenario_version_ids", "a queue scenario may have at most one spawn_card event")
			}
			if index+1 >= len(queue) {
				return validationErr("scenario_version_ids", "spawn_card has no next queue scenario")
			}
			want := queue[index+1]
			if event.Spawn == nil {
				return validationErr("scenario_version_ids", "spawn_card is missing its target")
			}
			switch event.Spawn.Kind {
			case "duplicate":
				if want != versionID {
					return validationErr("scenario_version_ids", "duplicate spawn_card must consume the same next version")
				}
			case "scenario":
				scenario, err := s.scenarios.ScenarioByKey(ctx, tx, event.Spawn.ScenarioKey)
				if err != nil {
					return validationErr("scenario_version_ids", "spawn_card target is unknown")
				}
				target, err := s.scenarios.VersionByNumber(ctx, tx, scenario.ID, event.Spawn.Version)
				if err != nil {
					return validationErr("scenario_version_ids", "spawn_card target is unknown")
				}
				if target.ID != want {
					return validationErr("scenario_version_ids", "spawn_card target must equal the next queue version")
				}
			default:
				return validationErr("scenario_version_ids", "spawn_card kind is unsupported")
			}
		}
	}
	return nil
}

func (s *Service) scheduleEventsForAnchor(ctx context.Context, tx pgx.Tx, item Item, events []content.Event, anchor string, anchorAt time.Time) error {
	existing, err := s.store.ItemEventsByItem(ctx, tx, item.ID)
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(existing))
	for _, event := range existing {
		known[event.EventKey] = struct{}{}
	}
	for _, event := range events {
		if event.Since != anchor {
			continue
		}
		if _, exists := known[event.Key]; exists {
			continue
		}
		dueAt := anchorAt.Add(time.Duration(event.AtS) * time.Second)
		if _, err := s.store.InsertItemEvent(ctx, tx, ItemEvent{ID: uuid.New(), ItemID: item.ID, EventKey: event.Key,
			AnchorAt: anchorAt, DueAt: dueAt, State: EventScheduled}); err != nil {
			return err
		}
	}
	return nil
}

// Execute is POST /items/{itemId}/actions (ADR-004 §7.1). cmd.Payload
// must already be extracted as raw JSON and cmd.CommandID/Type/
// ExpectedSeq already parsed by the caller (a structurally malformed
// request body never reaches here — that is a plain 400 the HTTP layer,
// slice 3's C5, returns before opening any transaction and before any
// journal entry is written).
func (s *Service) Execute(ctx context.Context, actor auth.Principal, itemID uuid.UUID, cmd Command, requestID string) (Receipt, error) {
	if cmd.CommandID == uuid.Nil {
		return Receipt{}, fmt.Errorf("training: command_id is required")
	}

	var receipt Receipt
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, run, item, err := s.lockForCommand(ctx, tx, itemID, cmd.Type)
		if err != nil {
			return err
		}
		if run.UserID != actor.UserID {
			return ErrNotFound
		}
		if actor.WorkstationID == nil || *actor.WorkstationID != run.WorkstationID {
			return ErrWorkstationMismatch
		}

		if existing, err := s.store.ActionByCommandID(ctx, tx, cmd.CommandID); err == nil {
			r, err := replayReceipt(actor.UserID, itemID, cmd, existing)
			if err != nil {
				return err
			}
			receipt = r
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}

		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}

		// control_report is not an exercise rule (ADR-015): RFC-001 §7.5
		// runs it through a dedicated branch that is allowed only after
		// the item has actually closed — the exact opposite of every
		// other command's item_closed rejection — and that never touches
		// reaction/state/card/evidence, so it is handled entirely here
		// rather than falling into recordDecision's close/event-
		// scheduling logic (which assumes an in-progress item).
		if cmd.Type == CommandControlReport {
			receipt, err = s.recordControlReport(ctx, tx, actor, item, cmd, now, requestID)
			return err
		}

		exercise, err := s.exerciseFor(lesson.ExerciseType)
		if err != nil {
			return err
		}

		var decision Decision
		switch {
		case lesson.State != LessonRunning:
			decision = unchangedDecision(item, RejectLessonStopped)
		case item.State == ItemClosed || item.State == ItemInterrupted:
			decision = unchangedDecision(item, RejectItemClosed)
		case cmd.ExpectedSeq != item.Seq:
			decision = unchangedDecision(item, RejectStaleSeq)
		default:
			calls, err := s.store.CallsByItem(ctx, tx, item.ID)
			if err != nil {
				return err
			}
			item.Calls = calls
			version, err := s.scenarios.VersionByID(ctx, tx, item.ScenarioVersionID)
			if err != nil {
				return err
			}
			item.Contacts, item.CallPolicy = version.Body.Contacts, version.Body.Reference.Call
			decision, err = exercise.Decide(item, cmd, now)
			if err != nil {
				return err
			}
		}

		receipt, err = s.recordDecision(ctx, tx, lesson, exercise, actor, item, run, cmd, decision, now, requestID)
		return err
	})
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// unchangedDecision is the shape of the three generic rejections the
// application service itself decides, before ever calling
// Exercise.Decide — lesson_stopped/item_closed/stale_seq apply to any
// exercise type (ADR-015), so they are not this exercise's rule to make.
func unchangedDecision(item Item, rejection Rejection) Decision {
	return Decision{Accepted: false, Rejection: rejection, Reaction: item.Reaction, State: item.State, Card: item.Card}
}

// lockForCommand acquires RFC-001 §8's lock order — lessons (FOR SHARE,
// or FOR UPDATE when close will also finish the run) -> runs (FOR
// UPDATE, only for close) -> items (FOR UPDATE, always) — via two
// unlocked peeks first: items.run_id and runs.lesson_id/user_id/
// workstation_id never change after insert, so reading them without a
// lock is safe for discovering *which* rows to lock in the correct
// order, and ownership can be (and is, by the caller) checked from that
// peek immediately, before any lock is even acquired.
func (s *Service) lockForCommand(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, cmdType CommandType) (Lesson, Run, Item, error) {
	peekItem, err := s.store.ItemByID(ctx, tx, itemID, LockNone)
	if err != nil {
		return Lesson{}, Run{}, Item{}, err
	}
	peekRun, err := s.store.RunByID(ctx, tx, peekItem.RunID, LockNone)
	if err != nil {
		return Lesson{}, Run{}, Item{}, err
	}

	lessonLock := LockShare
	run := peekRun
	if cmdType == CommandClose {
		lessonLock = LockUpdate
	}
	lesson, err := s.store.LessonByID(ctx, tx, peekRun.LessonID, lessonLock)
	if err != nil {
		return Lesson{}, Run{}, Item{}, err
	}
	if cmdType == CommandClose {
		run, err = s.store.RunByID(ctx, tx, peekRun.ID, LockUpdate)
		if err != nil {
			return Lesson{}, Run{}, Item{}, err
		}
	}
	item, err := s.store.ItemByID(ctx, tx, itemID, LockUpdate)
	if err != nil {
		return Lesson{}, Run{}, Item{}, err
	}
	return lesson, run, item, nil
}

// replayReceipt is ADR-004 §7.1's replay branch: the original outcome,
// HTTP status and receipt are reused unchanged (only Replayed flips to
// true); a different actor/item/body under the same command_id is
// ErrCommandIDConflict, and the original receipt is never disclosed for
// that case.
func replayReceipt(actorID, itemID uuid.UUID, cmd Command, existing Action) (Receipt, error) {
	digest, err := RequestDigest(actorID, itemID, cmd.Type, cmd.Payload, cmd.ExpectedSeq, cmd.ClientAt)
	if err != nil {
		return Receipt{}, err
	}
	if existing.ItemID != itemID || existing.ActorID != actorID || existing.RequestDigest != digest {
		return Receipt{}, ErrCommandIDConflict
	}
	receipt := existing.Receipt
	receipt.Replayed = true
	return receipt, nil
}

// recordDecision persists one command attempt — accepted or rejected —
// under the locks lockForCommand already holds: an action row always,
// an items update and (on close) evidence/run-finish only when
// decision.Accepted. log_seq increments for every attempt; seq only for
// an applied one (RFC-001 §6 "log_seq нумерует все авторизованные
// попытки... включая отклонённые").
func (s *Service) recordDecision(ctx context.Context, tx pgx.Tx, lesson Lesson, exercise Exercise, actor auth.Principal, item Item, run Run, cmd Command, decision Decision, now time.Time, requestID string) (Receipt, error) {
	digest, err := RequestDigest(actor.UserID, item.ID, cmd.Type, cmd.Payload, cmd.ExpectedSeq, cmd.ClientAt)
	if err != nil {
		return Receipt{}, err
	}

	newLogSeq := item.LogSeq + 1
	newSeq := item.Seq
	if decision.Accepted {
		newSeq++
	}
	actionID := uuid.New()
	if decision.Accepted && decision.StartCall != nil {
		decision.StartCall.ID, decision.StartCall.ItemID = uuid.New(), item.ID
	}

	receipt := Receipt{
		CommandID: cmd.CommandID, Seq: newSeq, Reaction: decision.Reaction,
		ItemState: decision.State, ServerAt: now, ActionID: actionID, LogSeq: newLogSeq,
	}
	if decision.Accepted && decision.StartCall != nil {
		receipt.CallID = &decision.StartCall.ID
	}
	httpStatus := 200
	if decision.Accepted {
		receipt.Outcome = OutcomeApplied
		completeAt := item.Deadlines.CompleteAt
		if decision.CompleteAt != nil {
			completeAt = decision.CompleteAt
		}
		receipt.Deadlines = &Deadlines{OpenAt: item.Deadlines.OpenAt, PrimaryAt: item.Deadlines.PrimaryAt, CompleteAt: completeAt}
	} else {
		receipt.Outcome = OutcomeRejected
		rejection := decision.Rejection
		receipt.ErrorCode = &rejection
		httpStatus = decision.Rejection.HTTPStatus()
	}

	var reactionAfter content.Reaction
	if decision.Accepted {
		reactionAfter = decision.Reaction
	}
	action := Action{
		ID: actionID, ItemID: item.ID, Seq: newSeq, LogSeq: newLogSeq, ActorID: actor.UserID,
		RequestDigest: digest, CommandID: cmd.CommandID, Type: cmd.Type, Payload: cmd.Payload,
		Effect: decision.Effect, Accepted: decision.Accepted, Rejection: decision.Rejection,
		Receipt: receipt, HTTPStatus: httpStatus, ClientAt: cmd.ClientAt, ServerAt: now,
		ReactionAfter: reactionAfter,
	}
	if _, err := s.store.InsertAction(ctx, tx, action); err != nil {
		return Receipt{}, err
	}
	if decision.Accepted && decision.StartCall != nil {
		if _, err := s.store.InsertCall(ctx, tx, *decision.StartCall); err != nil {
			return Receipt{}, err
		}
		item.Calls = append(item.Calls, *decision.StartCall)
	}
	if decision.Accepted && decision.EndCall != nil {
		if err := s.store.EndCall(ctx, tx, decision.EndCall.CallID, now, decision.EndCall.AcceptedBy, decision.EndCall.Summary, decision.EndCall.Recording); err != nil {
			return Receipt{}, err
		}
		for i := range item.Calls {
			if item.Calls[i].ID == decision.EndCall.CallID {
				item.Calls[i].EndedAt = &now
				item.Calls[i].AcceptedBy = &decision.EndCall.AcceptedBy
				item.Calls[i].Summary = &decision.EndCall.Summary
				item.Calls[i].Recording = decision.EndCall.Recording
				if decision.EndCall.Recording != nil {
					item.Calls[i].RecordingState = RecordingAwaiting
				}
				break
			}
		}
	}

	auditOutcome := audit.OutcomeOK
	details := map[string]any{"type": string(cmd.Type)}
	if !decision.Accepted {
		auditOutcome = audit.OutcomeRejected
		details["rejection"] = string(decision.Rejection)
	}
	if err := s.store.AuditRecord(ctx, tx, audit.Entry{
		ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "item.command",
		ResourceType: "item", ResourceID: &item.ID, Outcome: auditOutcome, RequestID: requestID, Details: details,
	}); err != nil {
		return Receipt{}, err
	}

	// items.log_seq must advance for every attempt, accepted or
	// rejected (RFC-001 §6: "log_seq нумерует все авторизованные
	// попытки... включая отклонённые") — otherwise the next command,
	// whatever its outcome, would recompute the same log_seq from a
	// stale item.LogSeq and collide with actions' UNIQUE(item_id,
	// log_seq). Seq/Reaction/State/Card are written unconditionally too
	// (a no-op on reject, per Decision's own contract).
	patch := ItemPatch{
		LogSeq: newLogSeq, Seq: newSeq, Reaction: decision.Reaction, State: decision.State, Card: decision.Card,
		OpenedAt: decision.OpenedAt, PrimaryAt: decision.PrimaryAt, CompleteAt: decision.CompleteAt,
	}
	if decision.Accepted && decision.Close != nil {
		closedAt := now
		patch.ClosedAt = &closedAt
		patch.CloseReason = decision.Close
	}
	if err := s.store.ApplyItemDecision(ctx, tx, item.ID, patch); err != nil {
		return Receipt{}, err
	}
	// One invalidation per command attempt, accepted or rejected — a
	// rejected attempt still moves log_seq and is worth the instructor's
	// monitor refreshing "last_action" for (ADR-018/RFC-001 §7.7: NOTIFY
	// carries identifiers only, the client always re-reads PostgreSQL).
	if err := s.notify(ctx, tx, item.LessonID, actor.UserID, item.ID); err != nil {
		return Receipt{}, err
	}
	if decision.Accepted {
		version, err := s.scenarios.VersionByID(ctx, tx, item.ScenarioVersionID)
		if err != nil {
			return Receipt{}, err
		}
		anchor := ""
		if cmd.Type == CommandOpen {
			anchor = "opened"
		} else {
			switch decision.Reaction {
			case content.ReactionAccepted:
				anchor = "accepted"
			case content.ReactionResponding:
				anchor = "responding"
			case content.ReactionArrived:
				anchor = "arrived"
			case content.ReactionWorking:
				anchor = "working"
			}
		}
		if anchor != "" {
			if err := s.scheduleEventsForAnchor(ctx, tx, item, version.Body.Events, anchor, now); err != nil {
				return Receipt{}, err
			}
		}
	}
	if !decision.Accepted || decision.Close == nil {
		return receipt, nil
	}

	closedItem := item
	closedItem.Seq, closedItem.LogSeq = newSeq, newLogSeq
	closedItem.Reaction, closedItem.State, closedItem.Card = decision.Reaction, decision.State, decision.Card
	if decision.PrimaryAt != nil {
		closedItem.PrimaryAt = decision.PrimaryAt
	}
	if decision.CompleteAt != nil {
		closedItem.Deadlines.CompleteAt = decision.CompleteAt
	}
	closedAt := now
	closedItem.ClosedAt = &closedAt
	closedItem.CloseReason = decision.Close
	if err := s.store.SetCallRecordingDeadline(ctx, tx, item.ID, closedAt.Add(time.Duration(lesson.RecordingGraceS)*time.Second)); err != nil {
		return Receipt{}, err
	}
	calls, err := s.store.CallsByItem(ctx, tx, item.ID)
	if err != nil {
		return Receipt{}, err
	}
	closedItem.Calls = calls

	// RFC-001 §7.4's close pseudocode cancels any event still scheduled
	// for this item before the evidence snapshot is taken, so a "will
	// never actually fire" event is never frozen into evidence as
	// "scheduled" (tickEvent's own item-closed check is only a lazy
	// fallback for an event whose due_at was already in the past).
	if err := s.store.SkipRemainingItemEvents(ctx, tx, item.ID, SkipReasonItemClosed); err != nil {
		return Receipt{}, err
	}
	actions, err := s.store.ActionsByItem(ctx, tx, item.ID)
	if err != nil {
		return Receipt{}, err
	}
	events, err := s.store.ItemEventsByItem(ctx, tx, item.ID)
	if err != nil {
		return Receipt{}, err
	}
	evidence, err := exercise.Evidence(closedItem, actions, events, newLogSeq, closedAt)
	if err != nil {
		return Receipt{}, err
	}
	if err := s.store.InsertEvidence(ctx, tx, item.ID, evidence); err != nil {
		return Receipt{}, err
	}
	assignments, err := s.store.AssignmentsByLesson(ctx, tx, item.LessonID)
	if err != nil {
		return Receipt{}, err
	}
	assignment, ok := assignmentForRun(assignments, run)
	if !ok {
		return Receipt{}, fmt.Errorf("training: missing assignment for run %s", run.ID)
	}
	if lesson.Level != auth.LevelHard && run.QueueCursor < len(assignment.ScenarioVersionIDs) {
		// A normal close advances an ordered queue. Hard-mode parallel
		// issuance is intentionally delegated to C5's scheduler.
		if err := s.offerQueueVersion(ctx, tx, lesson, run, assignment, run.QueueCursor, closedAt, nil); err != nil {
			return Receipt{}, err
		}
		return receipt, nil
	}
	if lesson.Level == auth.LevelHard && run.QueueCursor < len(assignment.ScenarioVersionIDs) {
		// The hard scheduler owns future offers; closing one parallel item
		// neither pulls the schedule forward nor terminates the run.
		return receipt, nil
	}
	items, err := s.store.ItemsByRun(ctx, tx, run.ID)
	if err != nil {
		return Receipt{}, err
	}
	for _, candidate := range items {
		if candidate.ID != item.ID && candidate.State != ItemClosed && candidate.State != ItemInterrupted {
			return receipt, nil
		}
	}
	if err := s.store.FinishRun(ctx, tx, run.ID, closedAt); err != nil {
		return Receipt{}, err
	}
	runs, err := s.store.RunsByLesson(ctx, tx, item.LessonID)
	if err != nil {
		return Receipt{}, err
	}
	for _, candidate := range runs {
		if candidate.State != RunFinished {
			return receipt, nil
		}
	}
	if err := s.store.FinishLesson(ctx, tx, item.LessonID, closedAt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// recordControlReport is control_report's own persistence path
// (RFC-001 §7.5/ADR-004): allowed only once the item is closed or
// interrupted, still subject to expected_seq/idempotency, but it never
// changes reaction/state/card, never schedules events and never touches
// evidence — a plain journal entry plus one control_reports row. It
// therefore bypasses recordDecision entirely rather than special-casing
// this command type inside it.
func (s *Service) recordControlReport(ctx context.Context, tx pgx.Tx, actor auth.Principal, item Item, cmd Command, now time.Time, requestID string) (Receipt, error) {
	digest, err := RequestDigest(actor.UserID, item.ID, cmd.Type, cmd.Payload, cmd.ExpectedSeq, cmd.ClientAt)
	if err != nil {
		return Receipt{}, err
	}

	var decision Decision
	var text string
	switch {
	case item.State != ItemClosed && item.State != ItemInterrupted:
		decision = unchangedDecision(item, RejectTransitionNotAllowed)
	case cmd.ExpectedSeq != item.Seq:
		decision = unchangedDecision(item, RejectStaleSeq)
	default:
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil || strings.TrimSpace(payload.Text) == "" {
			decision = unchangedDecision(item, RejectInvalidPayload)
		} else {
			text = payload.Text
			decision = Decision{Accepted: true, Reaction: item.Reaction, State: item.State, Card: item.Card}
		}
	}

	newLogSeq := item.LogSeq + 1
	newSeq := item.Seq
	if decision.Accepted {
		newSeq++
	}
	actionID := uuid.New()

	receipt := Receipt{
		CommandID: cmd.CommandID, Seq: newSeq, Reaction: decision.Reaction,
		ItemState: decision.State, ServerAt: now, ActionID: actionID, LogSeq: newLogSeq,
	}
	httpStatus := 200
	if decision.Accepted {
		receipt.Outcome = OutcomeApplied
		receipt.Deadlines = &Deadlines{OpenAt: item.Deadlines.OpenAt, PrimaryAt: item.Deadlines.PrimaryAt, CompleteAt: item.Deadlines.CompleteAt}
	} else {
		receipt.Outcome = OutcomeRejected
		rejection := decision.Rejection
		receipt.ErrorCode = &rejection
		httpStatus = decision.Rejection.HTTPStatus()
	}

	action := Action{
		ID: actionID, ItemID: item.ID, Seq: newSeq, LogSeq: newLogSeq, ActorID: actor.UserID,
		RequestDigest: digest, CommandID: cmd.CommandID, Type: cmd.Type, Payload: cmd.Payload,
		Accepted: decision.Accepted, Rejection: decision.Rejection, Receipt: receipt, HTTPStatus: httpStatus,
		ClientAt: cmd.ClientAt, ServerAt: now,
	}
	if _, err := s.store.InsertAction(ctx, tx, action); err != nil {
		return Receipt{}, err
	}

	auditOutcome := audit.OutcomeOK
	details := map[string]any{"type": string(cmd.Type)}
	if !decision.Accepted {
		auditOutcome = audit.OutcomeRejected
		details["rejection"] = string(decision.Rejection)
	}
	if err := s.store.AuditRecord(ctx, tx, audit.Entry{
		ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "item.control_report",
		ResourceType: "item", ResourceID: &item.ID, Outcome: auditOutcome, RequestID: requestID, Details: details,
	}); err != nil {
		return Receipt{}, err
	}

	patch := ItemPatch{LogSeq: newLogSeq, Seq: newSeq, Reaction: item.Reaction, State: item.State, Card: item.Card}
	if err := s.store.ApplyItemDecision(ctx, tx, item.ID, patch); err != nil {
		return Receipt{}, err
	}

	if decision.Accepted {
		if _, err := s.store.InsertControlReport(ctx, tx, ControlReport{
			ID: uuid.New(), ItemID: item.ID, ActionID: actionID, Text: text, CreatedAt: now,
		}); err != nil {
			return Receipt{}, err
		}
	}
	if err := s.notify(ctx, tx, item.LessonID, actor.UserID, item.ID); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// appendVariation adds a duplicate spawn_card's own "чем отличается"
// text (scenario.schema.json's events[].spawn.variation) to the new
// item's card description, per ADR-018: "'variation' добавляется к
// описанию экземпляра новой карточки" — the scenario body itself is
// immutable and shared with the original item, so the distinguishing
// detail can only live on the spawned item's own mutable card copy.
func appendVariation(description, variation string) string {
	if description == "" {
		return variation
	}
	return description + " " + variation
}

func assignmentForRun(assignments []Assignment, run Run) (Assignment, bool) {
	for _, assignment := range assignments {
		if assignment.WorkstationID == run.WorkstationID && assignment.UserID == run.UserID {
			return assignment, true
		}
	}
	return Assignment{}, false
}

// MyRun returns the caller's active run and its lesson, or ErrNotFound
// if they have none (GET /my/run's 204 case).
// MyRun returns the caller's active run and its lesson, plus the total
// length of the run's own assigned queue — the trainee-facing
// GET /my/run's queue_left is (that total - run.QueueCursor), the same
// "not yet offered" semantics Monitor.rows[].queue_left already uses,
// not a count of currently open items (which active_items/MyItems
// already covers, and which a hard-level run with several parallel
// cards would otherwise double-count as "still queued").
func (s *Service) MyRun(ctx context.Context, actor auth.Principal) (Run, Lesson, int, error) {
	var run Run
	var lesson Lesson
	var queueTotal int
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		run, err = s.store.ActiveRunByUser(ctx, tx, actor.UserID)
		if err != nil {
			return err
		}
		lesson, err = s.store.LessonByID(ctx, tx, run.LessonID, LockNone)
		if err != nil {
			return err
		}
		assignments, err := s.store.AssignmentsByLesson(ctx, tx, run.LessonID)
		if err != nil {
			return err
		}
		if assignment, ok := assignmentForRun(assignments, run); ok {
			queueTotal = len(assignment.ScenarioVersionIDs)
		}
		return nil
	})
	if err != nil {
		return Run{}, Lesson{}, 0, err
	}
	return run, lesson, queueTotal, nil
}

// MyItems returns every item in the caller's active run, offered-first.
func (s *Service) MyItems(ctx context.Context, actor auth.Principal) ([]Item, error) {
	var items []Item
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		run, err := s.store.ActiveRunByUser(ctx, tx, actor.UserID)
		if err != nil {
			return err
		}
		items, err = s.store.ItemsByRun(ctx, tx, run.ID)
		return err
	})
	return items, err
}

// Recover marks every still-open item of every running lesson with one
// idempotent interruption entry, per RFC-001 §7.2's "упрощение MVP":
// cause is always the constant server-restart cause, offered_at/
// deadlines/due_at are never touched, and a repeat call (e.g. a retried
// startup) with the same recoveryID cannot append a second marker —
// RecoverOpenItems's own WHERE NOT EXISTS guard makes that call a no-op.
// cmd/emsim's api process calls this exactly once, before it starts
// answering readiness checks (RFC-001 §7.2: "Новые команды принимаются
// после фиксации маркеров"); it does not itself decide when overdue
// events are delivered — that is simply the scheduler's normal Tick
// loop, started right after this returns, picking up whatever is due.
//
// Each running lesson is recovered under its own barrier — one
// transaction per lesson, holding that lesson's own row FOR UPDATE for
// its duration (RFC-001 §7.2: "в транзакции под барьером занятия"),
// the same lock Start/Stop already use — rather than one bulk statement
// spanning every running lesson under no lock at all. This MVP still
// only ever calls Recover once, synchronously, before the api process
// starts accepting any HTTP request (so nothing can race it today), but
// a real per-lesson barrier means Recover stays correct even if that
// single-process assumption is ever relaxed, instead of silently
// depending on it.
func (s *Service) Recover(ctx context.Context, recoveryID uuid.UUID, cause string) ([]uuid.UUID, error) {
	var lessonIDs []uuid.UUID
	if err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		lessonIDs, err = s.store.RunningLessonIDs(ctx, tx)
		return err
	}); err != nil {
		return nil, err
	}

	var affected []uuid.UUID
	for _, lessonID := range lessonIDs {
		items, err := s.recoverOneLesson(ctx, lessonID, recoveryID, cause)
		if err != nil {
			return nil, err
		}
		affected = append(affected, items...)
	}
	return affected, nil
}

// recoverOneLesson is Recover's own per-lesson barrier transaction: lock
// lessonID FOR UPDATE, re-check it is still running (it could have
// finished between RunningLessonIDs' unlocked read and this lock — a
// lesson that raced its own natural close during startup needs no
// recovery), then mark its open items.
func (s *Service) recoverOneLesson(ctx context.Context, lessonID, recoveryID uuid.UUID, cause string) ([]uuid.UUID, error) {
	var affected []uuid.UUID
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, err := s.store.LessonByID(ctx, tx, lessonID, LockUpdate)
		if err != nil {
			return err
		}
		if lesson.State != LessonRunning {
			return nil
		}
		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}
		affected, err = s.store.RecoverOpenItems(ctx, tx, lessonID, Interruption{RecoveryID: recoveryID, Cause: cause, DetectedAt: now})
		if err != nil {
			return err
		}
		if len(affected) == 0 {
			return nil
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			Action: "training.recover", ResourceType: "lesson", ResourceID: &lessonID, Outcome: audit.OutcomeOK,
			Details: map[string]any{"recovery_id": recoveryID.String(), "cause": cause, "item_count": len(affected)},
		})
	})
	if err != nil {
		return nil, err
	}
	return affected, nil
}

// Tick advances durable time-based training work. It is deliberately a
// synchronous, idempotent application method: C6 will arrange periodic
// execution, while tests and a single-process server can call it directly.
// Each candidate gets its own transaction and rechecks state under the
// lesson -> run -> item lock order, so a stale poll result can do no harm.
func (s *Service) Tick(ctx context.Context) error {
	var now time.Time
	var dueRuns []Run
	var dueEvents []ItemEvent
	if err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		now, err = s.store.Now(ctx, tx)
		if err != nil {
			return err
		}
		dueRuns, err = s.store.RunsDueForOffer(ctx, tx, now)
		if err != nil {
			return err
		}
		dueEvents, err = s.store.ScheduledItemEventsDue(ctx, tx, now)
		return err
	}); err != nil {
		return err
	}
	// Each candidate is independent: one due run or event that fails —
	// a transient lock conflict, or a structural error — must not stop
	// every other due run/event in this same tick from being tried.
	// Before this fix, the first error aborted the whole batch, and
	// since ScheduledItemEventsDue orders by due_at, a candidate that
	// fails the same way on every attempt (see tickEvent's own
	// spawn_card-mismatch handling) starved the scheduler for every
	// other running lesson forever, not just delayed it. Errors are
	// joined and returned so the caller (runTrainingScheduler) still
	// logs/observes them; they never abort the loop itself.
	var errs []error
	for _, run := range dueRuns {
		if err := s.tickHardRun(ctx, run.ID); err != nil {
			errs = append(errs, fmt.Errorf("hard run %s: %w", run.ID, err))
		}
	}
	for _, event := range dueEvents {
		if err := s.tickEvent(ctx, event.ID); err != nil {
			errs = append(errs, fmt.Errorf("item event %s: %w", event.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) tickHardRun(ctx context.Context, runID uuid.UUID) error {
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		peek, err := s.store.RunByID(ctx, tx, runID, LockNone)
		if err != nil {
			return err
		}
		// FOR SHARE, not FOR UPDATE: tickHardRun never writes to the
		// lessons row itself (only Stop/Start/FinishLesson do), so
		// FOR UPDATE here bought no extra safety and only blocked every
		// trainee's own FOR SHARE command on this lesson, every 500ms
		// (same reasoning as tickEvent's own lock, above).
		lesson, err := s.store.LessonByID(ctx, tx, peek.LessonID, LockShare)
		if err != nil {
			return err
		}
		run, err := s.store.RunByID(ctx, tx, runID, LockUpdate)
		if err != nil {
			return err
		}
		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}
		if lesson.State != LessonRunning || lesson.Level != auth.LevelHard || run.State != RunActive {
			// Stop's own barrier (Service.Stop) already clears
			// next_offer_at for every active run of a lesson it stops,
			// so this should not normally be reached with a non-nil
			// NextOfferAt — but a defensive clear here costs nothing
			// and guarantees this run can never be re-selected by
			// RunsDueForOffer on the next tick regardless of how it
			// stopped being tickable (a lesson stopped through some
			// future path that forgets to clear it, a run finished by
			// some other means, etc.).
			if run.NextOfferAt != nil {
				if err := s.store.SetRunNextOfferAt(ctx, tx, run.ID, nil); err != nil {
					return err
				}
			}
			return nil
		}
		if run.NextOfferAt == nil || run.NextOfferAt.After(now) {
			return nil
		}
		assignments, err := s.store.AssignmentsByLesson(ctx, tx, lesson.ID)
		if err != nil {
			return err
		}
		assignment, ok := assignmentForRun(assignments, run)
		if !ok {
			return fmt.Errorf("training: missing assignment for run %s", run.ID)
		}
		if run.QueueCursor >= len(assignment.ScenarioVersionIDs) {
			return s.store.SetRunNextOfferAt(ctx, tx, run.ID, nil)
		}
		if err := s.offerQueueVersion(ctx, tx, lesson, run, assignment, run.QueueCursor, now, nil); err != nil {
			return err
		}
		if lesson.Timing.SpawnEveryS == nil {
			return fmt.Errorf("training: hard run %s lacks spawn interval", run.ID)
		}
		next := now.Add(time.Duration(*lesson.Timing.SpawnEveryS) * time.Second)
		if run.QueueCursor+1 >= len(assignment.ScenarioVersionIDs) {
			return s.store.SetRunNextOfferAt(ctx, tx, run.ID, nil)
		}
		return s.store.SetRunNextOfferAt(ctx, tx, run.ID, &next)
	})
}

func (s *Service) tickEvent(ctx context.Context, eventID uuid.UUID) error {
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		event, err := s.store.ItemEventByID(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if event.State != EventScheduled {
			return nil
		}
		peekItem, err := s.store.ItemByID(ctx, tx, event.ItemID, LockNone)
		if err != nil {
			return err
		}
		peekRun, err := s.store.RunByID(ctx, tx, peekItem.RunID, LockNone)
		if err != nil {
			return err
		}
		// The event's own definition is resolved from the unlocked peek
		// before any lock is taken — item.ScenarioVersionID, like
		// run.LessonID, never changes after insert (scenario versions
		// are immutable, RFC-001 §6), so reading it this early is safe
		// and lets the lock plan below be decided up front.
		version, err := s.scenarios.VersionByID(ctx, tx, peekItem.ScenarioVersionID)
		if err != nil {
			return err
		}
		definition, ok := eventByKey(version.Body.Events, event.EventKey)
		if !ok {
			return fmt.Errorf("training: event %q missing from scenario version", event.EventKey)
		}
		// RFC-001 §7.2's own scheduler lock order is "lessons FOR SHARE
		// → items FOR UPDATE → item_events...": tickEvent never writes
		// to the lessons row itself (only Stop/Start/FinishLesson do),
		// so FOR UPDATE here bought no additional safety — it only
		// blocked every trainee's own FOR SHARE command on this lesson
		// for the transaction's duration, every 500ms, including while
		// an event sits waiting on status_in and is re-selected on
		// every tick. runs FOR UPDATE is upgraded only for a spawn_card
		// event, the one case that actually writes to runs
		// (queue_cursor/next_offer_at) — RFC-001 §7.2: "если транзакция
		// создаёт следующую карточку, она заранее берёт runs FOR UPDATE
		// до блокировок items", which is exactly the order kept below.
		lesson, err := s.store.LessonByID(ctx, tx, peekRun.LessonID, LockShare)
		if err != nil {
			return err
		}
		run := peekRun
		if definition.Delivery == "spawn_card" {
			run, err = s.store.RunByID(ctx, tx, peekRun.ID, LockUpdate)
			if err != nil {
				return err
			}
		}
		item, err := s.store.ItemByID(ctx, tx, event.ItemID, LockUpdate)
		if err != nil {
			return err
		}
		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}
		if lesson.State != LessonRunning || item.State == ItemClosed || item.State == ItemInterrupted {
			return s.store.SkipItemEvent(ctx, tx, event.ID, SkipReasonItemClosed)
		}
		if len(definition.StatusIn) > 0 && !reactionIn(definition.StatusIn, item.Reaction) {
			return nil
		}
		if definition.Delivery == "spawn_card" {
			if err := s.spawnEventCard(ctx, tx, lesson, run, item, definition, now); err != nil {
				if isSpawnPlanMismatch(err) {
					// The hard scheduler's own next_offer_at tick raced
					// this event and consumed the queue slot spawn_card
					// wanted first (ADR-018's plan check at assignment
					// time only proves the *static* plan is reachable,
					// not that runtime issuance order matches it).
					// Retrying can never succeed once that slot is
					// gone, so this is a terminal skip, not an error —
					// before this fix, the same mismatch was returned
					// on every tick forever, and (with Tick's old
					// abort-on-first-error behaviour) starved the
					// scheduler for every other lesson, not just this
					// event.
					if err := s.store.SkipItemEvent(ctx, tx, event.ID, SkipReasonSpawnPlanMismatch); err != nil {
						return err
					}
					return s.notify(ctx, tx, lesson.ID, item.UserID, item.ID)
				}
				return err
			}
		}
		// late is RFC-001 §7.2's restart-recovery marker ("простой
		// сервера"), not a generic "delivered more than 5s after
		// due_at" flag: an event legitimately waiting on status_in
		// (definition.StatusIn above) can clear that gate many seconds
		// after due_at without any server outage ever happening, and
		// schema.sql's own comment on item_events.late documents the
		// restart-only meaning. Only mark late when this item actually
		// carries an interruption detected at or after this event's own
		// due_at — i.e. the scheduler itself was down for some or all
		// of the time this event was pending.
		late := now.Sub(event.DueAt) > 5*time.Second && interruptedSince(item.Interruptions, event.DueAt)
		if err := s.store.DeliverItemEvent(ctx, tx, event.ID, now, late); err != nil {
			return err
		}
		return s.notify(ctx, tx, lesson.ID, item.UserID, item.ID)
	})
}

// isSpawnPlanMismatch reports whether err is spawnEventCard's own
// deterministic "runtime queue state disagrees with this spawn_card's
// target" rejection (all its own validationErr calls, field
// "scenario_version_ids") as opposed to a structural failure (a content
// lookup error, storage failure) that should still be retried/surfaced
// as a real error.
func isSpawnPlanMismatch(err error) bool {
	var verr *ValidationError
	return errors.As(err, &verr) && verr.Field == "scenario_version_ids"
}

// interruptedSince reports whether interruptions contains a marker
// detected at or after threshold — used to distinguish an event
// genuinely delivered late because of a server outage (RFC-001 §7.2)
// from one that simply waited on status_in for a while.
func interruptedSince(interruptions []Interruption, threshold time.Time) bool {
	for _, in := range interruptions {
		if !in.DetectedAt.Before(threshold) {
			return true
		}
	}
	return false
}

func (s *Service) spawnEventCard(ctx context.Context, tx pgx.Tx, lesson Lesson, run Run, item Item, definition content.Event, now time.Time) error {
	assignments, err := s.store.AssignmentsByLesson(ctx, tx, lesson.ID)
	if err != nil {
		return err
	}
	assignment, ok := assignmentForRun(assignments, run)
	if !ok {
		return fmt.Errorf("training: missing assignment for run %s", run.ID)
	}
	if run.QueueCursor >= len(assignment.ScenarioVersionIDs) {
		return validationErr("scenario_version_ids", "spawn_card queue is exhausted")
	}
	nextVersionID := assignment.ScenarioVersionIDs[run.QueueCursor]
	if definition.Spawn == nil {
		return validationErr("scenario_version_ids", "spawn_card is missing its target")
	}
	switch definition.Spawn.Kind {
	case "duplicate":
		if nextVersionID != item.ScenarioVersionID {
			return validationErr("scenario_version_ids", "duplicate spawn_card does not match next queue version")
		}
	case "scenario":
		scenario, err := s.scenarios.ScenarioByKey(ctx, tx, definition.Spawn.ScenarioKey)
		if err != nil {
			return err
		}
		target, err := s.scenarios.VersionByNumber(ctx, tx, scenario.ID, definition.Spawn.Version)
		if err != nil {
			return err
		}
		if target.ID != nextVersionID {
			return validationErr("scenario_version_ids", "spawn_card does not match next queue version")
		}
	default:
		return validationErr("scenario_version_ids", "spawn_card kind is unsupported")
	}
	origin := &spawnOrigin{itemID: item.ID}
	if definition.Spawn != nil {
		origin.variation = definition.Spawn.Variation
	}
	if err := s.offerQueueVersion(ctx, tx, lesson, run, assignment, run.QueueCursor, now, origin); err != nil {
		return err
	}
	if lesson.Level == auth.LevelHard && lesson.Timing.SpawnEveryS != nil {
		next := now.Add(time.Duration(*lesson.Timing.SpawnEveryS) * time.Second)
		return s.store.SetRunNextOfferAt(ctx, tx, run.ID, &next)
	}
	return nil
}

func eventByKey(events []content.Event, key string) (content.Event, bool) {
	for _, event := range events {
		if event.Key == key {
			return event, true
		}
	}
	return content.Event{}, false
}

func reactionIn(reactions []content.Reaction, reaction content.Reaction) bool {
	for _, candidate := range reactions {
		if candidate == reaction {
			return true
		}
	}
	return false
}

// ItemForTrainee reads one item and its action log for its own trainee —
// ownership and workstation checks match Execute's (a trainee reads only
// their own run's items, from the workstation the run is assigned to).
// Actions are included because openapi.yaml's Item schema requires them
// (the trainee's own action feed, not just the card) and the HTTP layer
// (slice 3's C5) has no other port to read them through.
func (s *Service) ItemForTrainee(ctx context.Context, actor auth.Principal, itemID uuid.UUID) (Item, []Action, []DeliveredEvent, error) {
	var item Item
	var actions []Action
	var events []DeliveredEvent
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		item, err = s.store.ItemByID(ctx, tx, itemID, LockNone)
		if err != nil {
			return err
		}
		run, err := s.store.RunByID(ctx, tx, item.RunID, LockNone)
		if err != nil {
			return err
		}
		if run.UserID != actor.UserID {
			return ErrNotFound
		}
		if actor.WorkstationID == nil || *actor.WorkstationID != run.WorkstationID {
			return ErrWorkstationMismatch
		}
		actions, err = s.store.ActionsByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		item.Calls, err = s.store.CallsByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		version, err := s.scenarios.VersionByID(ctx, tx, item.ScenarioVersionID)
		if err != nil {
			return err
		}
		item.Contacts, item.CallPolicy = version.Body.Contacts, version.Body.Reference.Call
		events, err = s.deliveredEventsForItem(ctx, tx, itemID, item.ScenarioVersionID)
		return err
	})
	return item, actions, events, err
}

// ItemForInstructor reads one item, its action log, and the scenario
// version's full Body (its reference is only ever shown to the
// instructor — RFC-001 §5's Item schema) for the instructor who owns its
// lesson.
func (s *Service) ItemForInstructor(ctx context.Context, actor auth.Principal, itemID uuid.UUID) (Item, []Action, []DeliveredEvent, content.Body, error) {
	var item Item
	var actions []Action
	var events []DeliveredEvent
	var body content.Body
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		item, err = s.store.ItemByID(ctx, tx, itemID, LockNone)
		if err != nil {
			return err
		}
		run, err := s.store.RunByID(ctx, tx, item.RunID, LockNone)
		if err != nil {
			return err
		}
		lesson, err := s.store.LessonByID(ctx, tx, run.LessonID, LockNone)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID {
			return ErrNotFound
		}
		version, err := s.scenarios.VersionByID(ctx, tx, item.ScenarioVersionID)
		if err != nil {
			return err
		}
		body = version.Body
		item.Contacts, item.CallPolicy = body.Contacts, body.Reference.Call
		actions, err = s.store.ActionsByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		item.Calls, err = s.store.CallsByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		events, err = s.deliveredEventsForItem(ctx, tx, itemID, item.ScenarioVersionID)
		return err
	})
	return item, actions, events, body, err
}

// Now returns the server's authoritative clock (RFC-001 §7.2's
// server_at/server_time convention) for a read-only response that needs
// to show the caller "now" without that time driving any deadline or
// ordering decision itself — e.g. GET /my/run's server_time.
func (s *Service) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		now, err = s.store.Now(ctx, tx)
		return err
	})
	return now, err
}

// UploadRecording attaches exactly the manifest announced by call_end. The
// caller stages bytes before entering this short transaction; lock order is
// lesson → run → item → calls, matching commands and close.
func (s *Service) UploadRecording(ctx context.Context, actor auth.Principal, itemID, callID uuid.UUID, blob Blob) error {
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, run, item, err := s.lockForCommand(ctx, tx, itemID, CommandCallEnd)
		if err != nil {
			return err
		}
		_ = lesson
		if run.UserID != actor.UserID || actor.WorkstationID == nil || *actor.WorkstationID != run.WorkstationID {
			return ErrNotFound
		}
		call, err := s.store.CallByID(ctx, tx, callID, LockUpdate)
		if err != nil || call.ItemID != itemID {
			return ErrNotFound
		}
		if call.Recording == nil || call.Recording.SHA256 != blob.SHA256 || call.Recording.Size != blob.Size || call.Recording.MIME != blob.MIME {
			return ErrConflict
		}
		if call.RecordingState == RecordingReady {
			return nil
		}
		now, err := s.store.Now(ctx, tx)
		if err != nil {
			return err
		}
		if item.ClosedAt != nil && call.RecordingUploadDeadlineAt != nil && now.After(*call.RecordingUploadDeadlineAt) {
			return ErrConflict
		}
		stored, _, err := s.store.InsertBlob(ctx, tx, blob)
		if err != nil {
			return err
		}
		return s.store.SetCallRecordingReady(ctx, tx, callID, stored.ID, now)
	})
}

// RecordingForInstructor protects audio at the service boundary rather than
// trusting a handler's item lookup.
func (s *Service) RecordingForInstructor(ctx context.Context, actor auth.Principal, itemID, callID uuid.UUID) (Blob, error) {
	var result Blob
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		item, err := s.store.ItemByID(ctx, tx, itemID, LockNone)
		if err != nil {
			return err
		}
		run, err := s.store.RunByID(ctx, tx, item.RunID, LockNone)
		if err != nil {
			return err
		}
		lesson, err := s.store.LessonByID(ctx, tx, run.LessonID, LockNone)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID || (item.State != ItemClosed && item.State != ItemInterrupted) {
			return ErrNotFound
		}
		call, err := s.store.CallByID(ctx, tx, callID, LockNone)
		if err != nil || call.ItemID != itemID || call.BlobID == nil {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx, `SELECT id,sha256,mime,size,created_at FROM blobs WHERE id=$1`, *call.BlobID)
		if err != nil {
			return ErrStorage
		}
		defer rows.Close()
		if !rows.Next() {
			return ErrNotFound
		}
		var raw []byte
		if err := rows.Scan(&result.ID, &raw, &result.MIME, &result.Size, &result.CreatedAt); err != nil {
			return ErrStorage
		}
		copy(result.SHA256[:], raw)
		return nil
	})
	return result, err
}

// RunActions is the instructor's action feed for one run (monitor/
// review) — ownership checked through the run's lesson.
func (s *Service) RunActions(ctx context.Context, actor auth.Principal, lessonID, runID uuid.UUID) ([]Action, error) {
	var actions []Action
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, err := s.store.LessonByID(ctx, tx, lessonID, LockNone)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID {
			return ErrNotFound
		}
		run, err := s.store.RunByID(ctx, tx, runID, LockNone)
		if err != nil {
			return err
		}
		if run.LessonID != lessonID {
			return ErrNotFound
		}
		items, err := s.store.ItemsByRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		for _, it := range items {
			itemActions, err := s.store.ActionsByItem(ctx, tx, it.ID)
			if err != nil {
				return err
			}
			actions = append(actions, itemActions...)
		}
		return nil
	})
	return actions, err
}

// ListLessons returns the instructor's own lessons, optionally filtered
// by state.
func (s *Service) ListLessons(ctx context.Context, actor auth.Principal, state *LessonState) ([]Lesson, error) {
	var lessons []Lesson
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		lessons, err = s.store.ListLessonsByInstructor(ctx, tx, actor.UserID, state)
		return err
	})
	return lessons, err
}

// Lesson returns one lesson and its assignments for the owning
// instructor.
func (s *Service) Lesson(ctx context.Context, actor auth.Principal, lessonID uuid.UUID) (Lesson, []Assignment, error) {
	var lesson Lesson
	var assignments []Assignment
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		lesson, err = s.store.LessonByID(ctx, tx, lessonID, LockNone)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID {
			return ErrNotFound
		}
		assignments, err = s.store.AssignmentsByLesson(ctx, tx, lessonID)
		return err
	})
	if err != nil {
		return Lesson{}, nil, err
	}
	return lesson, assignments, nil
}

// LessonOptionsResult is GET /lessons/options's read model — active
// trainees (with their service's display name resolved) and active
// workstations, for an instructor's assignment UI.
type LessonOptionsResult struct {
	Trainees     []LessonOptionTrainee
	Workstations []auth.Workstation
}

type LessonOptionTrainee struct {
	auth.User
	ServiceName string
}

// LessonOptions lists what ReplaceAssignments can validate against —
// active trainees and active workstations only (slice-planning.md §4).
// It does not check the caller's role: that is GroupLessons's job at the
// HTTP layer (slice 3's C5), same as every other instructor-only method
// here.
func (s *Service) LessonOptions(ctx context.Context) (LessonOptionsResult, error) {
	var result LessonOptionsResult
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		users, _, err := s.users.ListUsers(ctx, tx, 1, 1000)
		if err != nil {
			return err
		}
		services, err := s.services.ListServices(ctx, tx)
		if err != nil {
			return err
		}
		serviceNames := make(map[string]string, len(services))
		for _, svc := range services {
			serviceNames[svc.Code] = svc.Name
		}
		for _, u := range users {
			if u.Role != auth.RoleTrainee || !u.Active {
				continue
			}
			name := ""
			if u.ServiceCode != nil {
				name = serviceNames[*u.ServiceCode]
			}
			result.Trainees = append(result.Trainees, LessonOptionTrainee{User: u, ServiceName: name})
		}

		workstations, err := s.workstations.ListWorkstations(ctx, tx)
		if err != nil {
			return err
		}
		for _, ws := range workstations {
			if ws.Active {
				result.Workstations = append(result.Workstations, ws)
			}
		}
		return nil
	})
	return result, err
}
