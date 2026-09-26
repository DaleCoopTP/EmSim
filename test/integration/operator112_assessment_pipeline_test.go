// 112-6/ADR-026's own pipeline integration tests (slice-112-6-plan.md's
// c7): the same close -> waiting -> sealed input -> auto rev=1 lifecycle
// assessment_pipeline_test.go already covers for dds_processing, driven
// here against a real operator112_intake full_case item instead. Reuses
// that file's package-level helpers (closePilotItemForAssessment's own
// siblings: mustRunCoordinatorTick, claimAndHandle, readAssessments,
// databaseTime, mustTaskEnqueuer, newTrainingService) and
// operator112_full_case_test.go's own command sequence for a full_case
// item, trimmed to just what assessment needs.
//
//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"

	"emsim/internal/assessment"
	assessmentintake "emsim/internal/assessment/operator112"
	assessmentpg "emsim/internal/assessment/postgres"
	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"
	trainingpg "emsim/internal/training/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newIntake112AssessmentServiceForTest is newAssessmentServiceForTest's
// own operator112_intake counterpart — a real Registry entry is exactly
// what distinguishes these c7 tests from c4's TestOperator112IntakeTransaction,
// which never registers an evaluator at all.
func newIntake112AssessmentServiceForTest(pool *pgxpool.Pool, taskStore *tasks.Store) *assessment.Service {
	trainingStore := trainingpg.NewStore(pool)
	contentStore := contentpg.NewStore(pool)
	return assessment.NewService(
		assessmentpg.NewStore(pool), trainingStore, trainingStore, trainingStore, contentStore, taskStore,
		assessment.Registry{content.ExerciseTypeOperator112Intake: assessmentintake.Evaluator}, nil,
	)
}

// operator112PipelineFixture is what every test in this file needs: a
// running services/catalog/classifier/scenario seed, one instructor and
// one trainee, and a helper to drive commands against one item.
type operator112PipelineFixture struct {
	pool            *pgxpool.Pool
	trainingService *training.Service
	instructor      auth.Principal
	lesson          training.Lesson
	trainee         auth.User
	workstation     uuid.UUID
}

func newOperator112PipelineFixture(t *testing.T, ctx context.Context, title string) operator112PipelineFixture {
	t.Helper()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	authStore := authpg.NewStore(pool)
	actor := insertInstructor(t, ctx, pool, "pipeline-instructor-"+uuid.NewString())
	adminID, adminRole := createContentAdmin(t, ctx, authStore, "pipeline-admin-"+uuid.NewString())
	contentService := content.NewService(contentpg.NewStore(pool), mustValidator(t))
	if _, err := contentService.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), adminID, adminRole, "pipeline-services"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportIntakeCatalog(ctx, openSeedFile(t, "../../seed/intake-catalog.json"), adminID, adminRole, "pipeline-catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportClassifierTypes(ctx, openSeedFile(t, "../../seed/classifier.json"), adminID, adminRole, "pipeline-classifier"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportScenarios(ctx, openScenarioDir(t, "../../seed/scenarios"), adminID, adminRole, "pipeline-scenarios"); err != nil {
		t.Fatal(err)
	}
	trainingService := newTrainingService(pool)

	instructor := principal(actor, uuid.Nil)
	lesson, err := trainingService.CreateLesson(ctx, instructor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeOperator112Intake, Title: title, Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "pipeline-lesson")
	if err != nil {
		t.Fatal(err)
	}
	if lesson.RubricVersion != "operator112/rubric-v2" {
		t.Fatalf("new lesson rubric_version = %q, want operator112/rubric-v2", lesson.RubricVersion)
	}

	traineeData := newTrainee("pipeline-trainee-"+uuid.NewString(), "")
	traineeData.ServiceCode = nil
	var trainee auth.User
	if err := authStore.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		trainee, err = authStore.InsertUser(ctx, tx, traineeData)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	workstation := insertWorkstation(t, ctx, pool, 1)

	return operator112PipelineFixture{
		pool: pool, trainingService: trainingService, instructor: instructor,
		lesson: lesson, trainee: trainee, workstation: workstation,
	}
}

