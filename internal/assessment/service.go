package assessment

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"emsim/internal/platform/audit"
	"emsim/internal/platform/realtime"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Service coordinates assessment's use cases and transactions
// (CLAUDE.md's hexagonal split: domain rules in rubric.go/score.go/
// revision.go/dds stay independent of HTTP/PostgreSQL; this is the one
// place that opens a transaction and calls Store/ItemReader/
// EvidenceReader/ScenarioReader/TaskStore together). It implements
// tasks.Handler (Handle -> RecordAuto) and tasks.Finalizer
// (FinalizeExpired) directly, so cmd/emsim registers *Service itself
// against the worker's handler registry and Recovery — no adapter type
// needed, the same structural-satisfaction convention
// internal/training/ports.go documents for its own consumer-owned
// interfaces.
type Service struct {
	store      Store
	items      ItemReader
	lessons    LessonReader
	evidence   EvidenceReader
	scenarios  ScenarioReader
	tasks      TaskStore
	evaluators Registry
}

func NewService(store Store, items ItemReader, lessons LessonReader, evidence EvidenceReader, scenarios ScenarioReader, taskStore TaskStore, evaluators Registry) *Service {
	return &Service{store: store, items: items, lessons: lessons, evidence: evidence, scenarios: scenarios, tasks: taskStore, evaluators: evaluators}
}

// evaluatePayload is assessment.evaluate's own task payload shape
// (training.enqueueEvaluateWaiting's own doc comment) — item_id/
// evidence_digest/rubric_version are set once at enqueue and never
// change; input_id starts nil and is filled in by SealAndPromote.
type evaluatePayload struct {
	ItemID         uuid.UUID  `json:"item_id"`
	EvidenceDigest string     `json:"evidence_digest"`
	RubricVersion  string     `json:"rubric_version"`
	InputID        *uuid.UUID `json:"input_id"`
}

// RunCoordinatorTick seals and promotes every assessment.evaluate task
// currently due (RFC-001 §7.4's coordinator: "цикл worker внутри worker,
// каждые 2 с, без LISTEN"). Each due task gets its own short transaction,
// matching WaitingDue's own doc comment: the unlocked candidate read here
// is only a list, and SealAndPromote re-locks whatever it actually needs
// one task at a time.
func (s *Service) RunCoordinatorTick(ctx context.Context, workerID string, now time.Time, limit int) error {
	due, err := s.tasks.WaitingDue(ctx, training.KindAssessmentEvaluate, now, limit)
	if err != nil {
		return err
	}
	for _, task := range due {
		if err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
			return s.SealAndPromote(ctx, tx, task, workerID, now)
		}); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------ coordinator half

// SealAndPromote is the coordinator's per-task step (RFC-001 §7.4): seal
// assessment_inputs for task's item (unless an earlier, crashed attempt
// already did) and promote the waiting task to pending with input_id
// filled in. workerID names this coordinator instance for
// FailWaitingTx's own terminal_worker column; now is the promoted task's
// next_attempt_at (RFC-001 §7.4: "waiting не расходует attempts", so this
// is simply "eligible immediately").
func (s *Service) SealAndPromote(ctx context.Context, tx pgx.Tx, task tasks.WaitingTask, workerID string, now time.Time) error {
	if task.ScopeID == nil {
		return fmt.Errorf("assessment: waiting task %s has no scope_id", task.TaskID)
	}
	itemID := *task.ScopeID

	var payload evaluatePayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("assessment: decode waiting task payload: %w", err)
	}

	inputID, _, found, err := s.store.InputByItem(ctx, tx, itemID)
	if err != nil {
		return err
	}
	if !found {
		inputID, err = s.sealInputForItem(ctx, tx, task.TaskID, itemID, payload, workerID)
		if err != nil || inputID == uuid.Nil {
			// A nil id with no error means SealAndPromote already routed
			// this task to FailWaitingTx (input preparation failed) — the
			// caller's transaction still commits that terminal write.
			return err
		}
	}

	payload.InputID = &inputID
	newPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("assessment: marshal promoted payload: %w", err)
	}
	return s.tasks.PromoteWaitingTx(ctx, tx, task.TaskID, newPayload, now)
}

