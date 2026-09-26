// ADR-028's own pipeline integration tests (112-6 LLM-stage's c11): the
// judge-specific half of the close -> waiting -> sealed input -> auto
// rev=1 lifecycle operator112_assessment_pipeline_test.go already
// covers for the deterministic rubric-v2 case. Reuses that file's own
// helpers (newOperator112PipelineFixtureWithJudge, closeFullCaseItem's
// sibling below, claimAndHandle, mustRunCoordinatorTick, readAssessments,
// evaluateTaskStatus, criterionByID, resolveAllCriteria, floatPtr) — a
// fakeSemanticJudge stands in for a real model call throughout, so
// these tests never need Ollama/llama-server running.
//
//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"emsim/internal/assessment"
	"emsim/internal/assessment/operator112/descjudge"
	"emsim/internal/content"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// fakeSemanticJudge implements assessment.SemanticJudge with a plain
// callback, so each test can script exactly the answer (or failure) it
// wants without a real model — the same "fake port, real transaction"
// pattern this package already uses for training.CallerReplier
// elsewhere. calls counts every invocation, so a test whose reference
// has no questions (or an unfilled description) can assert the judge
// was never called at all — PrepareSemantic's own "nothing to ask"
// short-circuit, proved end to end.
type fakeSemanticJudge struct {
	calls  int
	answer func(payload json.RawMessage) (json.RawMessage, error)
}

func (f *fakeSemanticJudge) Answer(_ context.Context, _ string, _ map[string]any, payload json.RawMessage) (json.RawMessage, error) {
	f.calls++
	return f.answer(payload)
}

// answeringAllQuestions builds a fakeSemanticJudge callback that decodes
// the sealed descjudge.Request and answers every one of its questions
// with the same fixed value — the common case for a "score should come
// out ready" test.
func answeringAllQuestions(value descjudge.Answer) func(json.RawMessage) (json.RawMessage, error) {
	return func(payload json.RawMessage) (json.RawMessage, error) {
		var req descjudge.Request
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		answers := make(map[string]descjudge.Answer, len(req.Questions))
		for _, q := range req.Questions {
			answers[q.ID] = value
		}
		return json.Marshal(answers)
	}
}

// answeringQuestionsByID builds a fakeSemanticJudge callback from an
// explicit id -> Answer map, for a mixed/partial-credit test.
func answeringQuestionsByID(byID map[string]descjudge.Answer) func(json.RawMessage) (json.RawMessage, error) {
	return func(payload json.RawMessage) (json.RawMessage, error) {
		var req descjudge.Request
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		answers := make(map[string]descjudge.Answer, len(req.Questions))
		for _, q := range req.Questions {
			answers[q.ID] = byID[q.ID]
		}
		return json.Marshal(answers)
	}
}

// insertFullCaseVersionWithDescriptionQuestions inserts pilot-112-full-
// gas-road-traffic-fire-01's own body, unchanged except for
// reference.description_questions, as a new approved scenario under a
// fresh source_key — the same technique operator112_assessment_pipeline_
// test.go's own insertLegacyIncomingCallVersion uses. Reusing this
// scenario's exact dialogue/facts means closeFullCaseItemWithComplaint
// below can drive it with the very same command sequence
// closeFullCaseItem already proves works.
func insertFullCaseVersionWithDescriptionQuestions(t *testing.T, ctx context.Context, f operator112PipelineFixture, sourceKey string, questions []content.Intake112DescriptionQuestion) uuid.UUID {
	t.Helper()
	raw, err := os.ReadFile("../../seed/scenarios/pilot-112-full-gas-road-traffic-fire-01.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Body content.Body `json:"body"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	file.Body.Intake112.Reference.DescriptionQuestions = questions
	bodyJSON, err := json.Marshal(file.Body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bodyJSON)
	scenarioID, versionID := uuid.New(), uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO scenarios (id, source_key, title, difficulty, origin, status, created_by)
		VALUES ($1, $2, 'Кейс с вопросами к описанию', 1, 'manual', 'approved', $3)`, scenarioID, sourceKey, f.lesson.InstructorID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO scenario_versions (id, scenario_id, version, status, body, digest, difficulty, exercise_type, created_by, approved_by, approved_at)
		VALUES ($1, $2, 1, 'approved', $3, $4, 1, 'operator112_intake', $5, $5, now())`, versionID, scenarioID, bodyJSON, digest[:], f.lesson.InstructorID); err != nil {
		t.Fatal(err)
	}
	return versionID
}