// versionBySourceKey returns sourceKey's own *approved* version — some
// seed scenarios (e.g. pilot-112-medical-01) have a newer version 2 that
// supersedes version 1 once the whole seed/scenarios directory is
// imported, so hardcoding version=1 would grab a version the importer
// has already marked not approved.
func (f operator112PipelineFixture) versionBySourceKey(t *testing.T, ctx context.Context, sourceKey string) uuid.UUID {
	t.Helper()
	var versionID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT sv.id FROM scenario_versions sv JOIN scenarios s ON s.id=sv.scenario_id WHERE s.source_key=$1 AND sv.status='approved'`,
		sourceKey).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	return versionID
}

// fillMinimalDraft loads itemID's current card, fills the fields
// dispatch_intake requires (ValidIntakeCard) beyond what open/answer_
// incoming already set, and saves it — the same fields
// TestOperator112IntakeTransaction fills for this same scenario body.
func (f operator112PipelineFixture) fillMinimalDraft(t *testing.T, ctx context.Context, operator auth.Principal, s *commandSender) {
	t.Helper()
	item, _, _, err := f.trainingService.ItemForTrainee(ctx, operator, s.itemID)
	if err != nil {
		t.Fatal(err)
	}
	draft := *item.IntakeCard
	draft.Age = training.IntakeField{State: "known", Value: "19"}
	draft.Address.City = training.IntakeField{State: "known", Value: "Москва"}
	draft.Address.Descriptive = training.IntakeField{State: "known", Value: "рядом с метро ВДНХ"}
	draft.OnSitePhone = training.IntakeField{State: "known", Value: "+79161313131"}
	s.send(training.CommandSaveIntakeDraft, map[string]any{"draft": draft})
}

// insertLegacyIncomingCallVersion inserts pilot-112-medical-01.json's
// own body (the plain script-based incoming_call shape — no dialogue,
// so no scenario-specific "which questions are required before
// dispatch" logic to replicate) directly as an approved version, under
// sourceKey rather than the real seed's own "pilot-112-medical-01" — the
// fixture's own ImportScenarios call already imports the whole seed
// directory, where medical-01's newer v2 (a dialogue variant) supersedes
// v1, so reusing that source_key here would either collide or resolve
// to the wrong (superseded) version. This is exactly
// TestOperator112IntakeTransaction's own setup technique (test/
// integration/operator112_test.go), reused here since c7's own tests
// need a legacy (pre-ADR-023, intake_state.finale=="") item, which
// incoming_call mode always is regardless of scenario content.
func (f operator112PipelineFixture) insertLegacyIncomingCallVersion(t *testing.T, ctx context.Context, sourceKey string) uuid.UUID {
	t.Helper()
	raw, err := os.ReadFile("../../seed/scenarios/pilot-112-medical-01.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Body content.Body `json:"body"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	bodyJSON, err := json.Marshal(file.Body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bodyJSON)
	scenarioID, versionID := uuid.New(), uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO scenarios (id, source_key, title, difficulty, origin, status, created_by)
		VALUES ($1, $2, 'Легаси 112', 1, 'manual', 'approved', $3)`, scenarioID, sourceKey, f.lesson.InstructorID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO scenario_versions (id, scenario_id, version, status, body, digest, difficulty, exercise_type, created_by, approved_by, approved_at)
		VALUES ($1, $2, 1, 'approved', $3, $4, 1, 'operator112_intake', $5, $5, now())`, versionID, scenarioID, bodyJSON, digest[:], f.lesson.InstructorID); err != nil {
		t.Fatal(err)
	}
	return versionID
}