// sealInputForItem computes and stores one item's assessment_inputs row.
// A recognized preparation failure (unsupported exercise_type, or the
// scenario version's digest no longer matching the evidence's own
// snapshot of it) terminates the task as failed (RFC-001 §7.4: "ошибка
// подготовки → failed без auto") and returns (uuid.Nil, nil) — not an
// error — so SealAndPromote's caller still commits that terminal write
// instead of rolling it back.
func (s *Service) sealInputForItem(ctx context.Context, tx pgx.Tx, taskID, itemID uuid.UUID, payload evaluatePayload, workerID string) (uuid.UUID, error) {
	if _, err := s.items.ItemByID(ctx, tx, itemID, training.LockUpdate); err != nil {
		return uuid.Nil, err
	}
	evidenceBody, evidenceDigest, err := s.evidence.EvidenceByItem(ctx, tx, itemID)
	if err != nil {
		return uuid.Nil, err
	}
	if hex.EncodeToString(evidenceDigest[:]) != payload.EvidenceDigest {
		return uuid.Nil, fmt.Errorf("assessment: evidence digest changed since close for item %s", itemID)
	}
	version, err := s.scenarios.VersionByID(ctx, tx, evidenceBody.ScenarioVersionID)
	if err != nil {
		return uuid.Nil, err
	}
	if hex.EncodeToString(version.Digest[:]) != evidenceBody.ScenarioDigest {
		return uuid.Nil, failInput(ctx, tx, s.tasks, taskID, workerID, "scenario_digest_mismatch")
	}
	evaluator, err := s.evaluators.EvaluatorFor(evidenceBody.ExerciseType)
	if err != nil {
		return uuid.Nil, failInput(ctx, tx, s.tasks, taskID, workerID, "unsupported_exercise_type")
	}
	base, err := LoadRubric(evidenceBody.ExerciseType, payload.RubricVersion)
	if err != nil {
		return uuid.Nil, fmt.Errorf("assessment: load rubric %s/%s: %w", evidenceBody.ExerciseType, payload.RubricVersion, err)
	}
	effective := Merge(base, ScoringFor(version.Body))
	evidenceDoc, _, err := s.evidence.EvidenceDocumentByItem(ctx, tx, itemID)
	if err != nil {
		return uuid.Nil, err
	}
	results, err := evaluator.Evaluate(evidenceDoc, version.Body, effective)
	if err != nil {
		var term *TerminalEvaluationError
		if errors.As(err, &term) {
			return uuid.Nil, failInput(ctx, tx, s.tasks, taskID, workerID, term.Code)
		}
		return uuid.Nil, fmt.Errorf("assessment: evaluate item %s: %w", itemID, err)
	}
	ruleResults := make([]RuleResult, 0, len(results))
	for _, r := range results {
		ruleResults = append(ruleResults, RuleResult{ID: r.ID, Status: r.Status})
	}
	inputBody := InputBody{
		Schema: "emsim/assessment-inputs/v1", ItemID: itemID,
		EvidenceDigest: payload.EvidenceDigest, RubricVersion: payload.RubricVersion, RubricEffective: effective,
		Transcripts:   []Transcript{},
		Judge:         Judge{Model: nil, PromptVersions: map[string]string{}, Parameters: map[string]any{}},
		SemanticInput: map[string]any{}, ExerciseType: evidenceBody.ExerciseType, RuleResults: ruleResults,
	}
	canonical, digest, err := SealInput(inputBody)
	if err != nil {
		return uuid.Nil, err
	}
	return s.store.InsertInput(ctx, tx, itemID, canonical, digest)
}

// failInput terminates a waiting task as failed and returns (uuid.Nil,
// nil), the sentinel sealInputForItem's caller reads as "already handled,
// commit the failure" rather than an error to propagate.
func failInput(ctx context.Context, tx pgx.Tx, store TaskStore, taskID uuid.UUID, workerID string, code tasks.ErrorCode) error {
	return store.FailWaitingTx(ctx, tx, taskID, workerID, code)
}

// ------------------------------------------------------------ worker handler half

