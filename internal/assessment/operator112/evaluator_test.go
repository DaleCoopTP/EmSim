package operator112

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"

	"github.com/google/uuid"
)

// testRubric is a minimal operator112/rubric-v2-shaped Rubric with the
// same rule/params wiring as design-docs/contracts/rubric.operator112.
// json, kept local to this package's tests (testdata, not seed/) so a
// change to the real embedded rubric never silently changes what these
// table tests assert.
func testRubric() assessment.Rubric {
	return assessment.Rubric{
		Schema: "emsim/rubric/v1", ID: "test", Version: "operator112/rubric-v2",
		PassThreshold: 70, CriticalCap: 100, ExerciseType: content.ExerciseTypeOperator112Intake,
		Criteria: []assessment.RubricCriterion{
			{ID: "ADDRESS_FIELDS", Kind: "deterministic", Weight: 35, Rule: "operator112_address_fields", Params: map[string]any{
				"fields": []any{
					map[string]any{"path": "city", "label": "Город", "points": 10.0},
					map[string]any{"path": "street", "label": "Улица", "points": 15.0},
					map[string]any{"path": "house", "label": "Дом", "points": 10.0},
				},
			}},
			{ID: "PROFILE_CARDS", Kind: "deterministic", Weight: 25, Rule: "operator112_profile_cards", Params: map[string]any{}},
			{ID: "P_ADDRESS_REGION", Kind: "penalty", Rule: "operator112_penalty_address_region", Params: map[string]any{"points": 10.0}},
			{ID: "P_APPLICANT_NAME", Kind: "penalty", Rule: "operator112_penalty_applicant_name", Params: map[string]any{"points": 5.0}},
			{ID: "P_SERVICES", Kind: "penalty", Rule: "operator112_penalty_services", Params: map[string]any{"points_per": 5.0}},
			{ID: "P_EXTRA_PROFILE", Kind: "penalty", Rule: "operator112_penalty_extra_profile", Params: map[string]any{"points_per": 5.0}},
		},
	}
}

func testCatalog() *content.IntakeCatalog {
	return &content.IntakeCatalog{
		Version: 1,
		Types:   []content.IntakeIncidentType{{ID: "gas_explosion", Name: "Взрыв газа", ProfileIDs: []string{"104"}}},
		Profiles: []content.IntakeProfile{{
			ID: "104", Version: 1, Name: "Газовая служба",
			Fields: []content.IntakeProfileField{
				{ID: "smell", Label: "Запах газа", Kind: "single", Options: []string{"yes", "no"}},
				{ID: "signs", Label: "Признаки", Kind: "multiple", Options: []string{"hissing", "smell", "visible_leak"}},
			},
		}},
	}
}

func fullReference() content.Intake112Reference {
	return content.Intake112Reference{
		ExpectedTypes: []string{"gas_explosion"}, CaseDescription: "test",
		ExpectedServices: []string{"pilot_gas_104"},
		ExpectedCard: &content.Intake112ExpectedCard{
			ApplicantName: "Иванов Иван Иванович",
			Address:       content.Intake112Address{City: "Москва", Street: "Тверская", House: "1"},
		},
		ExpectedProfiles: map[string]map[string]content.Intake112ExpectedProfileValue{
			"104": {"smell": {Value: "yes"}},
		},
	}
}

func baseBody(ref content.Intake112Reference) content.Body {
	return content.Body{
		ExerciseType: content.ExerciseTypeOperator112Intake,
		Intake112: &content.Intake112{
			Mode: "full_case",
			Dialogue: &content.Intake112Dialogue{
				Facts: []content.Intake112Fact{
					{ID: "applicant_name_fact", Label: "ФИО", CardPath: "/applicant_name", Knowledge: "on_question", Value: "Иванов Иван Иванович"},
				},
			},
			Reference: ref,
		},
	}
}

func knownField(v string) training.IntakeField { return training.IntakeField{State: "known", Value: v} }

func correctCard() training.IntakeCard {
	card := training.UnansweredIntakeCard("112-1", "+79161234567", "12:00", "Europe/Moscow")
	card.ApplicantName = knownField("Иванов Иван Иванович")
	card.Address.City = knownField("Москва")
	card.Address.Street = knownField("Тверская")
	card.Address.House = knownField("1")
	card.Profiles = map[string]training.IntakeProfile{
		"104": {DefinitionID: "104", Version: 1, Answers: map[string]training.IntakeProfileAnswer{
			"smell": {State: "known", Value: "yes"},
		}},
	}
	return card
}