// assignAndStart assigns versionID to the fixture's one trainee/
// workstation and starts the lesson, returning the operator principal
// and the resulting item id.
func (f operator112PipelineFixture) assignAndStart(t *testing.T, ctx context.Context, versionID uuid.UUID) (auth.Principal, uuid.UUID) {
	t.Helper()
	if _, err := f.trainingService.ReplaceAssignments(ctx, f.instructor, f.lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 1, UserID: f.trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}},
	}, "pipeline-assign"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.trainingService.Start(ctx, f.instructor, f.lesson.ID, "pipeline-start"); err != nil {
		t.Fatal(err)
	}
	operator := principal(f.trainee, f.workstation)
	items, err := f.trainingService.MyItems(ctx, operator)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v, %v", items, err)
	}
	return operator, items[0].ID
}

// commandSender is the same send/sendRejected pattern every operator112
// integration test in this package already uses (operator112_full_case_
// test.go, operator112_test.go), factored out once here since this file
// needs it in several tests.
type commandSender struct {
	t               *testing.T
	ctx             context.Context
	trainingService *training.Service
	operator        auth.Principal
	itemID          uuid.UUID
	seq             int64
}

func (s *commandSender) send(kind training.CommandType, body any) training.Receipt {
	s.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		s.t.Fatal(err)
	}
	receipt, err := s.trainingService.Execute(s.ctx, s.operator, s.itemID, training.Command{CommandID: uuid.New(), ExpectedSeq: s.seq, Type: kind, Payload: data}, "pipeline-command")
	if err != nil || receipt.Outcome != training.OutcomeApplied {
		s.t.Fatalf("%s: %+v, %v", kind, receipt, err)
	}
	s.seq = receipt.Seq
	return receipt
}

// closeFullCaseItem drives pilot-112-full-gas-road-traffic-fire-01 (the
// same scenario operator112_full_case_test.go uses) from ringing through
// notify_services/complete_intake — the thin reference this seed has
// (expected_types/expected_services only, no expected_card/expected_
// profiles) is deliberate: it is exactly the "no reference yet" shape
// ADR-026's own rule is about, and slice-112-6-plan.md's Verification
// section expects exactly this scenario for its own manual walkthrough.
func (f operator112PipelineFixture) closeFullCaseItem(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	versionID := f.versionBySourceKey(t, ctx, "pilot-112-full-gas-road-traffic-fire-01")
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
	s.send(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard})

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

// TestOperator112AssessmentAutoPipelineEndToEnd is c7's own counterpart
// to TestAssessmentAutoPipelineEndToEnd: close -> waiting -> coordinator
// seals assessment_inputs -> pending -> a claimed worker run produces
// exactly one auto rev=1, and — unlike DDS's JUDGE=off G_GRAMMAR, which
// makes every slice 6 auto needs_review by construction — this pipeline
// is fully deterministic, so status must be ready with a real numeric
// score, never needs_review just because the seed's own reference is
// thin.
func TestOperator112AssessmentAutoPipelineEndToEnd(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixture(t, ctx, "Конвейер оценки 112")
	taskStore := mustTaskEnqueuer(f.pool)
	assessmentService := newIntake112AssessmentServiceForTest(f.pool, taskStore)

	itemID := f.closeFullCaseItem(t, ctx)

	status, found := evaluateTaskStatus(t, ctx, f.pool, itemID)
	if !found || status != "waiting" {
		t.Fatalf("evaluate task status = %q, found=%v, want waiting", status, found)
	}
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	status, found = evaluateTaskStatus(t, ctx, f.pool, itemID)
	if !found || status != "pending" {
		t.Fatalf("evaluate task status after coordinator tick = %q, found=%v, want pending", status, found)
	}

	handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-112-1")
	if !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}

	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 {
		t.Fatalf("assessments for item = %d rows, want exactly 1 auto", len(rows))
	}
	auto := rows[0]
	if auto.Kind != "auto" || auto.Revision != 1 {
		t.Fatalf("auto assessment = %+v, want kind=auto revision=1", auto)
	}
	if auto.Status != "ready" || auto.Score == nil || auto.Passed == nil {
		t.Fatalf("auto assessment = %+v, want ready with a numeric score (this rubric has no llm criteria to force needs_review)", auto)
	}
	addr, ok := criterionByID(auto.Criteria, "ADDRESS_FIELDS")
	if !ok || addr.Status != assessment.CriterionNotMet {
		t.Fatalf("ADDRESS_FIELDS = %+v, want not_met (seed has no expected_card yet)", addr)
	}
	cards, ok := criterionByID(auto.Criteria, "PROFILE_CARDS")
	if !ok || cards.Status != assessment.CriterionNotMet {
		t.Fatalf("PROFILE_CARDS = %+v, want not_met (seed has no expected_profiles yet)", cards)
	}
	var rubricVersion string
	if err := f.pool.QueryRow(ctx, `SELECT rubric_version FROM assessments WHERE item_id=$1 AND kind='auto'`, itemID).Scan(&rubricVersion); err != nil {
		t.Fatal(err)
	}
	if rubricVersion != "operator112/rubric-v2" {
		t.Fatalf("auto.rubric_version = %q, want operator112/rubric-v2", rubricVersion)
	}
}