// Handle implements tasks.Handler — assessment.evaluate's own claimed-
// task entry point (cmd/emsim registers *Service directly against the
// "llm" pool's HandlerRegistry).
func (s *Service) Handle(ctx context.Context, lease tasks.Lease) error {
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		return s.recordAutoTx(ctx, tx, lease)
	})
}

func (s *Service) recordAutoTx(ctx context.Context, tx pgx.Tx, lease tasks.Lease) error {
	var payload evaluatePayload
	if err := json.Unmarshal(lease.Payload, &payload); err != nil {
		return fmt.Errorf("assessment: decode assessment.evaluate payload: %w", err)
	}
	// items FOR UPDATE is a mutex against a concurrent CreateExpertRevision
	// for the same item, not a write of any item field (RFC-001 §7.4:
	// "Worker перед фиксацией проверяет отмену/отсутствие expert").
	if _, err := s.items.ItemByID(ctx, tx, payload.ItemID, training.LockUpdate); err != nil {
		return err
	}
	hasExpert, err := s.store.HasExpert(ctx, tx, payload.ItemID)
	if err != nil {
		return err
	}
	if !hasExpert {
		if _, found, err := s.store.AutoByItem(ctx, tx, payload.ItemID); err != nil {
			return err
		} else if !found {
			if payload.InputID == nil {
				return fmt.Errorf("assessment: evaluate task %s has no input_id", lease.TaskID)
			}
			if err := s.computeAndInsertAuto(ctx, tx, payload.ItemID, *payload.InputID, lease.TaskID); err != nil {
				return err
			}
		}
	}
	// If an expert revision won the race and cancelled this task
	// concurrently, Terminal's own fencing (status is no longer 'leased')
	// makes this return ErrLeaseLost — exactly the benign outcome the
	// Runner already treats as "someone else finished this" (RFC-001
	// §7.4: "поздняя auto не появляется").
	_, err = s.tasks.Terminal(ctx, tx, tasks.TerminalRequest{Lease: lease, Now: time.Now().UTC(), Outcome: tasks.Done(nil)})
	return err
}

// computeAndInsertAuto runs the registered RuleEvaluator over the item's
// immutable evidence+reference+rubric_effective (all already fixed in
// the sealed input) and records the single auto rev=1 the pipeline ever
// produces (RFC-001 §7.4). Shared by the normal handler path
// (recordAutoTx) and the exhaustion Finalizer (FinalizeExpired) — the
// same computation either way, only the caller's transaction and
// terminal write differ.
func (s *Service) computeAndInsertAuto(ctx context.Context, tx pgx.Tx, itemID, inputID, sourceTaskID uuid.UUID) error {
	inputBody, err := s.store.InputByID(ctx, tx, inputID)
	if err != nil {
		return err
	}
	evidenceBody, evidenceDigest, err := s.evidence.EvidenceByItem(ctx, tx, itemID)
	if err != nil {
		return err
	}
	evidenceDoc, _, err := s.evidence.EvidenceDocumentByItem(ctx, tx, itemID)
	if err != nil {
		return err
	}
	version, err := s.scenarios.VersionByID(ctx, tx, evidenceBody.ScenarioVersionID)
	if err != nil {
		return err
	}
	evaluator, err := s.evaluators.EvaluatorFor(evidenceBody.ExerciseType)
	if err != nil {
		return err
	}
	// sealInputForItem already ran this same Evaluate once (over the same
	// digest-verified evidence and rubric_effective) to build
	// assessment_inputs.rule_results — a second error here would mean the
	// evaluator is non-deterministic, which ADR-006 forbids, so this is
	// treated as an unexpected error rather than a second
	// failInput/TerminalEvaluationError path.
	results, err := evaluator.Evaluate(evidenceDoc, version.Body, inputBody.RubricEffective)
	if err != nil {
		return fmt.Errorf("assessment: re-evaluate item %s for auto record: %w", itemID, err)
	}
	scoreResult := Score(results, inputBody.RubricEffective)
	a := Assessment{
		ID: uuid.New(), ItemID: itemID, Revision: 1, Kind: KindAuto, Status: scoreResult.Status,
		EvidenceDigest: evidenceDigest, InputID: &inputID, SourceTaskID: &sourceTaskID,
		RubricVersion: inputBody.RubricVersion, RubricEffective: inputBody.RubricEffective,
		Score: scoreResult.Score, Passed: scoreResult.Passed, Criteria: results, CriticalErrors: scoreResult.CriticalErrors,
	}
	if _, err := s.store.InsertAssessment(ctx, tx, a); err != nil {
		return err
	}
	if err := s.store.AuditRecord(ctx, tx, audit.Entry{
		Action: "assessment.auto_record", ResourceType: "item", ResourceID: &itemID, Outcome: audit.OutcomeOK,
		Details: map[string]any{"status": string(a.Status)},
	}); err != nil {
		return err
	}
	return realtime.NotifyTx(ctx, tx, realtime.Event{ItemID: &itemID})
}

