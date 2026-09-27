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
			{ID: "CALLER_TOPICS", Kind: "deterministic", Weight: 15, Rule: "operator112_caller_topics", Params: map[string]any{
				"topics": []any{
					map[string]any{"id": "address", "label": "Адрес", "points": 7.5, "patterns": []any{"адрес", "где"}},
					map[string]any{"id": "victims", "label": "Пострадавшие", "points": 7.5, "patterns": []any{"пострадав"}},
				},
			}},
			{ID: "T_ANSWER", Kind: "deterministic", Weight: 7.5, Rule: "operator112_answer_timing", Params: map[string]any{"norm_s": 15.0}},
			{ID: "T_FILL", Kind: "deterministic", Weight: 7.5, Rule: "operator112_fill_timing", Params: map[string]any{"norm_s": 90.0}},
			{ID: "DESCRIPTION_PRESENT", Kind: "deterministic", Weight: 10, Rule: "operator112_description_present", Params: map[string]any{}},
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
				Initial: content.Intake112Utterance{ID: "greeting", Text: "Алло", Reveals: []string{}},
				Questions: []content.Intake112Question{
					{ID: "q_address", Text: "По какому адресу это произошло?", TopicID: "address", Answer: content.Intake112Utterance{ID: "a_address", Text: "Тверская 1", Reveals: []string{}}},
					{ID: "q_victims", Text: "Есть ли пострадавшие?", TopicID: "victims", Answer: content.Intake112Utterance{ID: "a_victims", Text: "Нет", Reveals: []string{}}},
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
	card.Complaint = knownField("Чувствуется сильный запах газа в квартире")
	card.Profiles = map[string]training.IntakeProfile{
		"104": {DefinitionID: "104", Version: 1, Answers: map[string]training.IntakeProfileAnswer{
			"smell": {State: "known", Value: "yes"},
		}},
	}
	return card
}

// baseEvidence builds evidence with every timing/topic/description
// dimension at a "fully correct" baseline: answered well inside the
// 15s norm, notified well inside the 90s fill norm, both scripted
// topics (address/victims) actually asked by the operator.
func baseEvidence(catalog *content.IntakeCatalog, card training.IntakeCard, notify bool, revealApplicantName bool) trainingintake.EvidenceBody {
	itemID, actionID := uuid.New(), uuid.New()
	offeredAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	answeredAt := offeredAt.Add(10 * time.Second)
	transcript := []training.IntakeLine{
		{ID: "op-1", Speaker: "operator", Text: "По какому адресу это произошло?", ServerAt: answeredAt.Add(1 * time.Second)},
		{ID: "op-2", Speaker: "operator", Text: "Есть ли пострадавшие?", ServerAt: answeredAt.Add(2 * time.Second)},
	}
	if revealApplicantName {
		transcript = append(transcript, training.IntakeLine{Reveals: []string{"applicant_name_fact"}})
	}
	ev := trainingintake.EvidenceBody{
		Schema: "operator112_intake/v1", ExerciseType: content.ExerciseTypeOperator112Intake,
		ItemID: itemID, OfferedAt: offeredAt, ClosedAt: offeredAt.Add(2 * time.Minute), Mode: "full_case", FinalCard: card,
		IntakeState: training.IntakeState{Finale: "notify", Catalog: catalog, Transcript: transcript, AnsweredAt: &answeredAt},
	}
	if notify {
		services := []training.IntakeNotificationService{{ServiceCode: "pilot_gas_104", Suggested: true}}
		ev.Notification = &training.IntakeNotification{
			ItemID: itemID, ActionID: actionID, Services: services, CardSnapshot: card,
			NotifiedAt: answeredAt.Add(60 * time.Second),
		}
	}
	return ev
}

