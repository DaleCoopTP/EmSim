//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

func TestOperator112ProfileCases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	authStore := authpg.NewStore(pool)
	actor := insertInstructor(t, ctx, pool, "profiles-instructor-"+uuid.NewString())
	adminID, adminRole := createContentAdmin(t, ctx, authStore, "profiles-admin-"+uuid.NewString())
	contentService := content.NewService(contentpg.NewStore(pool), mustValidator(t))
	if _, err := contentService.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), adminID, adminRole, "profile-services"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportIntakeCatalog(ctx, openSeedFile(t, "../../seed/intake-catalog.json"), adminID, adminRole, "profile-catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportClassifierTypes(ctx, openSeedFile(t, "../../seed/classifier.json"), adminID, adminRole, "profile-classifier"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportScenarios(ctx, openScenarioDir(t, "../../seed/scenarios"), adminID, adminRole, "profile-scenarios"); err != nil {
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
	validateEvidence := func(raw []byte) {
		t.Helper()
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := evidenceSchema.Validate(doc); err != nil {
			t.Fatalf("operator112 evidence violates schema: %v", err)
		}
	}
	lesson, err := trainingService.CreateLesson(ctx, principal(actor, uuid.Nil), training.LessonCreate{
		ExerciseType: content.ExerciseTypeOperator112Intake, Title: "Карты 104 и 101", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "profile-lesson")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		key, typeID                string
		profileCount, serviceCount int
	}{
		{"pilot-112-gas-explosion-01", "gas_explosion", 1, 1},
		{"pilot-112-road-traffic-fire-01", "road_traffic_fire", 1, 2},
		{"pilot-112-gas-road-traffic-fire-01", "gas_explosion_road_traffic_fire", 2, 3},
	}
	assignments := make([]training.AssignmentInput, 0, len(tests))
	operators := make([]auth.Principal, 0, len(tests))
	for i, tc := range tests {
		user := newTrainee("profile-trainee-"+uuid.NewString(), "")
		user.ServiceCode = nil
		var trainee auth.User
		if err := authStore.WithTx(ctx, func(tx pgx.Tx) error { var err error; trainee, err = authStore.InsertUser(ctx, tx, user); return err }); err != nil {
			t.Fatal(err)
		}
		ws := insertWorkstation(t, ctx, pool, i+1)
		var versionID uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT sv.id FROM scenario_versions sv JOIN scenarios s ON s.id=sv.scenario_id WHERE s.source_key=$1 AND sv.version=1`, tc.key).Scan(&versionID); err != nil {
			t.Fatal(err)
		}
		assignments = append(assignments, training.AssignmentInput{WorkstationNo: i + 1, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}})
		operators = append(operators, principal(trainee, ws))
	}
	// A 112 assignment can contain several ordered cases, including repeats.
	assignments[0].ScenarioVersionIDs = append(assignments[0].ScenarioVersionIDs, assignments[1].ScenarioVersionIDs[0])
	stopUser := newTrainee("profile-stop-trainee-"+uuid.NewString(), "")
	stopUser.ServiceCode = nil
	var stopTrainee auth.User
	if err := authStore.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		stopTrainee, err = authStore.InsertUser(ctx, tx, stopUser)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stopWorkstation := insertWorkstation(t, ctx, pool, 4)
	assignments = append(assignments, training.AssignmentInput{WorkstationNo: 4, UserID: stopTrainee.ID,
		ScenarioVersionIDs: []uuid.UUID{assignments[0].ScenarioVersionIDs[0]}})
	stopOperator := principal(stopTrainee, stopWorkstation)
	if _, err := trainingService.ReplaceAssignments(ctx, principal(actor, uuid.Nil), lesson.ID, assignments, "profile-assign"); err != nil {
		t.Fatal(err)
	}
	seedCatalog, err := content.DecodeIntakeCatalog(openSeedFile(t, "../../seed/intake-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	started, err := trainingService.Start(ctx, principal(actor, uuid.Nil), lesson.ID, "profile-start")
	if err != nil || started.IntakeCatalogVersion == nil || *started.IntakeCatalogVersion != seedCatalog.Version {
		t.Fatalf("catalog pin: %+v, %v", started, err)
	}
	updatedCatalog := seedCatalog
	updatedCatalog.Types = append([]content.IntakeIncidentType(nil), seedCatalog.Types...)
	updatedCatalog.Version = seedCatalog.Version + 1
	updatedCatalog.Types[0].Name = "Обновлённое название для следующего занятия"
	updatedJSON, err := json.Marshal(updatedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportIntakeCatalog(ctx, bytes.NewReader(updatedJSON), adminID, adminRole, "profile-catalog-v2"); err != nil {
		t.Fatal(err)
	}
	for i, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			operator := operators[i]
			items, err := trainingService.MyItems(ctx, operator)
			if err != nil || len(items) != 1 {
				t.Fatalf("items: %+v, %v", items, err)
			}
			itemID := items[0].ID
			if items[0].IntakeState.Mode != "card_only" || len(items[0].IntakeCard.Profiles) != 0 || len(items[0].IntakeCard.IncidentTypes) != 0 || items[0].IntakeState.Catalog.Version != seedCatalog.Version {
				t.Fatalf("initial profile state: %+v", items[0])
			}
			if items[0].IntakeState.Catalog.Types[0].Name != "Взрыв газа" {
				t.Fatal("running item adopted a later catalog version")
			}
			seq := int64(0)
			send := func(kind training.CommandType, body any) training.Receipt {
				t.Helper()
				data, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := trainingService.Execute(ctx, operator, itemID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq, Type: kind, Payload: data}, "profile-command")
				if err != nil || receipt.Outcome != training.OutcomeApplied {
					t.Fatalf("%s: %+v, %v", kind, receipt, err)
				}
				seq = receipt.Seq
				return receipt
			}
			send(training.CommandOpen, map[string]any{})
			addData, _ := json.Marshal(map[string]string{"type_id": tc.typeID})
			addCommand := training.Command{CommandID: uuid.New(), ExpectedSeq: seq, Type: training.CommandAddIncidentType, Payload: addData}
			added, err := trainingService.Execute(ctx, operator, itemID, addCommand, "profile-add")
			if err != nil || added.Outcome != training.OutcomeApplied {
				t.Fatalf("add: %+v, %v", added, err)
			}
			seq = added.Seq
			replayed, err := trainingService.Execute(ctx, operator, itemID, addCommand, "profile-replay")
			if err != nil || !replayed.Replayed || replayed.Seq != seq {
				t.Fatalf("replay: %+v, %v", replayed, err)
			}
			stale, err := trainingService.Execute(ctx, operator, itemID, training.Command{CommandID: uuid.New(), ExpectedSeq: seq - 1, Type: training.CommandCompleteProfileCase, Payload: []byte(`{}`)}, "profile-stale")
			if err != nil || stale.Outcome != training.OutcomeRejected || stale.ErrorCode == nil || *stale.ErrorCode != training.RejectStaleSeq {
				t.Fatalf("stale: %+v, %v", stale, err)
			}
			item, _, _, err := trainingService.ItemForTrainee(ctx, operator, itemID)
			if err != nil || len(item.IntakeCard.Profiles) != tc.profileCount {
				t.Fatalf("profiles: %+v, %v", item, err)
			}
			if item.IntakeState.CallStatus != "not_applicable" || len(item.IntakeState.Transcript) != 0 {
				t.Fatal("unexpected conversation")
			}
			draft := *item.IntakeCard
			if i > 0 {
				profile := draft.Profiles["101"]
				profile.Answers["medical_help"] = training.IntakeProfileAnswer{State: "known", Value: "Да"}
				draft.Profiles["101"] = profile
			}
			send(training.CommandSaveIntakeDraft, map[string]any{"draft": draft})
			item, _, _, err = trainingService.ItemForTrainee(ctx, operator, itemID)
			if err != nil || len(item.IntakeState.SuggestedServices) != tc.serviceCount {
				t.Fatalf("services: %+v, %v", item.IntakeState, err)
			}
			selected := make([]string, 0, len(item.IntakeState.SuggestedServices))
			for _, suggestion := range item.IntakeState.SuggestedServices {
				selected = append(selected, suggestion.ServiceCode)
			}
			// ADR-023: every card_only item offered from this slice on
			// uses the single "оповестить и сохранить карточку" finale —
			// the legacy review_service_selection/complete_profile_case
			// pair is rejected once IntakeState.Finale is "notify".
			if item.IntakeState.Finale != "notify" {
				t.Fatalf("expected the notify finale on a freshly offered card_only item: %+v", item.IntakeState)
			}
			legacyData, _ := json.Marshal(map[string]any{"services": selected, "reason": ""})
			legacyReview, err := trainingService.Execute(ctx, operator, itemID, training.Command{
				CommandID: uuid.New(), ExpectedSeq: seq, Type: training.CommandReviewServices, Payload: legacyData,
			}, "profile-legacy-review")
			if err != nil || legacyReview.Outcome != training.OutcomeRejected || legacyReview.ErrorCode == nil || *legacyReview.ErrorCode != training.RejectTransitionNotAllowed {
				t.Fatalf("legacy review_service_selection must reject once finale is notify: %+v, %v", legacyReview, err)
			}
			send(training.CommandNotifyServices, map[string]any{"services": selected, "reason": ""})
			send(training.CommandCompleteIntake, map[string]any{})
			var dispatchCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM intake_dispatches WHERE item_id=$1`, itemID).Scan(&dispatchCount); err != nil || dispatchCount != 0 {
				t.Fatalf("dispatch count: %d, %v", dispatchCount, err)
			}
			var notificationCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM intake_notifications WHERE item_id=$1`, itemID).Scan(&notificationCount); err != nil || notificationCount != 1 {
				t.Fatalf("notification count: %d, %v", notificationCount, err)
			}
			var evidenceJSON []byte
			if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, itemID).Scan(&evidenceJSON); err != nil {
				t.Fatal(err)
			}
			validateEvidence(evidenceJSON)
			if i == 0 {
				invalidDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(evidenceJSON))
				if err != nil {
					t.Fatal(err)
				}
				invalidDoc.(map[string]any)["intake_state"].(map[string]any)["call_status"] = "connected"
				if err := evidenceSchema.Validate(invalidDoc); err == nil {
					t.Fatal("schema accepted a connected call in card_only evidence")
				}
				invalidDoc, err = jsonschema.UnmarshalJSON(bytes.NewReader(evidenceJSON))
				if err != nil {
					t.Fatal(err)
				}
				delete(invalidDoc.(map[string]any), "notification")
				if err := evidenceSchema.Validate(invalidDoc); err == nil {
					t.Fatal("schema accepted completed notify-finale card_only evidence without a notification record")
				}
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
			if len(evidence.FinalCard.Profiles) != tc.profileCount || evidence.Dispatch != nil ||
				evidence.Notification == nil || len(evidence.Notification.Services) != tc.serviceCount {
				t.Fatalf("evidence: %+v", evidence)
			}
			if i == 0 {
				nextItems, err := trainingService.MyItems(ctx, operator)
				if err != nil || len(nextItems) != 2 || nextItems[0].State != training.ItemClosed || nextItems[1].State != training.ItemOffered || nextItems[1].ScenarioVersionID != assignments[1].ScenarioVersionIDs[0] {
					t.Fatalf("next 112 case was not offered: %+v, %v", nextItems, err)
				}
				if nextItems[1].IntakeCard == nil || len(nextItems[1].IntakeCard.IncidentTypes) != 0 || len(nextItems[1].IntakeCard.Profiles) != 0 {
					t.Fatalf("next case inherited profile cards: %+v", nextItems[1].IntakeCard)
				}
			}
		})
	}
	stopItems, err := trainingService.MyItems(ctx, stopOperator)
	if err != nil || len(stopItems) != 1 {
		t.Fatalf("stop item: %+v, %v", stopItems, err)
	}
	stopItemID := stopItems[0].ID
	for seq, kind := range []training.CommandType{training.CommandOpen, training.CommandAddIncidentType} {
		body := []byte(`{}`)
		if kind == training.CommandAddIncidentType {
			body = []byte(`{"type_id":"gas_explosion"}`)
		}
		receipt, err := trainingService.Execute(ctx, stopOperator, stopItemID, training.Command{
			CommandID: uuid.New(), ExpectedSeq: int64(seq), Type: kind, Payload: body,
		}, "profile-before-stop")
		if err != nil || receipt.Outcome != training.OutcomeApplied {
			t.Fatalf("before stop: %+v, %v", receipt, err)
		}
	}
	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim worker: %v\n%s", err, output)
	}
	worker := startWorkerProcess(t, binary, databaseURL, "worker", "profile-close-worker")
	stopped, err := trainingService.Stop(ctx, principal(actor, uuid.Nil), lesson.ID, nil, "profile-stop")
	if err != nil || stopped.State != training.LessonStopped {
		t.Fatalf("stop: %+v, %v", stopped, err)
	}
	late, err := trainingService.Execute(ctx, stopOperator, stopItemID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 2, Type: training.CommandSaveIntakeDraft, Payload: []byte(`{}`),
	}, "profile-after-stop")
	if err != nil || late.Outcome != training.OutcomeRejected || late.ErrorCode == nil || *late.ErrorCode != training.RejectLessonStopped {
		t.Fatalf("late card-only command: %+v, %v", late, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var done bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM tasks t JOIN evidence e ON e.item_id=$2
			WHERE t.kind='lesson.close' AND t.scope_id=$1 AND t.status='done' AND t.terminal_worker='profile-close-worker'
		)`, lesson.ID, stopItemID).Scan(&done)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not seal stopped card-only evidence")
		}
		time.Sleep(20 * time.Millisecond)
	}
	worker.stop(t, false)
	var stoppedEvidence []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, stopItemID).Scan(&stoppedEvidence); err != nil {
		t.Fatal(err)
	}
	validateEvidence(stoppedEvidence)
	var stoppedBody struct {
		FinalCard training.IntakeCard `json:"final_card"`
	}
	if err := json.Unmarshal(stoppedEvidence, &stoppedBody); err != nil || len(stoppedBody.FinalCard.Profiles) != 1 {
		t.Fatalf("stopped evidence: %s, %v", stoppedEvidence, err)
	}
}
