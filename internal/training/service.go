package training

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"emsim/internal/auth"
	"emsim/internal/content"
	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
	exerciseTypes map[content.ExerciseType]Exercise
}

func NewService(store Store, users UserDirectory, workstations WorkstationDirectory, scenarios ScenarioReader, services ServiceReader, exerciseTypes map[content.ExerciseType]Exercise) *Service {
	return &Service{
		store: store, users: users, workstations: workstations,
		scenarios: scenarios, services: services, exerciseTypes: exerciseTypes,
	}
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

// CreateLesson creates a draft lesson (slice-planning.md §4). Only
// exercise_type=dds_processing exists in this slice; timing defaults to
// RFC-001 §7.2's policy when the caller does not override it, and
// spawn_every_s is rejected — hard-level spawn issuance is slice 4.
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
		if timing.SpawnEveryS != nil {
			return Lesson{}, validationErr("timing.spawn_every_s", "not supported until slice 4")
		}
		if timing.OpenS <= 0 || timing.PrimaryS <= 0 || timing.CompleteS <= 0 {
			return Lesson{}, validationErr("timing", "open_s/primary_s/complete_s must be positive")
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

// ReplaceAssignments sets lessonID's assignments — exactly one
// assignment with exactly one scenario version in this slice
// (slice-planning.md §4: "назначение одному обучаемому на одном РМ
// одного подготовленного сценария"). Only the owning instructor may
// call it, and only while the lesson is still a draft.
func (s *Service) ReplaceAssignments(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, inputs []AssignmentInput, requestID string) (Lesson, error) {
	if len(inputs) != 1 {
		return Lesson{}, validationErr("assignments", "exactly one assignment is supported until slice 4")
	}
	if len(inputs[0].ScenarioVersionIDs) != 1 {
		return Lesson{}, validationErr("scenario_version_ids", "exactly one scenario version is supported until slice 4")
	}
	in := inputs[0]

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

		versionID := in.ScenarioVersionIDs[0]
		version, err := s.scenarios.VersionByID(ctx, tx, versionID)
		if errors.Is(err, content.ErrNotFound) {
			return validationErr("scenario_version_ids", "unknown")
		} else if err != nil {
			return err
		}
		if err := s.checkAssignableVersion(version, lesson.ExerciseType, *trainee.ServiceCode); err != nil {
			return err
		}

		assignment := Assignment{
			LessonID: lessonID, WorkstationID: ws.ID, WorkstationNo: ws.Number,
			UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID},
		}
		if err := s.store.ReplaceAssignments(ctx, tx, lessonID, []Assignment{assignment}); err != nil {
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
	if len(version.Body.Events) > 0 {
		return validationErr("scenario_version_ids", "scenario events are not supported until slice 4")
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

	versionID := a.ScenarioVersionIDs[0]
	version, err := s.scenarios.VersionByID(ctx, tx, versionID)
	if err != nil {
		return err
	}
	if err := s.checkAssignableVersion(version, lesson.ExerciseType, *trainee.ServiceCode); err != nil {
		return err
	}

	if _, err := s.store.ActiveRunByUser(ctx, tx, trainee.ID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	svc, err := s.services.ServiceByCode(ctx, tx, version.Body.TargetService)
	if err != nil {
		return err
	}

	run := Run{
		ID: uuid.New(), ExerciseType: lesson.ExerciseType, LessonID: lesson.ID,
		UserID: trainee.ID, WorkstationID: ws.ID, WorkstationNo: ws.Number,
		Mode: lesson.Mode, State: RunActive, LevelAtStart: lesson.Level,
		QueueCursor: 1, StartedAt: now,
	}
	run, err = s.store.InsertRun(ctx, tx, run)
	if err != nil {
		return err
	}

	openAt := now.Add(time.Duration(lesson.Timing.OpenS) * time.Second)
	item := Item{
		ID: uuid.New(), RunID: run.ID, LessonID: lesson.ID, UserID: trainee.ID,
		WorkstationNo: ws.Number, ScenarioVersionID: versionID,
		ScenarioDigest: fmt.Sprintf("%x", version.Digest), TargetService: version.Body.TargetService,
		Ordinal: 1, State: ItemOffered, Reaction: content.ReactionAdded,
		Card: content.ProjectCard(version.Body.Card), Workflow: svc.Workflow,
		PilotGoal: version.Body.Reference.PilotGoal, Mode: lesson.Mode,
		TimingEffective: lesson.Timing,
		Deadlines:       Deadlines{OpenAt: openAt, PrimaryAt: openAt},
		OfferedAt:       now,
	}
	if _, err := s.store.InsertItem(ctx, tx, item); err != nil {
		return err
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

		exercise, err := s.exerciseFor(lesson.ExerciseType)
		if err != nil {
			return err
		}

		now, err := s.store.Now(ctx, tx)
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
			decision, err = exercise.Decide(item, cmd, now)
			if err != nil {
				return err
			}
		}

		receipt, err = s.recordDecision(ctx, tx, exercise, actor, item, run, cmd, decision, now, requestID)
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
		run, err = s.store.RunByID(ctx, tx, peekRun.ID, LockUpdate)
		if err != nil {
			return Lesson{}, Run{}, Item{}, err
		}
	}
	lesson, err := s.store.LessonByID(ctx, tx, peekRun.LessonID, lessonLock)
	if err != nil {
		return Lesson{}, Run{}, Item{}, err
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
func (s *Service) recordDecision(ctx context.Context, tx pgx.Tx, exercise Exercise, actor auth.Principal, item Item, run Run, cmd Command, decision Decision, now time.Time, requestID string) (Receipt, error) {
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

	receipt := Receipt{
		CommandID: cmd.CommandID, Seq: newSeq, Reaction: decision.Reaction,
		ItemState: decision.State, ServerAt: now, ActionID: actionID, LogSeq: newLogSeq,
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

	actions, err := s.store.ActionsByItem(ctx, tx, item.ID)
	if err != nil {
		return Receipt{}, err
	}
	evidence, err := exercise.Evidence(closedItem, actions, newLogSeq, closedAt)
	if err != nil {
		return Receipt{}, err
	}
	if err := s.store.InsertEvidence(ctx, tx, item.ID, evidence); err != nil {
		return Receipt{}, err
	}
	// Slice 3: exactly one item per run, so closing it always exhausts
	// the queue; issuing a next item and finishing the lesson once every
	// run is done are slice 4's queue-advancement concerns.
	if err := s.store.FinishRun(ctx, tx, run.ID, closedAt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// MyRun returns the caller's active run and its lesson, or ErrNotFound
// if they have none (GET /my/run's 204 case).
func (s *Service) MyRun(ctx context.Context, actor auth.Principal) (Run, Lesson, error) {
	var run Run
	var lesson Lesson
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		run, err = s.store.ActiveRunByUser(ctx, tx, actor.UserID)
		if err != nil {
			return err
		}
		lesson, err = s.store.LessonByID(ctx, tx, run.LessonID, LockNone)
		return err
	})
	if err != nil {
		return Run{}, Lesson{}, err
	}
	return run, lesson, nil
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

// ItemForTrainee reads one item and its action log for its own trainee —
// ownership and workstation checks match Execute's (a trainee reads only
// their own run's items, from the workstation the run is assigned to).
// Actions are included because openapi.yaml's Item schema requires them
// (the trainee's own action feed, not just the card) and the HTTP layer
// (slice 3's C5) has no other port to read them through.
func (s *Service) ItemForTrainee(ctx context.Context, actor auth.Principal, itemID uuid.UUID) (Item, []Action, error) {
	var item Item
	var actions []Action
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
		return err
	})
	return item, actions, err
}

// ItemForInstructor reads one item, its action log, and the scenario
// version's full Body (its reference is only ever shown to the
// instructor — RFC-001 §5's Item schema) for the instructor who owns its
// lesson.
func (s *Service) ItemForInstructor(ctx context.Context, actor auth.Principal, itemID uuid.UUID) (Item, []Action, content.Body, error) {
	var item Item
	var actions []Action
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
		actions, err = s.store.ActionsByItem(ctx, tx, itemID)
		return err
	})
	return item, actions, body, err
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