func baseEvidence(catalog *content.IntakeCatalog, card training.IntakeCard, notify bool, revealApplicantName bool) trainingintake.EvidenceBody {
	itemID, actionID := uuid.New(), uuid.New()
	transcript := []training.IntakeLine{}
	if revealApplicantName {
		transcript = append(transcript, training.IntakeLine{Reveals: []string{"applicant_name_fact"}})
	}
	ev := trainingintake.EvidenceBody{
		Schema: "operator112_intake/v1", ExerciseType: content.ExerciseTypeOperator112Intake,
		ItemID: itemID, ClosedAt: time.Now(), Mode: "full_case", FinalCard: card,
		IntakeState: training.IntakeState{Finale: "notify", Catalog: catalog, Transcript: transcript},
	}
	if notify {
		services := []training.IntakeNotificationService{{ServiceCode: "pilot_gas_104", Suggested: true}}
		ev.Notification = &training.IntakeNotification{ItemID: itemID, ActionID: actionID, Services: services, CardSnapshot: card, NotifiedAt: time.Now()}
	}
	return ev
}

func mustEvaluate(t *testing.T, ev trainingintake.EvidenceBody, body content.Body, rubric assessment.Rubric) []assessment.CriterionResult {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	results, err := Evaluator.Evaluate(raw, body, rubric)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return results
}

func findResult(t *testing.T, results []assessment.CriterionResult, id string) assessment.CriterionResult {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result for %q", id)
	return assessment.CriterionResult{}
}

func TestEvaluateFullyCorrectPass(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	results := mustEvaluate(t, ev, body, testRubric())

	addr := findResult(t, results, "ADDRESS_FIELDS")
	if addr.Status != assessment.CriterionMet || addr.Score == nil || *addr.Score != 1 {
		t.Fatalf("ADDRESS_FIELDS = %+v, want met/1", addr)
	}
	cards := findResult(t, results, "PROFILE_CARDS")
	if cards.Status != assessment.CriterionMet || cards.Score == nil || *cards.Score != 1 {
		t.Fatalf("PROFILE_CARDS = %+v, want met/1", cards)
	}
	for _, id := range []string{"P_ADDRESS_REGION", "P_APPLICANT_NAME", "P_SERVICES", "P_EXTRA_PROFILE"} {
		p := findResult(t, results, id)
		if p.PenaltyPoints == nil || *p.PenaltyPoints != 0 {
			t.Fatalf("%s = %+v, want 0 penalty on a fully correct pass", id, p)
		}
	}
	score := assessment.Score(results, testRubric())
	if score.Status != assessment.StatusReady || score.Score == nil || *score.Score != 100 {
		t.Fatalf("overall score = %+v, want ready/100", score)
	}
}

func TestEvaluateWrongAddress(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	card := correctCard()
	card.Address.Street = knownField("Садовая")
	ev := baseEvidence(testCatalog(), card, true, false)
	results := mustEvaluate(t, ev, body, testRubric())

	addr := findResult(t, results, "ADDRESS_FIELDS")
	// city(10)+house(10) correct out of 35 total -> 20/35.
	if addr.Status != assessment.CriterionPartial || addr.Score == nil {
		t.Fatalf("ADDRESS_FIELDS = %+v, want partial", addr)
	}
	want := 20.0 / 35.0
	if diff := *addr.Score - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("ADDRESS_FIELDS.score = %v, want %v", *addr.Score, want)
	}
	street := findDetail(t, addr.Details, "street")
	if street.Status != assessment.CriterionNotMet {
		t.Fatalf("street detail = %+v, want not_met", street)
	}
}

func TestEvaluateMissingAndExtraService(t *testing.T) {
	ref := fullReference()
	ref.ExpectedServices = []string{"pilot_gas_104", "pilot_fire_101"}
	body := baseBody(ref)
	card := correctCard()
	ev := baseEvidence(testCatalog(), card, false, true)
	ev.Notification = &training.IntakeNotification{
		ItemID: ev.ItemID, ActionID: uuid.New(), CardSnapshot: card, NotifiedAt: time.Now(),
		Services: []training.IntakeNotificationService{{ServiceCode: "pilot_gas_104"}, {ServiceCode: "pilot_ambulance"}},
	}
	results := mustEvaluate(t, ev, body, testRubric())

	services := findResult(t, results, "P_SERVICES")
	// Missing pilot_fire_101, extra pilot_ambulance -> 2 * 5 = 10.
	if services.PenaltyPoints == nil || *services.PenaltyPoints != 10 {
		t.Fatalf("P_SERVICES = %+v, want penalty 10", services)
	}
	if len(services.Details) != 2 {
		t.Fatalf("P_SERVICES details = %+v, want 2 entries", services.Details)
	}
}

func TestEvaluateExtraProfileCard(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	card := correctCard()
	card.Profiles["101"] = training.IntakeProfile{DefinitionID: "101", Version: 1, Answers: map[string]training.IntakeProfileAnswer{}}
	catalog := testCatalog()
	catalog.Types = append(catalog.Types, content.IntakeIncidentType{ID: "road_traffic_fire", Name: "ДТП", ProfileIDs: []string{"101"}})
	catalog.Profiles = append(catalog.Profiles, content.IntakeProfile{ID: "101", Version: 1, Name: "Пожарная служба", Fields: []content.IntakeProfileField{{ID: "x", Label: "x", Kind: "text"}}})
	ev := baseEvidence(catalog, card, true, false)
	ev.Notification.CardSnapshot = card
	results := mustEvaluate(t, ev, body, testRubric())

	extra := findResult(t, results, "P_EXTRA_PROFILE")
	if extra.PenaltyPoints == nil || *extra.PenaltyPoints != 5 {
		t.Fatalf("P_EXTRA_PROFILE = %+v, want penalty 5", extra)
	}
	if len(extra.Details) != 1 || extra.Details[0].Key != "101" {
		t.Fatalf("P_EXTRA_PROFILE details = %+v, want [101]", extra.Details)
	}
}

