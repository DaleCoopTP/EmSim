//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"

	"emsim/internal/assessment"
	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	pgstore "emsim/internal/platform/postgres"
	reportingpg "emsim/internal/reporting/postgres"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestOperator112IntakeTransaction(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	svc := newTrainingService(pool)
	actor := insertInstructor(t, ctx, pool, "112-instructor-"+uuid.NewString())
	traineeData := newTrainee("112-trainee-"+uuid.NewString(), "")
	traineeData.ServiceCode = nil
	var trainee auth.User
	store := authpg.NewStore(pool)
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		trainee, err = store.InsertUser(ctx, tx, traineeData)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	workstation := insertWorkstation(t, ctx, pool, 1)
	otherData := newTrainee("112-other-"+uuid.NewString(), "")
	otherData.ServiceCode = nil
	var other auth.User
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		other, err = store.InsertUser(ctx, tx, otherData)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	otherWorkstation := insertWorkstation(t, ctx, pool, 2)
	if _, err := pool.Exec(ctx, `INSERT INTO services (code, name, workflow) VALUES ('pilot_ambulance', 'Учебная скорая', '{}')`); err != nil {
		t.Fatal(err)
	}
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
	if _, err := pool.Exec(ctx, `INSERT INTO scenarios (id, source_key, title, difficulty, origin, status, created_by)
		VALUES ($1, 'pilot-112-medical-01', 'Пилот 112', 1, 'manual', 'approved', $2)`, scenarioID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO scenario_versions (id, scenario_id, version, status, body, digest, difficulty, exercise_type, created_by, approved_by, approved_at)
		VALUES ($1, $2, 1, 'approved', $3, $4, 1, 'operator112_intake', $5, $5, now())`, versionID, scenarioID, bodyJSON, digest[:], actor.ID); err != nil {
		t.Fatal(err)
	}
	instructor := principal(actor, uuid.Nil)
	lesson, err := svc.CreateLesson(ctx, instructor, training.LessonCreate{ExerciseType: content.ExerciseTypeOperator112Intake,
		Title: "Пилот 112", Mode: training.ModeTraining, Level: auth.LevelEasy}, "112-create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReplaceAssignments(ctx, instructor, lesson.ID, []training.AssignmentInput{{WorkstationNo: 1,
		UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}}, {WorkstationNo: 2,
		UserID: other.ID, ScenarioVersionIDs: []uuid.UUID{versionID}}}, "112-assign"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(ctx, instructor, lesson.ID, "112-start"); err != nil {
		t.Fatal(err)
	}
	operator := principal(trainee, workstation)
	items, err := svc.MyItems(ctx, operator)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v, %v", items, err)
	}
	item := items[0]
	if item.IntakeCard == nil || item.IntakeCard.Address.City.State != "unanswered" || item.IntakeState.CallStatus != "ringing" {
		t.Fatalf("initial item: %+v", item)
	}
	otherOperator := principal(other, otherWorkstation)
	otherItems, err := svc.MyItems(ctx, otherOperator)
	if err != nil || len(otherItems) != 1 || otherItems[0].ID == item.ID || otherItems[0].IntakeState.CallStatus != "ringing" {
		t.Fatalf("independent item: %+v, %v", otherItems, err)
	}
	otherItem := otherItems[0]
	command := func(typ training.CommandType, payload any, seq int64) training.Receipt {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		r, err := svc.Execute(ctx, operator, item.ID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq, Type: typ, Payload: raw}, "112-action")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := command(training.CommandOpen, map[string]any{}, 0); r.Outcome != training.OutcomeApplied {
		t.Fatalf("open: %+v", r)
	}
	if r := command(training.CommandAnswerIncoming, map[string]any{}, 1); r.Outcome != training.OutcomeApplied {
		t.Fatalf("answer: %+v", r)
	}
	loaded, _, _, err := svc.ItemForTrainee(ctx, operator, item.ID)
	if err != nil || len(loaded.IntakeState.Transcript) != 3 {
		t.Fatalf("transcript: %+v, %v", loaded, err)
	}
	otherBefore, _, _, err := svc.ItemForTrainee(ctx, otherOperator, otherItem.ID)
	if err != nil || len(otherBefore.IntakeState.Transcript) != 0 || otherBefore.IntakeCard.Address.City.State != "unanswered" {
		t.Fatalf("other trainee saw first call: %+v, %v", otherBefore, err)
	}
	draft := *loaded.IntakeCard
	draft.Age = training.IntakeField{State: "known", Value: "19"}
	draft.Address.City = training.IntakeField{State: "known", Value: "Москва"}
	if r := command(training.CommandSaveIntakeDraft, map[string]any{"draft": draft}, 2); r.Outcome != training.OutcomeApplied {
		t.Fatalf("save: %+v", r)
	}
	stale, err := svc.Execute(ctx, operator, item.ID, training.Command{CommandID: uuid.New(), ExpectedSeq: 2,
		Type: training.CommandDispatchIntake, Payload: []byte(`{"service_code":"pilot_ambulance"}`)}, "112-stale")
	if err != nil || stale.Outcome != training.OutcomeRejected || stale.ErrorCode == nil || *stale.ErrorCode != training.RejectStaleSeq {
		t.Fatalf("stale dispatch: %+v, %v", stale, err)
	}
	dispatchID := uuid.New()
	dispatchCommand := training.Command{CommandID: dispatchID, ExpectedSeq: 3, Type: training.CommandDispatchIntake,
		Payload: []byte(`{"service_code":"pilot_ambulance"}`)}
	r, err := svc.Execute(ctx, operator, item.ID, dispatchCommand, "112-dispatch")
	if err != nil || r.Outcome != training.OutcomeApplied {
		t.Fatalf("dispatch: %+v, %v", r, err)
	}
	replay, err := svc.Execute(ctx, operator, item.ID, dispatchCommand, "112-replay")
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	var dispatchCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM intake_dispatches WHERE item_id = $1`, item.ID).Scan(&dispatchCount); err != nil || dispatchCount != 1 {
		t.Fatalf("dispatch count %d: %v", dispatchCount, err)
	}
	if r := command(training.CommandEndIncoming, map[string]any{}, 4); r.Outcome != training.OutcomeApplied {
		t.Fatalf("end: %+v", r)
	}
	if r := command(training.CommandCompleteIntake, map[string]any{}, 5); r.Outcome != training.OutcomeApplied {
		t.Fatalf("complete: %+v", r)
	}
	var evidenceJSON []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id = $1`, item.ID).Scan(&evidenceJSON); err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Schema   string                   `json:"schema"`
		Dispatch *training.IntakeDispatch `json:"dispatch"`
	}
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Schema != "operator112_intake/v1" || evidence.Dispatch == nil || evidence.Dispatch.CardSnapshot.Age.Value != "19" {
		t.Fatalf("evidence: %+v", evidence)
	}
	var autoCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE kind = 'assessment.evaluate'`).Scan(&autoCount); err != nil || autoCount != 0 {
		t.Fatalf("auto tasks %d: %v", autoCount, err)
	}
	preReviewHistory, err := reportingpg.NewStore(pool).ResultsFor(ctx, trainee.ID, content.ExerciseTypeOperator112Intake)
	if err != nil || len(preReviewHistory) != 1 || preReviewHistory[0].AssessmentStatus != "pending" {
		t.Fatalf("112 awaiting manual review: %+v, %v", preReviewHistory, err)
	}
	assessmentService := newAssessmentServiceForTest(pool, mustTaskEnqueuer(pool))
	before, err := assessmentService.Get(ctx, item.ID)
	if err != nil || before.Final != nil || before.RubricEffective.Version != "operator112/rubric-v1" {
		t.Fatalf("review before manual rating: %+v, %v", before, err)
	}
	created, err := assessmentService.CreateExpertRevision(ctx, item.ID, actor.ID, assessment.RevisionInput{
		BaseRevision: 0, Reason: "Разбор учебного вызова",
		Criteria: []assessment.CriterionResult{
			{ID: "INTAKE_COMPLETENESS", Status: assessment.CriterionPartial, Explanation: "Адрес заполнен не полностью"},
			{ID: "INTAKE_ACCURACY", Status: assessment.CriterionMet, Explanation: "Внесённые сведения соответствуют разговору"},
			{ID: "INTAKE_DISPATCH", Status: assessment.CriterionMet, Explanation: "Карточка направлена учебной скорой"},
		},
	}, "112-assess")
	if err != nil || created.Revision != 2 || created.Score == nil || *created.Score != 80 {
		t.Fatalf("manual rating: %+v, %v", created, err)
	}
	after, err := assessmentService.Get(ctx, item.ID)
	if err != nil || after.Final == nil || after.Final.Kind != assessment.KindExpert {
		t.Fatalf("review after manual rating: %+v, %v", after, err)
	}
	rawReview, ok := after.Evidence.(json.RawMessage)
	if !ok || !json.Valid(rawReview) || !containsJSONKey(rawReview, "dispatch") {
		t.Fatalf("112 evidence in review: %T %s", after.Evidence, rawReview)
	}
	reports := reportingpg.NewStore(pool)
	ddsHistory, err := reports.Results(ctx, trainee.ID)
	if err != nil || len(ddsHistory) != 0 {
		t.Fatalf("DDS history mixed with 112: %+v, %v", ddsHistory, err)
	}
	intakeHistory, err := reports.ResultsFor(ctx, trainee.ID, content.ExerciseTypeOperator112Intake)
	if err != nil || len(intakeHistory) != 1 || intakeHistory[0].Score == nil || *intakeHistory[0].Score != 80 {
		t.Fatalf("112 history: %+v, %v", intakeHistory, err)
	}
	progress, err := reports.ProgressFor(ctx, trainee.ID, content.ExerciseTypeOperator112Intake)
	if err != nil || progress.CompletedItems != 1 || progress.AvgScore == nil || *progress.AvgScore != 80 {
		t.Fatalf("112 progress: %+v, %v", progress, err)
	}
	otherCommand := func(typ training.CommandType, payload any, seq int64) training.Receipt {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		r, err := svc.Execute(ctx, otherOperator, otherItem.ID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq,
			Type: typ, Payload: raw}, "112-other-action")
		if err != nil || r.Outcome != training.OutcomeApplied {
			t.Fatalf("other command %s: %+v, %v", typ, r, err)
		}
		return r
	}
	otherCommand(training.CommandOpen, map[string]any{}, 0)
	otherCommand(training.CommandAnswerIncoming, map[string]any{}, 1)
	otherLoaded, _, _, err := svc.ItemForTrainee(ctx, otherOperator, otherItem.ID)
	if err != nil || len(otherLoaded.IntakeState.Transcript) != 3 {
		t.Fatalf("other transcript: %+v, %v", otherLoaded, err)
	}
	otherDraft := *otherLoaded.IntakeCard
	otherDraft.Age = training.IntakeField{State: "known", Value: "20"}
	otherCommand(training.CommandSaveIntakeDraft, map[string]any{"draft": otherDraft}, 2)
	recoveryID := uuid.New()
	affected, err := svc.Recover(ctx, recoveryID, "server_restart")
	if err != nil || len(affected) != 1 || affected[0] != otherItem.ID {
		t.Fatalf("recover 112 draft: %+v, %v", affected, err)
	}
	if repeated, err := svc.Recover(ctx, recoveryID, "server_restart"); err != nil || len(repeated) != 0 {
		t.Fatalf("repeated recovery: %+v, %v", repeated, err)
	}
	otherAfterRecovery, _, _, err := svc.ItemForTrainee(ctx, otherOperator, otherItem.ID)
	if err != nil || otherAfterRecovery.IntakeCard.Age.Value != "20" || len(otherAfterRecovery.IntakeState.Transcript) != 3 || len(otherAfterRecovery.Interruptions) != 1 {
		t.Fatalf("112 draft after recovery: %+v, %v", otherAfterRecovery, err)
	}
	firstAfter, _, _, err := svc.ItemForTrainee(ctx, operator, item.ID)
	if err != nil || firstAfter.IntakeCard.Age.Value != "19" {
		t.Fatalf("second trainee changed first card: %+v, %v", firstAfter, err)
	}
	stopped, err := svc.Stop(ctx, instructor, lesson.ID, nil, "112-stop")
	if err != nil || stopped.State != training.LessonStopped {
		t.Fatalf("stop: %+v, %v", stopped, err)
	}
	late, err := svc.Execute(ctx, otherOperator, otherItem.ID, training.Command{CommandID: uuid.New(), ExpectedSeq: 3,
		Type: training.CommandDispatchIntake, Payload: []byte(`{"service_code":"pilot_ambulance"}`)}, "112-after-stop")
	if err != nil || late.Outcome != training.OutcomeRejected || late.ErrorCode == nil || *late.ErrorCode != training.RejectLessonStopped {
		t.Fatalf("dispatch after stop: %+v, %v", late, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := svc.CloseStoppedLesson(ctx, tx, lesson.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var otherEvidenceJSON []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, otherItem.ID).Scan(&otherEvidenceJSON); err != nil {
		t.Fatal(err)
	}
	var otherEvidence struct {
		Schema    string                   `json:"schema"`
		FinalCard training.IntakeCard      `json:"final_card"`
		Dispatch  *training.IntakeDispatch `json:"dispatch"`
	}
	if err := json.Unmarshal(otherEvidenceJSON, &otherEvidence); err != nil {
		t.Fatal(err)
	}
	if otherEvidence.Schema != "operator112_intake/v1" || otherEvidence.FinalCard.Age.Value != "20" || otherEvidence.Dispatch != nil {
		t.Fatalf("stopped item evidence: %+v", otherEvidence)
	}
}

func containsJSONKey(raw []byte, key string) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && len(value[key]) > 0
}