// ------------------------------------------------------------ exhaustion finalizer

// FinalizeExpired implements tasks.Finalizer — assessment.evaluate's
// exhaustion path, called by Recovery (never while it holds the task's
// own row lock, per Finalizer's own doc comment) once a lease's retry
// budget is exhausted or its last attempt's lease expired outright. It
// re-derives the item id from the task's own scope_id (an unlocked
// peek), then acquires the same items lock recordAutoTx uses before
// deciding whether an auto is still owed, so the two can never race.
func (s *Service) FinalizeExpired(ctx context.Context, taskID uuid.UUID, workerID string, token uint64, terminalStatus tasks.TaskStatus, code tasks.ErrorCode) error {
	scopeID, payloadRaw, err := s.tasks.PeekPayload(ctx, taskID)
	if err != nil {
		return err
	}
	if scopeID == nil {
		return tasks.ErrLeaseLost
	}
	itemID := *scopeID
	var payload evaluatePayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return fmt.Errorf("assessment: decode exhausted task payload: %w", err)
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.items.ItemByID(ctx, tx, itemID, training.LockUpdate); err != nil {
			return err
		}
		if payload.InputID != nil {
			hasExpert, err := s.store.HasExpert(ctx, tx, itemID)
			if err != nil {
				return err
			}
			if !hasExpert {
				if _, found, err := s.store.AutoByItem(ctx, tx, itemID); err != nil {
					return err
				} else if !found {
					if err := s.computeAndInsertAuto(ctx, tx, itemID, *payload.InputID, taskID); err != nil {
						return err
					}
				}
			}
		}
		ok, err := s.tasks.FinalizeExpiredTx(ctx, tx, taskID, workerID, token, terminalStatus, code)
		if err != nil {
			return err
		}
		if !ok {
			return tasks.ErrLeaseLost
		}
		return nil
	})
}

// ------------------------------------------------------------ expert revisions