func TestEvaluateEmptyReference(t *testing.T) {
	ref := content.Intake112Reference{ExpectedTypes: []string{"gas_explosion"}, CaseDescription: "test", ExpectedServices: []string{"pilot_gas_104"}}
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	results := mustEvaluate(t, ev, body, testRubric())

	addr := findResult(t, results, "ADDRESS_FIELDS")
	if addr.Status != assessment.CriterionNotMet || addr.Score == nil || *addr.Score != 0 {
		t.Fatalf("ADDRESS_FIELDS with no expected_card = %+v, want not_met/0", addr)
	}
	cards := findResult(t, results, "PROFILE_CARDS")
	if cards.Status != assessment.CriterionNotMet || cards.Score == nil || *cards.Score != 0 {
		t.Fatalf("PROFILE_CARDS with no expected_profiles = %+v, want not_met/0", cards)
	}
	name := findResult(t, results, "P_APPLICANT_NAME")
	if name.PenaltyPoints == nil || *name.PenaltyPoints != 0 {
		t.Fatalf("P_APPLICANT_NAME with no reference = %+v, want 0 penalty, not needs_review", name)
	}
	// No criterion here is unavailable/needs_review just because the
	// reference is empty — Score must still produce a ready numeric score.
	score := assessment.Score(results, testRubric())
	if score.Status != assessment.StatusReady {
		t.Fatalf("score.status = %v, want ready even with an empty reference (ADR-026)", score.Status)
	}
}

func TestEvaluatePartialReferenceFieldIsNotApplicableWithinBlock(t *testing.T) {
	ref := fullReference()
	ref.ExpectedCard.Address.House = "" // only city/street remain referenced
	body := baseBody(ref)
	card := correctCard()
	card.Address.House = knownField("99") // wrong, but unscored since absent from reference
	ev := baseEvidence(testCatalog(), card, true, true)
	results := mustEvaluate(t, ev, body, testRubric())

	addr := findResult(t, results, "ADDRESS_FIELDS")
	if addr.Status != assessment.CriterionMet || addr.Score == nil || *addr.Score != 1 {
		t.Fatalf("ADDRESS_FIELDS = %+v, want met/1 (house unscored, city+street correct)", addr)
	}
	house := findDetail(t, addr.Details, "house")
	if house.Status != assessment.CriterionNotApplicable {
		t.Fatalf("house detail = %+v, want not_applicable", house)
	}
}

func TestEvaluateStopBeforeNotifyUsesFinalCard(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	card := correctCard()
	ev := baseEvidence(testCatalog(), card, false, true) // no Notification at all
	results := mustEvaluate(t, ev, body, testRubric())

	addr := findResult(t, results, "ADDRESS_FIELDS")
	if addr.Status != assessment.CriterionMet {
		t.Fatalf("ADDRESS_FIELDS from final_card = %+v, want met", addr)
	}
	services := findResult(t, results, "P_SERVICES")
	if services.Status != assessment.CriterionNotApplicable {
		t.Fatalf("P_SERVICES without notify = %+v, want not_applicable (milestone not reached)", services)
	}
	if services.PenaltyPoints != nil {
		t.Fatalf("P_SERVICES without notify must not set penalty_points, got %v", *services.PenaltyPoints)
	}
}

func TestEvaluateLegacyRouteFails(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	ev.IntakeState.Finale = "" // pre-ADR-023 item
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Evaluator.Evaluate(raw, body, testRubric())
	if err == nil {
		t.Fatal("Evaluate on a legacy-route item must error")
	}
	var term *assessment.TerminalEvaluationError
	if !errors.As(err, &term) {
		t.Fatalf("err = %v, want *assessment.TerminalEvaluationError", err)
	}
	if term.Code != "operator112_legacy_route" {
		t.Fatalf("code = %q, want operator112_legacy_route", term.Code)
	}
}

func TestEvaluateDeterministic(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Evaluator.Evaluate(raw, body, testRubric())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluator.Evaluate(raw, body, testRubric())
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("two Evaluate runs over the same input diverged:\n%s\nvs\n%s", firstJSON, secondJSON)
	}
}

func findDetail(t *testing.T, details []assessment.CriterionDetail, key string) assessment.CriterionDetail {
	t.Helper()
	for _, d := range details {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("no detail for key %q among %+v", key, details)
	return assessment.CriterionDetail{}
}