// closeFullCaseItemWithComplaint is closeFullCaseItem's own parameterized
// sibling: versionID (rather than a hardcoded source_key lookup) and an
// explicit complaint text saved into the draft before notify_services —
// closeFullCaseItem never sets one at all, since ADR-026's plain
// DESCRIPTION_PRESENT only cares whether it is non-empty.
func closeFullCaseItemWithComplaint(t *testing.T, ctx context.Context, f operator112PipelineFixture, versionID uuid.UUID, complaint string) uuid.UUID {
	t.Helper()
	operator, itemID := f.assignAndStart(t, ctx, versionID)
	s := &commandSender{t: t, ctx: ctx, trainingService: f.trainingService, operator: operator, itemID: itemID}

	s.send(training.CommandOpen, map[string]any{})
	s.send(training.CommandAnswerIncoming, map[string]any{})
	s.send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "ask_address"})
	s.send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "clarify_house"})
	s.send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "ask_victims"})
	s.send(training.CommandAddIncidentType, map[string]string{"type_id": "gas_explosion_road_traffic_fire"})

	item, _, _, err := f.trainingService.ItemForTrainee(ctx, operator, itemID)
	if err != nil {
		t.Fatal(err)
	}
	draft := *item.IntakeCard
	if complaint != "" {
		draft.Complaint = training.IntakeField{State: "known", Value: complaint}
	}
	s.send(training.CommandSaveIntakeDraft, map[string]any{"draft": draft})

	item, _, _, err = f.trainingService.ItemForTrainee(ctx, operator, itemID)
	if err != nil {
		t.Fatal(err)
	}
	selected := make([]string, 0, len(item.IntakeState.SuggestedServices))
	for _, sv := range item.IntakeState.SuggestedServices {
		selected = append(selected, sv.ServiceCode)
	}
	s.send(training.CommandNotifyServices, map[string]any{"services": selected, "reason": ""})
	s.send(training.CommandEndIncoming, map[string]any{})
	s.send(training.CommandCompleteIntake, map[string]any{})
	return itemID
}

func testDescriptionQuestions() []content.Intake112DescriptionQuestion {
	return []content.Intake112DescriptionQuestion{
		{ID: "smell", Question: "Указано ли, что ощущается запах газа?"},
		{ID: "victims", Question: "Указано ли число пострадавших?"},
	}
}

// TestOperator112DescriptionJudgeAllYesReady is the judge-enabled happy
// path end to end: a lesson created with judgeEnabled=true freezes
// rubric-v3 (content.Operator112RubricVersion), the closed item's
// description gets a real (fake) judge call outside any transaction,
// and DESCRIPTION_CONTENT comes back met/10 — the whole auto ready with
// a numeric score, not needs_review.
func TestOperator112DescriptionJudgeAllYesReady(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Судья описания: всё да", true)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: answeringAllQuestions(descjudge.AnswerYes)}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	versionID := insertFullCaseVersionWithDescriptionQuestions(t, ctx, f, "pipeline-desc-judge-all-yes", testDescriptionQuestions())
	itemID := closeFullCaseItemWithComplaint(t, ctx, f, versionID, "Сильно пахнет газом, пострадавших двое")

	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-desc-judge-1")
	if !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	if judge.calls != 1 {
		t.Fatalf("judge.calls = %d, want exactly 1", judge.calls)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 || rows[0].Status != "ready" || rows[0].Score == nil {
		t.Fatalf("assessments = %+v, want exactly one ready auto with a numeric score", rows)
	}
	desc, ok := criterionByID(rows[0].Criteria, "DESCRIPTION_CONTENT")
	if !ok || desc.Status != assessment.CriterionMet || desc.Score == nil || *desc.Score != 1 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want met/1", desc)
	}
	var rubricVersion string
	if err := f.pool.QueryRow(ctx, `SELECT rubric_version FROM assessments WHERE item_id=$1 AND kind='auto'`, itemID).Scan(&rubricVersion); err != nil {
		t.Fatal(err)
	}
	if rubricVersion != "operator112/rubric-v3" {
		t.Fatalf("auto.rubric_version = %q, want operator112/rubric-v3", rubricVersion)
	}
}