func mustEvaluate(t *testing.T, ev trainingintake.EvidenceBody, body content.Body, rubric assessment.Rubric) []assessment.CriterionResult {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	results, err := Evaluator.Evaluate(raw, body, rubric, nil)
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
	_, err = Evaluator.Evaluate(raw, body, testRubric(), nil)
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
	first, err := Evaluator.Evaluate(raw, body, testRubric(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluator.Evaluate(raw, body, testRubric(), nil)
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

func TestEvaluateMissedTopic(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	// Only the address question was actually asked; drop the victims one.
	ev.IntakeState.Transcript = ev.IntakeState.Transcript[:1]
	results := mustEvaluate(t, ev, body, testRubric())

	topics := findResult(t, results, "CALLER_TOPICS")
	if topics.Status != assessment.CriterionPartial || topics.Score == nil || *topics.Score != 0.5 {
		t.Fatalf("CALLER_TOPICS = %+v, want partial/0.5 (address asked, victims missed)", topics)
	}
	address := findDetail(t, topics.Details, "address")
	if address.Status != assessment.CriterionMet {
		t.Fatalf("address topic detail = %+v, want met", address)
	}
	victims := findDetail(t, topics.Details, "victims")
	if victims.Status != assessment.CriterionNotMet {
		t.Fatalf("victims topic detail = %+v, want not_met", victims)
	}
}

func TestEvaluateTopicUnreachableInPreparedScriptIsNotApplicable(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	// Drop the victims question from the scenario's own script entirely -
	// the trainee cannot be faulted for not asking a question that does
	// not exist in a prepared dialogue.
	body.Intake112.Dialogue.Questions = body.Intake112.Dialogue.Questions[:1]
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	ev.IntakeState.Transcript = ev.IntakeState.Transcript[:1] // only the address question was asked
	results := mustEvaluate(t, ev, body, testRubric())

	topics := findResult(t, results, "CALLER_TOPICS")
	victims := findDetail(t, topics.Details, "victims")
	if victims.Status != assessment.CriterionNotApplicable {
		t.Fatalf("victims topic detail = %+v, want not_applicable (unreachable in this script)", victims)
	}
	// The block renormalizes over just the "address" topic -> fully met.
	if topics.Status != assessment.CriterionMet || topics.Score == nil || *topics.Score != 1 {
		t.Fatalf("CALLER_TOPICS = %+v, want met/1 after renormalizing over the one reachable topic", topics)
	}
}

func TestEvaluateCardOnlyHasNoConversationTopics(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	body.Intake112.Mode = "card_only"
	body.Intake112.Dialogue = nil
	ev := baseEvidence(testCatalog(), correctCard(), true, false)
	ev.Mode = "card_only"
	results := mustEvaluate(t, ev, body, testRubric())

	topics := findResult(t, results, "CALLER_TOPICS")
	if topics.Status != assessment.CriterionNotApplicable {
		t.Fatalf("CALLER_TOPICS for card_only = %+v, want not_applicable", topics)
	}
}

func TestEvaluateTimingBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		elapsed    time.Duration
		wantScore  float64
		wantStatus assessment.CriterionStatus
	}{
		{"at norm is fully met", 15 * time.Second, 1, assessment.CriterionMet},
		{"double norm is exactly zero", 30 * time.Second, 0, assessment.CriterionNotMet},
		{"beyond double norm clamps at zero", 60 * time.Second, 0, assessment.CriterionNotMet},
		{"halfway between norm and double is half credit", 22500 * time.Millisecond, 0.5, assessment.CriterionPartial},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := linearTimingScore(c.elapsed.Seconds(), 15)
			if diff := got - c.wantScore; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("linearTimingScore(%v, 15) = %v, want %v", c.elapsed, got, c.wantScore)
			}
		})
	}
}

func TestEvaluateAnswerTimingNotApplicableWhenNeverAnswered(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	ev.IntakeState.AnsweredAt = nil
	results := mustEvaluate(t, ev, body, testRubric())

	answer := findResult(t, results, "T_ANSWER")
	if answer.Status != assessment.CriterionNotApplicable {
		t.Fatalf("T_ANSWER = %+v, want not_applicable when the call was never answered", answer)
	}
	fill := findResult(t, results, "T_FILL")
	if fill.Status != assessment.CriterionNotApplicable {
		t.Fatalf("T_FILL = %+v, want not_applicable when there is no answered_at anchor", fill)
	}
}