// TestOperator112AssessmentRetryDoesNotDuplicateAuto mirrors
// TestAssessmentEvaluateRetryDoesNotDuplicateAuto for a 112 item.
func TestOperator112AssessmentRetryDoesNotDuplicateAuto(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixture(t, ctx, "Повтор задачи 112")
	taskStore := mustTaskEnqueuer(f.pool)
	assessmentService := newIntake112AssessmentServiceForTest(f.pool, taskStore)

	itemID := f.closeFullCaseItem(t, ctx)
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-112-a"); !claimed || handleErr != nil {
		t.Fatalf("first Handle: claimed=%v err=%v", claimed, handleErr)
	}
	if rows := readAssessments(t, ctx, f.pool, itemID); len(rows) != 1 {
		t.Fatalf("assessments after first handle = %d, want 1", len(rows))
	}

	var taskID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE tasks SET status = 'pending', next_attempt_at = now(), terminal_worker = NULL, terminal_at = NULL, result = NULL
		WHERE id = $1
	`, taskID); err != nil {
		t.Fatal(err)
	}
	handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-112-b")
	if !claimed || handleErr != nil {
		t.Fatalf("redelivered Handle: claimed=%v err=%v", claimed, handleErr)
	}
	if rows := readAssessments(t, ctx, f.pool, itemID); len(rows) != 1 {
		t.Fatalf("assessments after redelivered handle = %d, want still 1 (no duplicate auto)", len(rows))
	}
}

// TestOperator112StopBeforeNotifyScoresFromFinalCard closes an item via
// lesson stop before notify_services ever ran (ADR-026 §2.7's own
// fallback) — the auto assessment must still come back ready (not
// needs_review just because notify never happened): T_FILL/P_SERVICES
// become not_applicable (milestone not reached), everything else scores
// off final_card as usual.
func TestOperator112StopBeforeNotifyScoresFromFinalCard(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixture(t, ctx, "Стоп до оповещения 112")
	taskStore := mustTaskEnqueuer(f.pool)
	assessmentService := newIntake112AssessmentServiceForTest(f.pool, taskStore)

	versionID := f.versionBySourceKey(t, ctx, "pilot-112-full-gas-road-traffic-fire-01")
	operator, itemID := f.assignAndStart(t, ctx, versionID)
	s := &commandSender{t: t, ctx: ctx, trainingService: f.trainingService, operator: operator, itemID: itemID}
	s.send(training.CommandOpen, map[string]any{})
	s.send(training.CommandAnswerIncoming, map[string]any{})
	s.send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "ask_address"})

	if _, err := f.trainingService.Stop(ctx, f.instructor, f.lesson.ID, nil, "pipeline-stop"); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.trainingService.CloseStoppedLesson(ctx, tx, f.lesson.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-112-stop")
	if !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 || rows[0].Status != "ready" {
		t.Fatalf("assessments after stop-before-notify = %+v, want exactly one ready auto", rows)
	}
	fill, ok := criterionByID(rows[0].Criteria, "T_FILL")
	if !ok || fill.Status != assessment.CriterionNotApplicable {
		t.Fatalf("T_FILL = %+v, want not_applicable (never notified)", fill)
	}
	services, ok := criterionByID(rows[0].Criteria, "P_SERVICES")
	if !ok || services.Status != assessment.CriterionNotApplicable {
		t.Fatalf("P_SERVICES = %+v, want not_applicable (never notified)", services)
	}
}

// TestOperator112LegacyRouteFailsWithManualReviewStillAvailable closes a
// pre-ADR-023 incoming_call item under a fresh rubric-v2 lesson: the
// evaluate task must fail with operator112_legacy_route rather than
// silently skip or crash, and manual review must remain fully available
// afterwards (RFC-001: a failed auto never blocks the instructor).
func TestOperator112LegacyRouteFailsWithManualReviewStillAvailable(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixture(t, ctx, "Легаси-маршрут 112")
	taskStore := mustTaskEnqueuer(f.pool)
	assessmentService := newIntake112AssessmentServiceForTest(f.pool, taskStore)

	versionID := f.insertLegacyIncomingCallVersion(t, ctx, "pipeline-legacy-incoming-call-review")
	operator, itemID := f.assignAndStart(t, ctx, versionID)
	s := &commandSender{t: t, ctx: ctx, trainingService: f.trainingService, operator: operator, itemID: itemID}
	s.send(training.CommandOpen, map[string]any{})
	s.send(training.CommandAnswerIncoming, map[string]any{})
	f.fillMinimalDraft(t, ctx, operator, s)
	s.send(training.CommandDispatchIntake, map[string]string{"service_code": "pilot_ambulance"})
	s.send(training.CommandEndIncoming, map[string]any{})
	s.send(training.CommandCompleteIntake, map[string]any{})

	status, found := evaluateTaskStatus(t, ctx, f.pool, itemID)
	if !found || status != "waiting" {
		t.Fatalf("evaluate task status = %q, found=%v, want waiting", status, found)
	}
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)

	var taskStatus string
	var errorCode *string
	if err := f.pool.QueryRow(ctx, `SELECT status, last_error_code FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&taskStatus, &errorCode); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "failed" || errorCode == nil || *errorCode != "operator112_legacy_route" {
		t.Fatalf("legacy-route task = status=%q code=%v, want failed/operator112_legacy_route", taskStatus, errorCode)
	}
	if rows := readAssessments(t, ctx, f.pool, itemID); len(rows) != 0 {
		t.Fatalf("assessments for a legacy-route item = %+v, want none (no auto ever produced)", rows)
	}

	created, err := assessmentService.CreateExpertRevision(ctx, itemID, f.lesson.InstructorID, assessment.RevisionInput{
		BaseRevision: 0, Reason: "Ручной разбор легаси-маршрута",
		Criteria: []assessment.CriterionResult{
			{ID: "ADDRESS_FIELDS", Status: assessment.CriterionMet},
			{ID: "PROFILE_CARDS", Status: assessment.CriterionNotApplicable},
			{ID: "CALLER_TOPICS", Status: assessment.CriterionMet},
			{ID: "T_ANSWER", Status: assessment.CriterionMet},
			{ID: "T_FILL", Status: assessment.CriterionNotApplicable},
			{ID: "DESCRIPTION_PRESENT", Status: assessment.CriterionMet},
			{ID: "P_ADDRESS_REGION", PenaltyPoints: floatPtr(0)},
			{ID: "P_APPLICANT_NAME", PenaltyPoints: floatPtr(0)},
			{ID: "P_SERVICES", Status: assessment.CriterionNotApplicable},
			{ID: "P_EXTRA_PROFILE", PenaltyPoints: floatPtr(0)},
		},
	}, "pipeline-legacy-review")
	if err != nil || created.Revision != 2 || created.Kind != assessment.KindExpert {
		t.Fatalf("manual review of legacy-route item: %+v, %v", created, err)
	}
}

