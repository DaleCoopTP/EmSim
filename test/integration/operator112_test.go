//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	pgstore "emsim/internal/platform/postgres"
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
		UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}}}, "112-assign"); err != nil {
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
	draft := *loaded.IntakeCard
	draft.Age = training.IntakeField{State: "known", Value: "19"}
	draft.Address.City = training.IntakeField{State: "known", Value: "Москва"}
	if r := command(training.CommandSaveIntakeDraft, map[string]any{"draft": draft}, 2); r.Outcome != training.OutcomeApplied {
		t.Fatalf("save: %+v", r)
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
}