func TestEvaluateFillTimingSubtractsCallerTurnWait(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	card := correctCard()
	ev := baseEvidence(testCatalog(), card, true, true)
	// Fill anchor is answeredAt+60s (met, well inside 90s). Add 40s of
	// caller-turn wait so the *raw* elapsed would blow past the 90s norm's
	// double (100s), but the trainee must not be penalized for it.
	requested := ev.IntakeState.AnsweredAt.Add(5 * time.Second)
	resolved := requested.Add(40 * time.Second)
	ev.IntakeState.CallerTurns = []training.IntakeCallerTurn{
		{Turn: 1, Status: training.CallerTurnAnswered, RequestedAt: requested, ResolvedAt: &resolved},
	}
	ev.Notification.NotifiedAt = ev.IntakeState.AnsweredAt.Add(95 * time.Second) // raw elapsed 95s > 90s norm
	results := mustEvaluate(t, ev, body, testRubric())

	fill := findResult(t, results, "T_FILL")
	// Effective elapsed = 95s - 40s = 55s, comfortably under the 90s norm.
	if fill.Status != assessment.CriterionMet {
		t.Fatalf("T_FILL = %+v, want met once caller-turn wait is subtracted", fill)
	}
}

// TestEvaluateFillTimingCountsOnlyWaitInsideFillWindow: only the part of
// a caller turn's wait that falls between answered_at and notified_at is
// subtracted — chatting on after "оповестить и сохранить" must not
// improve T_FILL (review 2026-09-26, item 3).
func TestEvaluateFillTimingCountsOnlyWaitInsideFillWindow(t *testing.T) {
	cases := []struct {
		name         string
		turns        func(answered, notified time.Time) []training.IntakeCallerTurn
		wantElapsedS float64
	}{
		{"no turns", func(answered, notified time.Time) []training.IntakeCallerTurn { return nil }, 150},
		{"wait entirely after notify is ignored", func(answered, notified time.Time) []training.IntakeCallerTurn {
			return []training.IntakeCallerTurn{
				resolvedTurn(1, notified.Add(5*time.Second), 55*time.Second),
				resolvedTurn(2, notified.Add(70*time.Second), 55*time.Second),
			}
		}, 150},
		{"wait entirely inside is subtracted", func(answered, notified time.Time) []training.IntakeCallerTurn {
			return []training.IntakeCallerTurn{resolvedTurn(1, answered.Add(10*time.Second), 30*time.Second)}
		}, 120},
		{"turn straddling notify counts up to notify", func(answered, notified time.Time) []training.IntakeCallerTurn {
			return []training.IntakeCallerTurn{resolvedTurn(1, notified.Add(-20*time.Second), 60*time.Second)}
		}, 130},
		{"turn still pending at snapshot counts up to notify", func(answered, notified time.Time) []training.IntakeCallerTurn {
			return []training.IntakeCallerTurn{{Turn: 1, Status: training.CallerTurnPending, RequestedAt: notified.Add(-25 * time.Second)}}
		}, 125},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := fullReference()
			body := baseBody(ref)
			ev := baseEvidence(testCatalog(), correctCard(), true, true)
			answered := *ev.IntakeState.AnsweredAt
			notified := answered.Add(150 * time.Second)
			ev.Notification.NotifiedAt = notified
			ev.IntakeState.CallerTurns = tc.turns(answered, notified)
			fill := findResult(t, mustEvaluate(t, ev, body, testRubric()), "T_FILL")
			want := linearTimingScore(tc.wantElapsedS, 90)
			if fill.Score == nil || *fill.Score != want {
				t.Fatalf("T_FILL = %+v, want score %.3f for %.0fs effective", fill, want, tc.wantElapsedS)
			}
		})
	}
}

func resolvedTurn(n int, requested time.Time, wait time.Duration) training.IntakeCallerTurn {
	resolved := requested.Add(wait)
	return training.IntakeCallerTurn{Turn: n, Status: training.CallerTurnAnswered, RequestedAt: requested, ResolvedAt: &resolved}
}

func TestEvaluateDescriptionAbsent(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	card := correctCard()
	card.Complaint = training.IntakeField{State: "unanswered"}
	ev := baseEvidence(testCatalog(), card, true, true)
	results := mustEvaluate(t, ev, body, testRubric())

	desc := findResult(t, results, "DESCRIPTION_PRESENT")
	if desc.Status != assessment.CriterionNotMet || desc.Score == nil || *desc.Score != 0 {
		t.Fatalf("DESCRIPTION_PRESENT = %+v, want not_met/0", desc)
	}
}