// TestOperator112DescriptionJudgePartialScoresFraction checks the
// mixed-answer case scores the fraction 112-6's decision 1 describes
// (equal shares of the block's weight), not all-or-nothing.
func TestOperator112DescriptionJudgePartialScoresFraction(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Судья описания: частично", true)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: answeringQuestionsByID(map[string]descjudge.Answer{"smell": descjudge.AnswerYes, "victims": descjudge.AnswerNo})}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	versionID := insertFullCaseVersionWithDescriptionQuestions(t, ctx, f, "pipeline-desc-judge-partial", testDescriptionQuestions())
	itemID := closeFullCaseItemWithComplaint(t, ctx, f, versionID, "Сильно пахнет газом")

	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-desc-judge-2"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	desc, ok := criterionByID(rows[0].Criteria, "DESCRIPTION_CONTENT")
	if !ok || desc.Status != assessment.CriterionPartial || desc.Score == nil || *desc.Score != 0.5 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want partial/0.5", desc)
	}
}

// TestOperator112DescriptionJudgeNeedsReviewThenExpertResolves is 112-6's
// decision 2 end to end: one needs_review answer forces the whole auto
// to needs_review with a NULL score (never a zero), and an instructor's
// expert revision can still resolve it to a real final score.
func TestOperator112DescriptionJudgeNeedsReviewThenExpertResolves(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Судья описания: нужна проверка", true)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: answeringQuestionsByID(map[string]descjudge.Answer{"smell": descjudge.AnswerYes, "victims": descjudge.AnswerNeedsReview})}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	versionID := insertFullCaseVersionWithDescriptionQuestions(t, ctx, f, "pipeline-desc-judge-needs-review", testDescriptionQuestions())
	itemID := closeFullCaseItemWithComplaint(t, ctx, f, versionID, "Пахнет газом, пострадавших двое или трое — неясно")

	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-desc-judge-3"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	auto := readAssessments(t, ctx, f.pool, itemID)[0]
	if auto.Status != "needs_review" || auto.Score != nil {
		t.Fatalf("auto = %+v, want needs_review with a nil score", auto)
	}
	desc, ok := criterionByID(auto.Criteria, "DESCRIPTION_CONTENT")
	if !ok || desc.Status != assessment.CriterionUnavailable {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want unavailable", desc)
	}

	created, err := assessmentService.CreateExpertRevision(ctx, itemID, f.lesson.InstructorID, assessment.RevisionInput{
		BaseRevision: 1, Reason: "Преподаватель проверил описание вручную",
		Criteria: resolveAllCriteria(auto.Criteria),
	}, "pipeline-desc-judge-expert")
	if err != nil || created.Revision != 2 || created.Kind != assessment.KindExpert || created.Status != assessment.StatusReady {
		t.Fatalf("expert revision: %+v, %v", created, err)
	}
}