// TestOperator112ExpertRevisionAfterAutoTakesPriority mirrors RFC-001
// §7.4's "итоговая — последняя expert" for a 112 item that did get a
// real auto: the expert revision becomes final, and the auto row is
// left untouched underneath it (the same coexistence
// TestAssessmentExpertRevisionAfterAutoRecordsTrainingExamples checks
// for DDS).
func TestOperator112ExpertRevisionAfterAutoTakesPriority(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixture(t, ctx, "Эксперт после авто 112")
	taskStore := mustTaskEnqueuer(f.pool)
	assessmentService := newIntake112AssessmentServiceForTest(f.pool, taskStore)

	itemID := f.closeFullCaseItem(t, ctx)
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-112-final"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	auto := readAssessments(t, ctx, f.pool, itemID)[0]

	overrideScore := 55.0
	created, err := assessmentService.CreateExpertRevision(ctx, itemID, f.lesson.InstructorID, assessment.RevisionInput{
		BaseRevision: 1, Reason: "Преподаватель не согласен с автооценкой", ScoreOverride: &overrideScore,
		Criteria: resolveAllCriteria(auto.Criteria),
	}, "pipeline-expert-after-auto")
	if err != nil || created.Revision != 2 || created.Kind != assessment.KindExpert || created.Score == nil || *created.Score != 55 {
		t.Fatalf("expert revision: %+v, %v", created, err)
	}

	detail, err := assessmentService.Get(ctx, itemID)
	if err != nil || detail.Final == nil || detail.Final.Kind != assessment.KindExpert || detail.Final.Revision != 2 {
		t.Fatalf("Get after expert revision: %+v, %v", detail, err)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 2 || rows[0].Kind != "auto" || rows[1].Kind != "expert" {
		t.Fatalf("assessments after expert revision = %+v, want [auto, expert]", rows)
	}
}

// TestOperator112RubricV1LessonDoesNotEnqueueEvaluate confirms a lesson
// still frozen on the pre-112-6 manual-only rubric (simulated here by
// updating the row directly, since CreateLesson itself can no longer
// produce one — c4's own change) keeps its old no-auto-evaluation
// behavior unchanged.
func TestOperator112RubricV1LessonDoesNotEnqueueEvaluate(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixture(t, ctx, "Занятие на rubric-v1")
	if _, err := f.pool.Exec(ctx, `UPDATE lessons SET rubric_version = 'operator112/rubric-v1' WHERE id = $1`, f.lesson.ID); err != nil {
		t.Fatal(err)
	}

	versionID := f.insertLegacyIncomingCallVersion(t, ctx, "pipeline-legacy-incoming-call-v1lesson")
	operator, itemID := f.assignAndStart(t, ctx, versionID)
	s := &commandSender{t: t, ctx: ctx, trainingService: f.trainingService, operator: operator, itemID: itemID}
	s.send(training.CommandOpen, map[string]any{})
	s.send(training.CommandAnswerIncoming, map[string]any{})
	f.fillMinimalDraft(t, ctx, operator, s)
	s.send(training.CommandDispatchIntake, map[string]string{"service_code": "pilot_ambulance"})
	s.send(training.CommandEndIncoming, map[string]any{})
	s.send(training.CommandCompleteIntake, map[string]any{})

	if _, found := evaluateTaskStatus(t, ctx, f.pool, itemID); found {
		t.Fatal("a rubric-v1 lesson's closed item must not get an assessment.evaluate task at all")
	}
}

func floatPtr(f float64) *float64 { return &f }