func TestEvaluateAIDivergenceMakesAddressUnavailable(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	body.Intake112.CallerMode = content.CallerModeFreeText
	card := correctCard()
	// The trainee wrote what the model actually said, which is not the
	// scenario author's own reference spelling.
	card.Address.Street = knownField("Садовое кольцо")
	ev := baseEvidence(testCatalog(), card, true, true)
	ev.IntakeState.Transcript = append(ev.IntakeState.Transcript, training.IntakeLine{
		Speaker: "caller", Text: "Адрес: Садовое кольцо, дом 1",
	})
	ev.IntakeState.CallerTurns = []training.IntakeCallerTurn{
		{Turn: 1, Status: training.CallerTurnAnswered, Source: training.CallerTurnSourceModel, RequestedAt: time.Now(), ResolvedAt: timePtr(time.Now())},
	}
	results := mustEvaluate(t, ev, body, testRubric())

	addr := findResult(t, results, "ADDRESS_FIELDS")
	if addr.Status != assessment.CriterionUnavailable {
		t.Fatalf("ADDRESS_FIELDS = %+v, want unavailable (AI-caller divergence)", addr)
	}
	score := assessment.Score(results, testRubric())
	if score.Status != assessment.StatusNeedsReview {
		t.Fatalf("overall score status = %v, want needs_review", score.Status)
	}
}

func TestEvaluateNoAIDivergenceWithoutModelTurn(t *testing.T) {
	// Same mismatch, but no model/fallback-sourced turn at all (e.g. a
	// prepared scenario, or a free_text one still answered by the stub) -
	// an ordinary wrong answer, not a divergence carve-out.
	ref := fullReference()
	body := baseBody(ref)
	body.Intake112.CallerMode = content.CallerModeFreeText
	card := correctCard()
	card.Address.Street = knownField("Садовое кольцо")
	ev := baseEvidence(testCatalog(), card, true, true)
	ev.IntakeState.Transcript = append(ev.IntakeState.Transcript, training.IntakeLine{
		Speaker: "caller", Text: "Мы на Садовом кольце",
	})
	ev.IntakeState.CallerTurns = []training.IntakeCallerTurn{
		{Turn: 1, Status: training.CallerTurnAnswered, Source: training.CallerTurnSourceStub, RequestedAt: time.Now(), ResolvedAt: timePtr(time.Now())},
	}
	results := mustEvaluate(t, ev, body, testRubric())

	addr := findResult(t, results, "ADDRESS_FIELDS")
	if addr.Status == assessment.CriterionUnavailable {
		t.Fatalf("ADDRESS_FIELDS = %+v, must not become unavailable for a stub-sourced turn", addr)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// TestEvaluateWorkedExample combines several dimensions in one item and
// checks the resulting total by hand: ADDRESS_FIELDS gets city+street
// right but house wrong (10+15)/35*35=25 of 35; PROFILE_CARDS fully
// correct (25); CALLER_TOPICS only the address topic asked (7.5 of 15,
// after renormalizing is 7.5/15*15=7.5... i.e. exactly half of the
// block, since both topics are reachable and worth equal points here);
// T_ANSWER/T_FILL/DESCRIPTION_PRESENT all met (7.5+7.5+10); one missing
// service (-5) and no other penalties. Total =
// 25 + 25 + 7.5 + 7.5 + 7.5 + 10 - 5 = 77.5.
func TestEvaluateWorkedExample(t *testing.T) {
	ref := fullReference()
	body := baseBody(ref)
	card := correctCard()
	card.Address.House = knownField("99") // wrong
	ev := baseEvidence(testCatalog(), card, true, true)
	ev.IntakeState.Transcript = ev.IntakeState.Transcript[:1] // victims topic not asked
	ev.Notification.Services = []training.IntakeNotificationService{}
	results := mustEvaluate(t, ev, body, testRubric())
	score := assessment.Score(results, testRubric())
	if score.Status != assessment.StatusReady || score.Score == nil {
		t.Fatalf("score = %+v, want a ready numeric score", score)
	}
	if *score.Score != 77.5 {
		t.Fatalf("total score = %v, want 77.5", *score.Score)
	}
}