// TestOperator112DescriptionJudgeTransportErrorIsRetryableHandlerFailure
// proves Handle's own new outside-transaction judge call (answerSemantic)
// turns a judge failure into the same *tasks.HandlerFailure(Retryable,
// "judge_unavailable") shape callerReplyHandler already uses for its
// own model call — and that no assessment row is ever written from a
// failed attempt.
func TestOperator112DescriptionJudgeTransportErrorIsRetryableHandlerFailure(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Судья описания: сбой сети", true)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: func(json.RawMessage) (json.RawMessage, error) {
		return nil, context.DeadlineExceeded
	}}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	versionID := insertFullCaseVersionWithDescriptionQuestions(t, ctx, f, "pipeline-desc-judge-transport-error", testDescriptionQuestions())
	itemID := closeFullCaseItemWithComplaint(t, ctx, f, versionID, "Сильно пахнет газом")

	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-desc-judge-4")
	if !claimed {
		t.Fatal("expected the evaluate task to be claimable")
	}
	failure, ok := tasks.AsHandlerFailure(handleErr)
	if !ok || failure.Retryability() != tasks.Retryable || failure.Code() != "judge_unavailable" {
		t.Fatalf("Handle error = %v, want a retryable judge_unavailable HandlerFailure", handleErr)
	}
	if rows := readAssessments(t, ctx, f.pool, itemID); len(rows) != 0 {
		t.Fatalf("assessments after a failed judge call = %+v, want none", rows)
	}
}

