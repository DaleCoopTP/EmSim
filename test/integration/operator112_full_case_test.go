//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestOperator112FullCase is slice 112-4's end-to-end vertical slice
// (ADR-023, slice-112-4-plan.md): a caller conversation (112-2) and
// profile cards (112-3) in one item, finished through the single
// notify_services/complete_intake pair rather than a per-service
// dispatch. It also checks that the pre-ADR-023 incoming_call route
// (dispatch_intake) keeps working unchanged in the same lesson.
func TestOperator112FullCase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	authStore := authpg.NewStore(pool)
	actor := insertInstructor(t, ctx, pool, "full-case-instructor-"+uuid.NewString())
	adminID, adminRole := createContentAdmin(t, ctx, authStore, "full-case-admin-"+uuid.NewString())
	contentService := content.NewService(contentpg.NewStore(pool), mustValidator(t))
	if _, err := contentService.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), adminID, adminRole, "full-case-services"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportIntakeCatalog(ctx, openSeedFile(t, "../../seed/intake-catalog.json"), adminID, adminRole, "full-case-catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportClassifierTypes(ctx, openSeedFile(t, "../../seed/classifier.json"), adminID, adminRole, "full-case-classifier"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportScenarios(ctx, openScenarioDir(t, "../../seed/scenarios"), adminID, adminRole, "full-case-scenarios"); err != nil {
		t.Fatal(err)
	}
	trainingService := newTrainingService(pool)

	evidenceSchemaJSON, err := os.ReadFile("../../design-docs/contracts/evidence.operator112.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	evidenceSchemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(evidenceSchemaJSON))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const evidenceSchemaID = "https://emsim.local/schemas/evidence/operator112/v1"
	if err := compiler.AddResource(evidenceSchemaID, evidenceSchemaDoc); err != nil {
		t.Fatal(err)
	}
	evidenceSchema, err := compiler.Compile(evidenceSchemaID)
	if err != nil {
		t.Fatal(err)
	}

	lesson, err := trainingService.CreateLesson(ctx, principal(actor, uuid.Nil), training.LessonCreate{
		ExerciseType: content.ExerciseTypeOperator112Intake, Title: "Полный кейс 112-4", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "full-case-lesson")
	if err != nil {
		t.Fatal(err)
	}

	traineeData := newTrainee("full-case-trainee-"+uuid.NewString(), "")
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
	operator := principal(trainee, workstation)

	var versionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT sv.id FROM scenario_versions sv JOIN scenarios s ON s.id=sv.scenario_id WHERE s.source_key=$1 AND sv.version=1`,
		"pilot-112-full-gas-road-traffic-fire-01").Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := trainingService.ReplaceAssignments(ctx, principal(actor, uuid.Nil), lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 1, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}},
	}, "full-case-assign"); err != nil {
		t.Fatal(err)
	}
	started, err := trainingService.Start(ctx, principal(actor, uuid.Nil), lesson.ID, "full-case-start")
	if err != nil || started.IntakeCatalogVersion == nil {
		t.Fatalf("start: %+v, %v", started, err)
	}

	items, err := trainingService.MyItems(ctx, operator)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v, %v", items, err)
	}
	itemID := items[0].ID
	if items[0].IntakeState.Mode != "full_case" || items[0].IntakeState.Finale != "notify" || items[0].IntakeState.CallStatus != "ringing" {
		t.Fatalf("initial full_case state: %+v", items[0].IntakeState)
	}

	seq := int64(0)
	send := func(kind training.CommandType, body any) training.Receipt {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := trainingService.Execute(ctx, operator, itemID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq, Type: kind, Payload: data}, "full-case-command")
		if err != nil || receipt.Outcome != training.OutcomeApplied {
			t.Fatalf("%s: %+v, %v", kind, receipt, err)
		}
		seq = receipt.Seq
		return receipt
	}
	sendRejected := func(kind training.CommandType, body any) training.Receipt {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := trainingService.Execute(ctx, operator, itemID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq, Type: kind, Payload: data}, "full-case-command-rejected")
		if err != nil || receipt.Outcome != training.OutcomeRejected {
			t.Fatalf("%s: expected rejection, got %+v, %v", kind, receipt, err)
		}
		return receipt
	}

	send(training.CommandOpen, map[string]any{})
	sendRejected(training.CommandDispatchIntake, map[string]string{"service_code": "pilot_gas_104"})
	send(training.CommandAnswerIncoming, map[string]any{})
	send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "ask_address"})
	send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "clarify_house"})
	send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "ask_victims"})
	send(training.CommandAddIncidentType, map[string]string{"type_id": "gas_explosion_road_traffic_fire"})

	item, _, _, err := trainingService.ItemForTrainee(ctx, operator, itemID)
	if err != nil || len(item.IntakeCard.Profiles) != 2 {
		t.Fatalf("profiles: %+v, %v", item, err)
	}
	if len(item.IntakeState.Transcript) != 7 { // initial + 3 questions * 2 lines
		t.Fatalf("transcript: %+v", item.IntakeState.Transcript)
	}
	send(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard})

	item, _, _, err = trainingService.ItemForTrainee(ctx, operator, itemID)
	if err != nil {
		t.Fatal(err)
	}
	selected := make([]string, 0, len(item.IntakeState.SuggestedServices))
	for _, s := range item.IntakeState.SuggestedServices {
		selected = append(selected, s.ServiceCode)
	}
	if len(selected) != 2 {
		t.Fatalf("suggested services: %+v", item.IntakeState.SuggestedServices)
	}

	sendRejected(training.CommandCompleteIntake, map[string]any{})
	send(training.CommandNotifyServices, map[string]any{"services": selected, "reason": ""})
	sendRejected(training.CommandCompleteIntake, map[string]any{}) // call not ended yet
	sendRejected(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard})
	send(training.CommandEndIncoming, map[string]any{})
	send(training.CommandCompleteIntake, map[string]any{})

	var notificationCount, dispatchCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM intake_notifications WHERE item_id=$1`, itemID).Scan(&notificationCount); err != nil || notificationCount != 1 {
		t.Fatalf("notification count: %d, %v", notificationCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM intake_dispatches WHERE item_id=$1`, itemID).Scan(&dispatchCount); err != nil || dispatchCount != 0 {
		t.Fatalf("dispatch count: %d, %v", dispatchCount, err)
	}

	var evidenceJSON []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, itemID).Scan(&evidenceJSON); err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(evidenceJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := evidenceSchema.Validate(doc); err != nil {
		t.Fatalf("operator112 full_case evidence violates schema: %v", err)
	}
	var evidence struct {
		FinalCard    training.IntakeCard          `json:"final_card"`
		IntakeState  training.IntakeState         `json:"intake_state"`
		Dispatch     *training.IntakeDispatch     `json:"dispatch"`
		Notification *training.IntakeNotification `json:"notification"`
	}
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence.FinalCard.Profiles) != 2 || evidence.Dispatch != nil ||
		evidence.Notification == nil || len(evidence.Notification.Services) != 2 ||
		len(evidence.IntakeState.Transcript) != 7 {
		t.Fatalf("evidence: %+v", evidence)
	}
}