// CreateExpertRevision records one instructor expert revision (RFC-001
// §7.4). Authorization (the caller is the item's own lesson's
// instructor) is the HTTP layer's job (slice 6's C7) — this method only
// enforces domain invariants: the item must be closed (evidence exists),
// BaseRevision must match the item's current final revision exactly
// (ErrStaleRevision otherwise), and the criteria set must validate
// against the effective rubric (ValidateRevision's own ErrValidation
// family). requestID may be "" for a non-HTTP caller (there is none in
// slice 6, but this keeps the signature stable for C7).
func (s *Service) CreateExpertRevision(ctx context.Context, itemID, createdBy uuid.UUID, in RevisionInput, requestID string) (Assessment, error) {
	var result Assessment
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		evidenceBody, evidenceDigest, err := s.evidence.EvidenceByItem(ctx, tx, itemID)
		if err != nil {
			if errors.Is(err, training.ErrNotFound) {
				return ErrNotClosed
			}
			return err
		}
		// RFC-001 §8's lock order: trainee_assessment_state -> items -> tasks.
		if _, err := s.store.LockTraineeState(ctx, tx, evidenceBody.TraineeID, evidenceBody.ExerciseType); err != nil {
			return err
		}
		item, err := s.items.ItemByID(ctx, tx, itemID, training.LockUpdate)
		if err != nil {
			return err
		}

		version, err := s.scenarios.VersionByID(ctx, tx, evidenceBody.ScenarioVersionID)
		if err != nil {
			return err
		}

		final, hasFinal, err := s.store.FinalByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		// 112-6/ADR-026's c3: an item is always scored against the exact
		// rubric version its own final assessment already recorded — reuse
		// it rather than reloading "current" (LoadDefaultFor). Only the
		// very first assessment for an item (no auto, no prior expert) has
		// no such record yet; that case alone needs the lesson's own
		// frozen rubric_version.
		var effective Rubric
		if hasFinal {
			effective = final.RubricEffective
		} else {
			lesson, err := s.lessons.LessonByID(ctx, tx, item.LessonID, training.LockNone)
			if err != nil {
				return err
			}
			base, err := LoadRubric(evidenceBody.ExerciseType, lesson.RubricVersion)
			if err != nil {
				return fmt.Errorf("assessment: load rubric %s/%s: %w", evidenceBody.ExerciseType, lesson.RubricVersion, err)
			}
			effective = Merge(base, ScoringFor(version.Body))
		}

		expectedBase := 0
		var currentCriteria []CriterionResult
		if hasFinal {
			expectedBase = final.Revision
			currentCriteria = final.Criteria
		}
		if in.BaseRevision != expectedBase {
			return ErrStaleRevision
		}

		merged, err := ValidateRevision(in, effective, currentCriteria)
		if err != nil {
			return err
		}
		scoreResult := ApplyOverride(Score(merged, effective), in.ScoreOverride, effective)

		auto, hasAuto, err := s.store.AutoByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		var inputID *uuid.UUID
		if hasAuto {
			inputID = auto.InputID
		}

		newRevision := 2
		if in.BaseRevision > 0 {
			newRevision = in.BaseRevision + 1
		}
		baseRevision := in.BaseRevision
		reason := in.Reason
		a := Assessment{
			ID: uuid.New(), ItemID: itemID, Revision: newRevision, Kind: KindExpert, Status: StatusReady,
			EvidenceDigest: evidenceDigest, InputID: inputID, BaseRevision: &baseRevision,
			RubricVersion: effective.Version, RubricEffective: effective,
			Score: scoreResult.Score, Passed: scoreResult.Passed, Criteria: merged, CriticalErrors: scoreResult.CriticalErrors,
			CreatedBy: &createdBy, Reason: &reason,
		}
		assessmentID, err := s.store.InsertAssessment(ctx, tx, a)
		if err != nil {
			return err
		}
		a.ID = assessmentID

		if hasAuto {
			autoByID := make(map[string]CriterionResult, len(auto.Criteria))
			for _, c := range auto.Criteria {
				autoByID[c.ID] = c
			}
			for _, c := range merged {
				if prior, ok := autoByID[c.ID]; ok && (prior.Status != c.Status || !floatEqual(prior.Score, c.Score)) {
					if err := s.store.InsertTrainingExample(ctx, tx, assessmentID, c.ID, prior, c); err != nil {
						return err
					}
				}
			}
		}

		if summary, err := s.tasks.ByDedupKey(ctx, tx, training.EvaluateDedupKey(itemID)); err == nil {
			now, nowErr := databaseNow(ctx, tx)
			if nowErr != nil {
				return nowErr
			}
			if _, err := s.tasks.CancelTx(ctx, tx, tasks.CancelRequest{TaskID: summary.TaskID, Now: now}); err != nil {
				return err
			}
		} else if !errors.Is(err, tasks.ErrNotFound) {
			return err
		}

		// 112-7/ADR-027: a preview run's own auto-assessment is real
		// (enqueueEvaluateWaiting now fires for it too) but must never
		// move the basis a future recommendation/advice would use —
		// trainee_assessment_state stays untouched for preview, the same
		// way intro never reaches this method's caller at all (intro has
		// no evidence to revise, since it gets no assessment either).
		if item.Mode != training.ModePreview {
			if err := s.store.BumpTraineeStateVersion(ctx, tx, evidenceBody.TraineeID, evidenceBody.ExerciseType); err != nil {
				return err
			}
		}
		if err := s.store.AuditRecord(ctx, tx, audit.Entry{
			Action: "assessment.expert_revision", ResourceType: "item", ResourceID: &itemID, ActorID: &createdBy,
			Outcome: audit.OutcomeOK, RequestID: requestID, Details: map[string]any{"revision": newRevision},
		}); err != nil {
			return err
		}
		if err := realtime.NotifyTx(ctx, tx, realtime.Event{ItemID: &itemID}); err != nil {
			return err
		}
		result = a
		return nil
	})
	if err != nil {
		return Assessment{}, err
	}
	return result, nil
}

func floatEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Get returns a closed item's complete instructor review projection.  Route
// ownership is deliberately enforced by the HTTP adapter through training's
// ItemForInstructor; keeping that actor-specific rule at the API boundary
// avoids making worker use cases carry an instructor principal.
func (s *Service) Get(ctx context.Context, itemID uuid.UUID) (Detail, error) {
	var result Detail
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		item, err := s.items.ItemByID(ctx, tx, itemID, training.LockNone)
		if err != nil {
			return err
		}
		if item.State != training.ItemClosed && item.State != training.ItemInterrupted {
			return ErrNotClosed
		}
		evidence, _, err := s.evidence.EvidenceByItem(ctx, tx, itemID)
		if err != nil {
			return ErrNotClosed
		}
		final, found, err := s.store.FinalByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		revisions, err := s.store.RevisionsByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		version, err := s.scenarios.VersionByID(ctx, tx, item.ScenarioVersionID)
		if err != nil {
			return err
		}
		var effective Rubric
		if found {
			result.Final = &final
			effective = final.RubricEffective
		} else {
			// No assessment exists yet (still waiting/pending, or the
			// legacy route's terminal failure with no manual review
			// started) — load the item's own frozen rubric version from
			// its lesson rather than "current" (112-6/ADR-026's c3; see
			// CreateExpertRevision's identical reasoning).
			lesson, err := s.lessons.LessonByID(ctx, tx, item.LessonID, training.LockNone)
			if err != nil {
				return err
			}
			base, err := LoadRubric(item.ExerciseType, lesson.RubricVersion)
			if err != nil {
				return err
			}
			effective = Merge(base, ScoringFor(version.Body))
		}
		if summary, err := s.tasks.ByDedupKey(ctx, tx, training.EvaluateDedupKey(itemID)); err == nil {
			status := string(summary.Status)
			result.AutomaticState = &status
		} else if !errors.Is(err, tasks.ErrNotFound) {
			return err
		}
		if item.ExerciseType == "operator112_intake" {
			document, _, err := s.evidence.EvidenceDocumentByItem(ctx, tx, itemID)
			if err != nil {
				return err
			}
			result.Evidence = document
		} else {
			result.Evidence = evidence
		}
		result.Revisions, result.RubricEffective = revisions, effective
		return nil
	})
	if err != nil {
		return Detail{}, err
	}
	return result, nil
}

// ListForLesson returns only closed/interrupted cards.  The caller performs
// the instructor ownership check before invoking it; doing the expensive
// projection only after that check also keeps foreign lessons indistinguish-
// able from missing ones at the HTTP boundary.
func (s *Service) ListForLesson(ctx context.Context, lessonID uuid.UUID) ([]LessonAssessmentItem, error) {
	var result []LessonAssessmentItem
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := s.store.ClosedItemsByLesson(ctx, tx, lessonID)
		if err != nil {
			return err
		}
		for i := range rows {
			final, found, err := s.store.FinalByItem(ctx, tx, rows[i].ItemID)
			if err != nil {
				return err
			}
			if found {
				rows[i].Final = &final
			}
			if summary, err := s.tasks.ByDedupKey(ctx, tx, training.EvaluateDedupKey(rows[i].ItemID)); err == nil {
				state := string(summary.Status)
				rows[i].AutomaticState = &state
			} else if !errors.Is(err, tasks.ErrNotFound) {
				return err
			}
		}
		result = rows
		return nil
	})
	return result, err
}

func databaseNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("assessment: read database clock: %w", err)
	}
	return now, nil
}