// TestOperator112DescriptionJudgeExhaustionRecordsNeedsReviewNotZero
// mirrors TestAssessmentFinalizerRecordsAutoOnExhaustion for a judge
// that never manages to answer within the task's attempt budget: the
// same claim-then-expire-then-reap cycle, three times (MaxAttempts=3),
// ends in dead_letter with exactly one needs_review auto — never a
// zero, and never blocking manual review.
func TestOperator112DescriptionJudgeExhaustionRecordsNeedsReviewNotZero(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Судья описания: исчерпание попыток", true)
	taskStore := mustTaskEnqueuer(f.pool)
	// A judge configured (so PrepareSemantic actually seals a
	// semantic_input for the coordinator) but never consulted by this
	// test — the retry loop below never calls Handle at all, the same
	// way the DDS exhaustion test never does either.
	judge := &fakeSemanticJudge{answer: func(json.RawMessage) (json.RawMessage, error) { return nil, context.DeadlineExceeded }}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})
	registry := mustAssessmentTaskRegistry(t)
	recovery, err := tasks.NewRecovery(f.pool, tasks.DefaultPolicy(), tasks.NoJitter{}, registry)
	if err != nil {
		t.Fatalf("NewRecovery: %v", err)
	}
	if err := recovery.RegisterFinalizer(training.KindAssessmentEvaluate, assessmentService); err != nil {
		t.Fatalf("RegisterFinalizer: %v", err)
	}

	versionID := insertFullCaseVersionWithDescriptionQuestions(t, ctx, f, "pipeline-desc-judge-exhaustion", testDescriptionQuestions())
	itemID := closeFullCaseItemWithComplaint(t, ctx, f, versionID, "Сильно пахнет газом")
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)

	for attempt := 1; attempt <= 3; attempt++ {
		lease, claimed, err := taskStore.Claim(ctx, tasks.ClaimRequest{
			Kinds: []tasks.Kind{training.KindAssessmentEvaluate}, WorkerID: "test-crashing-worker-desc", Now: databaseTime(t, ctx, f.pool),
		})
		if err != nil || !claimed {
			t.Fatalf("Claim attempt %d: claimed=%v err=%v", attempt, claimed, err)
		}
		if _, err := f.pool.Exec(ctx, `
			UPDATE tasks SET lease_started_at = now() - interval '1 hour', lease_expires_at = now() - interval '55 minutes'
			WHERE id = $1
		`, lease.TaskID); err != nil {
			t.Fatalf("expire lease: %v", err)
		}
		if _, err := recovery.ReapExpired(ctx); err != nil {
			t.Fatalf("ReapExpired attempt %d: %v", attempt, err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE tasks SET next_attempt_at = now() WHERE dedup_key = $1 AND status = 'pending'`, training.EvaluateDedupKey(itemID)); err != nil {
			t.Fatalf("fast-forward next_attempt_at: %v", err)
		}
	}

	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "dead_letter" {
		t.Fatalf("task status = %q, want dead_letter after exhausting max_attempts=3", status)
	}
	if judge.calls != 0 {
		t.Fatalf("judge.calls = %d, want 0 (this test's own retry loop never calls Handle)", judge.calls)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 || rows[0].Kind != "auto" || rows[0].Status != "needs_review" || rows[0].Score != nil {
		t.Fatalf("assessments after exhaustion = %+v, want exactly one needs_review auto with no score", rows)
	}
}

// TestOperator112DescriptionEmptyNeverCallsJudge and
// TestOperator112DescriptionNoQuestionsNeverCallsJudge are ADR-028's own
// "no model call for a zero the reference already explains" guarantee
// (decision 3) — PrepareSemantic's own short-circuit, proved against a
// judge that would fail the test outright if it were ever invoked.
func TestOperator112DescriptionEmptyNeverCallsJudge(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Судья описания: пустое описание", true)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: func(json.RawMessage) (json.RawMessage, error) {
		t.Fatal("the judge must never be called for an empty description")
		return nil, nil
	}}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	versionID := insertFullCaseVersionWithDescriptionQuestions(t, ctx, f, "pipeline-desc-judge-empty", testDescriptionQuestions())
	itemID := closeFullCaseItemWithComplaint(t, ctx, f, versionID, "") // leaves Complaint unset

	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-desc-judge-5"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	desc, ok := criterionByID(rows[0].Criteria, "DESCRIPTION_CONTENT")
	if !ok || desc.Status != assessment.CriterionNotMet || desc.Score == nil || *desc.Score != 0 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want not_met/0 without ever calling the judge", desc)
	}
	if judge.calls != 0 {
		t.Fatalf("judge.calls = %d, want 0", judge.calls)
	}
}

func TestOperator112DescriptionNoQuestionsNeverCallsJudge(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Судья описания: нет вопросов", true)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: func(json.RawMessage) (json.RawMessage, error) {
		t.Fatal("the judge must never be called when the reference has no questions")
		return nil, nil
	}}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	// The unmodified seed scenario — no description_questions at all —
	// under a v3 (judge-enabled) lesson.
	itemID := f.closeFullCaseItem(t, ctx)

	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-desc-judge-6"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 || rows[0].Status != "ready" {
		t.Fatalf("assessments = %+v, want exactly one ready auto (no questions is a plain zero, not needs_review)", rows)
	}
	desc, ok := criterionByID(rows[0].Criteria, "DESCRIPTION_CONTENT")
	if !ok || desc.Status != assessment.CriterionNotMet || desc.Score == nil || *desc.Score != 0 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want not_met/0", desc)
	}
	if judge.calls != 0 {
		t.Fatalf("judge.calls = %d, want 0", judge.calls)
	}
}

// TestOperator112RubricV2LessonWithJudgeConfiguredUnaffected is ADR-028's
// own isolation guarantee from the other direction: a lesson created
// while the judge is off (rubric-v2, no DESCRIPTION_CONTENT criterion at
// all) is scored exactly as before this ADR even when the *worker's*
// assessment.Service happens to have a judge configured — a possible
// operational mismatch (e.g. ASSESSMENT_JUDGE flipped on between two
// deploys) that must never retroactively change an already-frozen
// lesson's own rubric.
func TestOperator112RubricV2LessonWithJudgeConfiguredUnaffected(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Занятие v2 при включённом судье воркера", false)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: func(json.RawMessage) (json.RawMessage, error) {
		t.Fatal("a rubric-v2 lesson has no llm criterion — the judge must never be called")
		return nil, nil
	}}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	itemID := f.closeFullCaseItem(t, ctx)
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-desc-judge-7"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 || rows[0].Status != "ready" {
		t.Fatalf("assessments = %+v, want exactly one ready auto (unaffected rubric-v2 behavior)", rows)
	}
	if _, ok := criterionByID(rows[0].Criteria, "DESCRIPTION_CONTENT"); ok {
		t.Fatal("a rubric-v2 auto must not contain DESCRIPTION_CONTENT at all")
	}
	desc, ok := criterionByID(rows[0].Criteria, "DESCRIPTION_PRESENT")
	if !ok {
		t.Fatal("a rubric-v2 auto must still contain DESCRIPTION_PRESENT")
	}
	_ = desc
	if judge.calls != 0 {
		t.Fatalf("judge.calls = %d, want 0", judge.calls)
	}
}

// TestOperator112DescriptionExpertBeforeJudgeAnswerCancelsEvaluate
// mirrors TestOperator112ExpertRevisionAfterAutoTakesPriority's own
// sibling for a full-hand revision made before any auto exists at all
// (base_revision=0) — RFC-001 §7.4's "поздняя auto не появляется" now
// exercised specifically against Handle's own new judge-calling split:
// a subsequent claim+Handle, even with a judge that WOULD succeed, must
// observe the task already cancelled and never write a second row.
func TestOperator112DescriptionExpertBeforeJudgeAnswerCancelsEvaluate(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixtureWithJudge(t, ctx, "Эксперт до ответа судьи", true)
	taskStore := mustTaskEnqueuer(f.pool)
	judge := &fakeSemanticJudge{answer: answeringAllQuestions(descjudge.AnswerYes)}
	assessmentService := newIntake112AssessmentServiceWithJudgeForTest(f.pool, taskStore, &assessment.JudgeConfig{
		Model: "test-model", Registry: assessment.SemanticJudgeRegistry{descjudge.PromptVersion: judge},
	})

	versionID := insertFullCaseVersionWithDescriptionQuestions(t, ctx, f, "pipeline-desc-judge-expert-first", testDescriptionQuestions())
	itemID := closeFullCaseItemWithComplaint(t, ctx, f, versionID, "Сильно пахнет газом, пострадавших двое")
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)

	// Claim the task first — as if a worker were about to process it —
	// exactly TestAssessmentExpertRevisionWithoutAutoCancelsEvaluate's
	// own sequencing: the expert revision below preempts an already-held
	// lease, and this same lease is then replayed into Handle directly
	// (not re-Claimed) to observe the cancellation Terminal's own
	// fencing would report.
	lease, claimed, err := taskStore.Claim(ctx, tasks.ClaimRequest{
		Kinds: []tasks.Kind{training.KindAssessmentEvaluate}, WorkerID: "test-racing-worker-desc", Now: databaseTime(t, ctx, f.pool),
	})
	if err != nil || !claimed {
		t.Fatalf("Claim: claimed=%v err=%v", claimed, err)
	}

	var inputBodyRaw []byte
	if err := f.pool.QueryRow(ctx, `SELECT body FROM assessment_inputs WHERE item_id = $1`, itemID).Scan(&inputBodyRaw); err != nil {
		t.Fatalf("read assessment_inputs: %v", err)
	}
	var inputBody assessment.InputBody
	if err := json.Unmarshal(inputBodyRaw, &inputBody); err != nil {
		t.Fatalf("decode input body: %v", err)
	}
	criteria := make([]assessment.CriterionResult, 0, len(inputBody.RuleResults))
	for _, rr := range inputBody.RuleResults {
		criteria = append(criteria, assessment.CriterionResult{ID: rr.ID, Status: rr.Status})
	}
	criteria = resolveAllCriteria(criteria) // DESCRIPTION_CONTENT is unavailable (no judge answer yet) -> met

	created, err := assessmentService.CreateExpertRevision(ctx, itemID, f.lesson.InstructorID, assessment.RevisionInput{
		BaseRevision: 0, Reason: "Преподаватель оценил вручную до ответа судьи", Criteria: criteria,
	}, "pipeline-desc-judge-expert-review")
	if err != nil || created.Revision != 2 || created.Kind != assessment.KindExpert {
		t.Fatalf("expert revision before any auto: %+v, %v", created, err)
	}

	handleErr := assessmentService.Handle(ctx, lease)
	if !errors.Is(handleErr, tasks.ErrLeaseLost) {
		t.Fatalf("Handle after an expert revision = %v, want tasks.ErrLeaseLost (cancelled task, no late auto)", handleErr)
	}
	if judge.calls != 1 {
		t.Fatalf("judge.calls = %d, want 1 (Handle's own answerSemantic still runs before the transaction that discovers cancellation)", judge.calls)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 || rows[0].Kind != "expert" {
		t.Fatalf("assessments = %+v, want exactly the one expert revision, no late auto", rows)
	}
}
