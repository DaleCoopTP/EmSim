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
	noContactData := newTrainee("112-no-contact-"+uuid.NewString(), "")
	noContactData.ServiceCode = nil
	var noContactUser auth.User
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		noContactUser, err = store.InsertUser(ctx, tx, noContactData)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	noContactWorkstation := insertWorkstation(t, ctx, pool, 3)
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
		UserID: other.ID, ScenarioVersionIDs: []uuid.UUID{versionID}}, {WorkstationNo: 3,
		UserID: noContactUser.ID, ScenarioVersionIDs: []uuid.UUID{versionID}}}, "112-assign"); err != nil {
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
	draft.Address.Descriptive = training.IntakeField{State: "known", Value: "рядом с метро ВДНХ"}
	draft.OnSitePhone = training.IntakeField{State: "known", Value: "+79161313131"}
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
	if evidence.Schema != "operator112_intake/v1" || evidence.Dispatch == nil || evidence.Dispatch.CardSnapshot.Age.Value != "19" ||
		evidence.Dispatch.CardSnapshot.Address.Descriptive.Value != "рядом с метро ВДНХ" ||
		evidence.Dispatch.CardSnapshot.OnSitePhone.Value != "+79161313131" {
		t.Fatalf("evidence: %+v", evidence)
	}
	noContact := principal(noContactUser, noContactWorkstation)
	noContactItems, err := svc.MyItems(ctx, noContact)
	if err != nil || len(noContactItems) != 1 {
		t.Fatalf("no-contact item: %+v, %v", noContactItems, err)
	}
	noContactItem := noContactItems[0]
	if r, err := svc.Execute(ctx, noContact, noContactItem.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`)}, "112-no-contact-open"); err != nil || r.Outcome != training.OutcomeApplied {
		t.Fatalf("no-contact open: %+v, %v", r, err)
	}
	noContactCommand := training.Command{CommandID: uuid.New(), ExpectedSeq: 1,
		Type: training.CommandMarkNoContact, Payload: []byte(`{}`)}
	if r, err := svc.Execute(ctx, noContact, noContactItem.ID, noContactCommand, "112-no-contact-close"); err != nil || r.Outcome != training.OutcomeApplied {
		t.Fatalf("no-contact close: %+v, %v", r, err)
	}
	if r, err := svc.Execute(ctx, noContact, noContactItem.ID, noContactCommand, "112-no-contact-replay"); err != nil || !r.Replayed {
		t.Fatalf("no-contact replay: %+v, %v", r, err)
	}
	var noContactEvidence []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, noContactItem.ID).Scan(&noContactEvidence); err != nil {
		t.Fatal(err)
	}
	var exceptional struct {
		CloseReason training.CloseReason     `json:"close_reason"`
		Dispatch    *training.IntakeDispatch `json:"dispatch"`
	}
	if err := json.Unmarshal(noContactEvidence, &exceptional); err != nil || exceptional.CloseReason != training.CloseNoContact || exceptional.Dispatch != nil {
		t.Fatalf("no-contact evidence: %+v, %v", exceptional, err)
	}
	// 112-6/ADR-026: a lesson created today always freezes the current
	// operator112_intake rubric (rubric-v2), even for this test's own
	// pre-ADR-023 incoming_call/dispatch_intake items — enqueueEvaluateWaiting
	// can only tell a legacy-route item from a real one once evidence is
	// sealed (c5's operator112_legacy_route), not at enqueue time, so a
	// waiting assessment.evaluate task is expected for both closed items
	// (item via complete_intake, noContactItem via mark_no_contact) despite
	// neither ever getting a notify_services snapshot to score. otherItem
	// stays open, so it gets none.
	var autoCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE kind = 'assessment.evaluate'`).Scan(&autoCount); err != nil || autoCount != 2 {
		t.Fatalf("auto tasks %d: %v", autoCount, err)
	}
	preReviewHistory, err := reportingpg.NewStore(pool).ResultsFor(ctx, trainee.ID, content.ExerciseTypeOperator112Intake)
	if err != nil || len(preReviewHistory) != 1 || preReviewHistory[0].AssessmentStatus != "pending" {
		t.Fatalf("112 awaiting manual review: %+v, %v", preReviewHistory, err)
	}
	assessmentService := newAssessmentServiceForTest(pool, mustTaskEnqueuer(pool))
	before, err := assessmentService.Get(ctx, item.ID)
	if err != nil || before.Final != nil || before.RubricEffective.Version != "operator112/rubric-v2" {
		t.Fatalf("review before manual rating: %+v, %v", before, err)
	}
	zeroPenalty := 0.0
	created, err := assessmentService.CreateExpertRevision(ctx, item.ID, actor.ID, assessment.RevisionInput{
		BaseRevision: 0, Reason: "Разбор учебного вызова (легаси-маршрут dispatch_intake, без эталона)",
		Criteria: []assessment.CriterionResult{
			{ID: "ADDRESS_FIELDS", Status: assessment.CriterionMet, Explanation: "Адрес заполнен по разговору"},
			{ID: "PROFILE_CARDS", Status: assessment.CriterionNotApplicable, Explanation: "incoming_call без профильных карт"},
			{ID: "CALLER_TOPICS", Status: assessment.CriterionMet, Explanation: "Основные темы разговора затронуты"},
			{ID: "T_ANSWER", Status: assessment.CriterionMet, Explanation: "Вызов принят вовремя"},
			{ID: "T_FILL", Status: assessment.CriterionMet, Explanation: "Карточка заполнена вовремя"},
			{ID: "DESCRIPTION_PRESENT", Status: assessment.CriterionMet, Explanation: "Жалоба указана"},
			{ID: "P_ADDRESS_REGION", PenaltyPoints: &zeroPenalty},
			{ID: "P_APPLICANT_NAME", PenaltyPoints: &zeroPenalty},
			{ID: "P_SERVICES", PenaltyPoints: &zeroPenalty},
			{ID: "P_EXTRA_PROFILE", PenaltyPoints: &zeroPenalty},
		},
	}, "112-assess")
	if err != nil || created.Revision != 2 || created.Score == nil || *created.Score != 100 {
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
	if err != nil || len(intakeHistory) != 1 || intakeHistory[0].Score == nil || *intakeHistory[0].Score != 100 {
		t.Fatalf("112 history: %+v, %v", intakeHistory, err)
	}
	progress, err := reports.ProgressFor(ctx, trainee.ID, content.ExerciseTypeOperator112Intake)
	if err != nil || progress.CompletedItems != 1 || progress.AvgScore == nil || *progress.AvgScore != 100 {
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

func TestOperator112PreparedDialogueTransaction(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	svc := newTrainingService(pool)
	actor := insertInstructor(t, ctx, pool, "112-dialogue-instructor-"+uuid.NewString())
	users := authpg.NewStore(pool)
	operators := make([]auth.Principal, 2)
	for i := range operators {
		candidate := newTrainee("112-dialogue-trainee-"+uuid.NewString(), "")
		candidate.ServiceCode = nil
		var user auth.User
		if err := users.WithTx(ctx, func(tx pgx.Tx) error {
			var err error
			user, err = users.InsertUser(ctx, tx, candidate)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		operators[i] = principal(user, insertWorkstation(t, ctx, pool, 20+i))
	}
	if _, err := pool.Exec(ctx, `INSERT INTO services (code, name, workflow) VALUES ('pilot_ambulance', 'Учебная скорая', '{}')`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../seed/scenarios/pilot-112-medical-01-v2.json")
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
		VALUES ($1, $2, 2, 'approved', $3, $4, 1, 'operator112_intake', $5, $5, now())`, versionID, scenarioID, bodyJSON, digest[:], actor.ID); err != nil {
		t.Fatal(err)
	}
	instructor := principal(actor, uuid.Nil)
	lesson, err := svc.CreateLesson(ctx, instructor, training.LessonCreate{ExerciseType: content.ExerciseTypeOperator112Intake,
		Title: "Опрос 112", Mode: training.ModeTraining, Level: auth.LevelEasy}, "dialogue-create")
	if err != nil {
		t.Fatal(err)
	}
	assignments := make([]training.AssignmentInput, 2)
	for i, operator := range operators {
		assignments[i] = training.AssignmentInput{WorkstationNo: 20 + i, UserID: operator.UserID, ScenarioVersionIDs: []uuid.UUID{versionID}}
	}
	if _, err := svc.ReplaceAssignments(ctx, instructor, lesson.ID, assignments, "dialogue-assign"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(ctx, instructor, lesson.ID, "dialogue-start"); err != nil {
		t.Fatal(err)
	}
	items := make([]training.Item, 2)
	seq := []int64{0, 0}
	for i, operator := range operators {
		own, err := svc.MyItems(ctx, operator)
		if err != nil || len(own) != 1 {
			t.Fatalf("operator %d items: %+v, %v", i, own, err)
		}
		items[i] = own[0]
	}
	send := func(i int, typ training.CommandType, payload any) training.Receipt {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		r, err := svc.Execute(ctx, operators[i], items[i].ID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq[i], Type: typ, Payload: body}, "dialogue-action")
		if err != nil || r.Outcome != training.OutcomeApplied {
			t.Fatalf("operator %d command %s: %+v, %v", i, typ, r, err)
		}
		seq[i] = r.Seq
		return r
	}
	for i := range items {
		send(i, training.CommandOpen, map[string]any{})
		send(i, training.CommandAnswerIncoming, map[string]any{})
		loaded, _, _, err := svc.ItemForTrainee(ctx, operators[i], items[i].ID)
		if err != nil || len(loaded.IntakeState.Transcript) != 1 || len(loaded.AvailableQuestions) != 3 {
			t.Fatalf("initial projection for %d: %+v, %v", i, loaded, err)
		}
	}
	ask := func(i int, id string) training.Item {
		t.Helper()
		send(i, training.CommandAskIntakeQuestion, map[string]any{"question_id": id})
		loaded, _, _, err := svc.ItemForTrainee(ctx, operators[i], items[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		return loaded
	}
	firstAge := ask(0, "ask_age")
	firstAddress := ask(0, "ask_address")
	secondAddress := ask(1, "ask_address")
	secondAge := ask(1, "ask_age")
	if firstAge.IntakeState.Transcript[2].Text != secondAge.IntakeState.Transcript[4].Text ||
		firstAddress.IntakeState.Transcript[4].Text != secondAddress.IntakeState.Transcript[2].Text {
		t.Fatal("question order changed fixed applicant facts")
	}
	if len(firstAddress.AvailableQuestions) != 4 || len(secondAge.AvailableQuestions) != 4 {
		t.Fatal("clarifications were not unlocked")
	}
	// The same command ID returns its receipt and cannot append another turn.
	repeatCmd := training.Command{CommandID: uuid.New(), ExpectedSeq: seq[0], Type: training.CommandAskIntakeQuestion,
		Payload: []byte(`{"question_id":"ask_age"}`)}
	first, err := svc.Execute(ctx, operators[0], items[0].ID, repeatCmd, "dialogue-repeat")
	if err != nil || first.Outcome != training.OutcomeApplied {
		t.Fatalf("first repeat: %+v, %v", first, err)
	}
	seq[0] = first.Seq
	replayed, err := svc.Execute(ctx, operators[0], items[0].ID, repeatCmd, "dialogue-replay")
	if err != nil || !replayed.Replayed || replayed.Seq != first.Seq {
		t.Fatalf("command replay: %+v, %v", replayed, err)
	}
	loaded, _, _, err := svc.ItemForTrainee(ctx, operators[0], items[0].ID)
	if err != nil || len(loaded.IntakeState.Transcript) != 7 || len(loaded.IntakeState.AskedQuestionIDs) != 2 {
		t.Fatalf("replay appended a turn: %+v, %v", loaded, err)
	}
	stale, err := svc.Execute(ctx, operators[0], items[0].ID, training.Command{CommandID: uuid.New(), ExpectedSeq: 2,
		Type: training.CommandAskIntakeQuestion, Payload: []byte(`{"question_id":"ask_victims"}`)}, "dialogue-stale")
	if err != nil || stale.ErrorCode == nil || *stale.ErrorCode != training.RejectStaleSeq {
		t.Fatalf("stale question: %+v, %v", stale, err)
	}
	send(0, training.CommandHoldIncoming, map[string]any{})
	held, _, _, err := svc.ItemForTrainee(ctx, operators[0], items[0].ID)
	if err != nil || held.IntakeState.CallStatus != "held" || len(held.AvailableQuestions) != 0 {
		t.Fatalf("held call: %+v, %v", held, err)
	}
	blocked, err := svc.Execute(ctx, operators[0], items[0].ID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq[0],
		Type: training.CommandAskIntakeQuestion, Payload: []byte(`{"question_id":"clarify_house"}`)}, "dialogue-held-question")
	if err != nil || blocked.ErrorCode == nil || *blocked.ErrorCode != training.RejectTransitionNotAllowed {
		t.Fatalf("question on hold: %+v, %v", blocked, err)
	}
	send(0, training.CommandResumeIncoming, map[string]any{})
	loaded = ask(0, "clarify_house")
	if loaded.IntakeState.Transcript[len(loaded.IntakeState.Transcript)-1].Text != "Дом 2, корпус 3, рядом с метро ВДНХ." {
		t.Fatalf("clarification: %+v", loaded.IntakeState)
	}
	draft := *loaded.IntakeCard
	draft.Address.House = training.IntakeField{State: "known", Value: "2"}
	send(0, training.CommandSaveIntakeDraft, map[string]any{"draft": draft})
	send(0, training.CommandDispatchIntake, map[string]any{"service_code": "pilot_ambulance"})
	send(0, training.CommandEndIncoming, map[string]any{})
	send(0, training.CommandCompleteIntake, map[string]any{})
	if late, err := svc.Execute(ctx, operators[0], items[0].ID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq[0],
		Type: training.CommandAskIntakeQuestion, Payload: []byte(`{"question_id":"ask_victims"}`)}, "dialogue-ended-question"); err != nil || late.ErrorCode == nil || *late.ErrorCode != training.RejectItemClosed {
		t.Fatalf("question after close: %+v, %v", late, err)
	}
	var closedEvidence []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, items[0].ID).Scan(&closedEvidence); err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		IntakeState training.IntakeState     `json:"intake_state"`
		Dispatch    *training.IntakeDispatch `json:"dispatch"`
	}
	if err := json.Unmarshal(closedEvidence, &evidence); err != nil || evidence.Dispatch == nil ||
		evidence.Dispatch.CardSnapshot.Address.House.Value != "2" || len(evidence.IntakeState.Transcript) != 9 ||
		evidence.IntakeState.Transcript[1].Speaker != "operator" || evidence.IntakeState.Transcript[1].TopicID != "applicant" {
		t.Fatalf("dialogue evidence: %+v, %v", evidence, err)
	}
	// Recovery reconstructs the other participant's confirmed branch.
	recoveryID := uuid.New()
	if _, err := svc.Recover(ctx, recoveryID, "server_restart"); err != nil {
		t.Fatal(err)
	}
	other, _, _, err := svc.ItemForTrainee(ctx, operators[1], items[1].ID)
	if err != nil || len(other.IntakeState.Transcript) != 5 || len(other.AvailableQuestions) != 4 || len(other.Interruptions) != 1 {
		t.Fatalf("recovered dialogue: %+v, %v", other, err)
	}
	if _, err := svc.Stop(ctx, instructor, lesson.ID, nil, "dialogue-stop"); err != nil {
		t.Fatal(err)
	}
	stopped, _, _, err := svc.ItemForTrainee(ctx, operators[1], items[1].ID)
	if err != nil || len(stopped.AvailableQuestions) != 0 {
		t.Fatalf("questions after stop: %+v, %v", stopped, err)
	}
	late, err := svc.Execute(ctx, operators[1], items[1].ID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq[1],
		Type: training.CommandAskIntakeQuestion, Payload: []byte(`{"question_id":"clarify_house"}`)}, "dialogue-after-stop")
	if err != nil || late.ErrorCode == nil || *late.ErrorCode != training.RejectLessonStopped {
		t.Fatalf("question after stop: %+v, %v", late, err)
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
	var stoppedEvidence []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, items[1].ID).Scan(&stoppedEvidence); err != nil {
		t.Fatal(err)
	}
	var stoppedState struct {
		IntakeState training.IntakeState `json:"intake_state"`
	}
	if err := json.Unmarshal(stoppedEvidence, &stoppedState); err != nil || len(stoppedState.IntakeState.Transcript) != 5 {
		t.Fatalf("stop evidence lost or added a turn: %+v, %v", stoppedState, err)
	}
}
