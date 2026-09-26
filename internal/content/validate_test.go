package content

import (
	"errors"
	"testing"
)

// fakeCatalog is an in-memory Catalog for Validate's tests — no pgx.Tx,
// no I/O, matching Catalog's doc comment that Validate itself performs
// none.
type fakeCatalog struct {
	services      map[string]ServiceRecord
	classifier    map[string]string // code -> name
	versions      map[string]ScenarioVersionReference
	intakeCatalog *IntakeCatalog
}

func (c fakeCatalog) Service(code string) (ServiceRecord, bool) {
	s, ok := c.services[code]
	return s, ok
}

func (c fakeCatalog) ClassifierType(code string) (string, bool) {
	name, ok := c.classifier[code]
	return name, ok
}

func (c fakeCatalog) ScenarioVersion(key string, version int) (ScenarioVersionReference, bool) {
	ref, ok := c.versions[scenarioVersionRefKey(key, version)]
	return ref, ok
}

func (c fakeCatalog) IntakeCatalog() (IntakeCatalog, bool) {
	if c.intakeCatalog == nil {
		return IntakeCatalog{}, false
	}
	return *c.intakeCatalog, true
}

// pilotWorkflow mirrors seed/services.json's minimal workflow
// (slice-2-plan.md's C3 seed plan): added -> received -> accepted|not_accepted,
// not_accepted -> accepted.
func pilotWorkflow() Workflow {
	return Workflow{
		Transitions: map[Reaction][]Reaction{
			ReactionAdded:       {ReactionReceived},
			ReactionReceived:    {ReactionAccepted, ReactionNotAccepted},
			ReactionNotAccepted: {ReactionAccepted},
		},
		CommentRequired: []Reaction{ReactionNotAccepted},
	}
}

func pilotCatalog() fakeCatalog {
	return fakeCatalog{
		services: map[string]ServiceRecord{
			"dds_district":            {Active: true, Workflow: pilotWorkflow()},
			"pilot_yuao_prefecture":   {Active: true, Workflow: pilotWorkflow()},
			"pilot_nature_department": {Active: true, Workflow: pilotWorkflow()},
		},
		classifier: map[string]string{
			"14080106": "Дерево упало во дворе",
		},
		versions: map[string]ScenarioVersionReference{},
	}
}

func validIntakeDialogueBody() Body {
	return Body{ExerciseType: ExerciseTypeOperator112Intake, Intake112: &Intake112{
		Call:              &Intake112Call{AON: "+79161313131", LocalTime: "02:03", TimeZone: "Europe/Moscow"},
		RecipientServices: []string{"pilot_ambulance"},
		Reference:         Intake112Reference{RecipientService: "pilot_ambulance", ExpectedCard: &Intake112ExpectedCard{}},
		Dialogue: &Intake112Dialogue{
			Facts: []Intake112Fact{
				{ID: "address_city", Label: "Город", CardPath: "/address/city", Knowledge: "initial", Value: "Москва"},
				{ID: "address_house", Label: "Дом", CardPath: "/address/house", Knowledge: "on_question", Value: "2"},
				{ID: "address_flat", Label: "Квартира", CardPath: "/address/flat", Knowledge: "unknown"},
			},
			Initial: Intake112Utterance{ID: "greeting", Text: "Я в Москве", Reveals: []string{"address_city"}},
			Questions: []Intake112Question{
				{ID: "where", Text: "Назовите дом", TopicID: "address", Answer: Intake112Utterance{ID: "where_answer", Text: "Дом 2", Reveals: []string{"address_house"}}},
				{ID: "flat", Text: "Назовите квартиру", TopicID: "address", AvailableAfter: []string{"where"}, Answer: Intake112Utterance{ID: "flat_answer", Text: "Не знаю", Reveals: []string{"address_flat"}}},
			},
		},
	}}
}

// validFullCaseBody is ADR-023's combined mode: 112-2's dialogue plus
// 112-3's incident types, finished through notify_services rather than
// a single recipient_services entry.
func validFullCaseBody() Body {
	body := validIntakeDialogueBody()
	body.Intake112.RecipientServices = nil
	body.Intake112.Mode = "full_case"
	body.Intake112.Reference = Intake112Reference{
		ExpectedTypes: []string{"gas_explosion"}, CaseDescription: "Запах газа в квартире",
		ExpectedServices: []string{"pilot_gas_104"},
	}
	return body
}

func TestValidateFullCase(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("valid full_case: %v", err)
	}
	for name, mutate := range map[string]func(*Body){
		"recipient_services present":  func(b *Body) { b.Intake112.RecipientServices = []string{"pilot_gas_104"} },
		"missing dialogue":            func(b *Body) { b.Intake112.Dialogue = nil },
		"legacy script":               func(b *Body) { b.Intake112.Call.Script = []string{"Здравствуйте"} },
		"recipient_service present":   func(b *Body) { b.Intake112.Reference.RecipientService = "pilot_gas_104" },
		"missing expected_types":      func(b *Body) { b.Intake112.Reference.ExpectedTypes = nil },
		"missing case_description":    func(b *Body) { b.Intake112.Reference.CaseDescription = "" },
		"missing expected_services":   func(b *Body) { b.Intake112.Reference.ExpectedServices = nil },
		"duplicate expected_services": func(b *Body) { b.Intake112.Reference.ExpectedServices = []string{"pilot_gas_104", "pilot_gas_104"} },
		"unknown expected_services":   func(b *Body) { b.Intake112.Reference.ExpectedServices = []string{"unknown_service"} },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := validFullCaseBody()
			mutate(&invalid)
			if err := Validate(invalid, catalog); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
	}
	t.Run("inactive expected_services", func(t *testing.T) {
		inactiveCatalog := pilotCatalog()
		inactiveCatalog.services["pilot_gas_104"] = ServiceRecord{Active: false}
		if err := Validate(validFullCaseBody(), inactiveCatalog); err == nil {
			t.Fatal("expected rejection for an inactive expected service")
		}
	})
	if err := Validate(validFullCaseBody(), catalog); err != nil {
		t.Fatalf("original body still valid after mutation subtests: %v", err)
	}
}

// validFreeTextFullCaseBody is 112-5a/ADR-024's caller_mode="free_text"
// shape: same full_case reference as validFullCaseBody, but the dialogue
// carries only facts — no scripted initial line or questions, since the
// trainee writes freely and a CallerReplier answers asynchronously.
func validFreeTextFullCaseBody() Body {
	body := validFullCaseBody()
	body.Intake112.CallerMode = CallerModeFreeText
	body.Intake112.Dialogue = &Intake112Dialogue{
		Facts: []Intake112Fact{
			{ID: "address_city", Label: "Город", CardPath: "/address/city", Knowledge: "initial", Value: "Москва"},
			{ID: "address_house", Label: "Дом", CardPath: "/address/house", Knowledge: "on_question", Value: "2"},
		},
	}
	return body
}

func TestValidateFreeTextFullCase(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFreeTextFullCaseBody()
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("valid free_text full_case: %v", err)
	}
	for name, mutate := range map[string]func(*Body){
		"invalid caller_mode": func(b *Body) { b.Intake112.CallerMode = "ai" },
		"initial present":     func(b *Body) { b.Intake112.Dialogue.Initial = Intake112Utterance{ID: "greeting", Text: "Алло"} },
		"questions present": func(b *Body) {
			b.Intake112.Dialogue.Questions = []Intake112Question{{ID: "q", Text: "?", TopicID: "t", Answer: Intake112Utterance{ID: "a", Text: "a"}}}
		},
		"no facts":            func(b *Body) { b.Intake112.Dialogue.Facts = nil },
		"duplicate fact id":   func(b *Body) { b.Intake112.Dialogue.Facts[1].ID = b.Intake112.Dialogue.Facts[0].ID },
		"duplicate card_path": func(b *Body) { b.Intake112.Dialogue.Facts[1].CardPath = b.Intake112.Dialogue.Facts[0].CardPath },
		"invalid card_path":   func(b *Body) { b.Intake112.Dialogue.Facts[0].CardPath = "/not/allowed" },
		"unknown fact w/ value": func(b *Body) {
			b.Intake112.Dialogue.Facts[0].Knowledge, b.Intake112.Dialogue.Facts[0].Value = "unknown", "x"
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := validFreeTextFullCaseBody()
			mutate(&invalid)
			if err := Validate(invalid, catalog); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
	}
	if err := Validate(validFreeTextFullCaseBody(), catalog); err != nil {
		t.Fatalf("original body still valid after mutation subtests: %v", err)
	}
}

// validAICallerFullCaseBody is 112-5b/ADR-025's own extension of
// validFreeTextFullCaseBody: a caller profile plus the per-fact fields
// the AI adapter reads (statement, ask_patterns, answer_variants,
// disclosure_patterns). A scenario without Caller (the plain
// validFreeTextFullCaseBody above) must stay valid too — see
// TestValidateFreeTextFullCase — since 112-5a scenarios predate this
// extension and are read unmodified.
func validAICallerFullCaseBody() Body {
	body := validFullCaseBody()
	body.Intake112.CallerMode = CallerModeFreeText
	body.Intake112.Dialogue = &Intake112Dialogue{
		Caller: &Intake112CallerProfile{
			Persona: "Женщина, 40 лет, испугана, но говорит связно.",
			Opening: Intake112Utterance{ID: "opening", Text: "Помогите, у нас в квартире пахнет газом!", Reveals: []string{"address_city"}},
		},
		Facts: []Intake112Fact{
			{
				ID: "address_city", Label: "Город", CardPath: "/address/city", Knowledge: "initial", Value: "Москва",
				Statement: "Мы в Москве", AskPatterns: []string{"город"}, DisclosurePatterns: []string{"москв"},
			},
			{
				ID: "age", Label: "Возраст", Knowledge: "on_question", Value: "40",
				Statement: "Мне сорок лет", AskPatterns: []string{"возраст", "сколько.*лет"}, AskExcludePatterns: []string{"сколько.*человек"},
				AnswerVariants:     []Intake112AnswerVariant{{When: "возраст", Text: "Мне сорок лет."}, {Text: "Сорок."}},
				DisclosurePatterns: []string{`\d{2}\s*лет`},
			},
		},
	}
	return body
}

func TestValidateAICallerProfile(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validAICallerFullCaseBody()
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("valid caller profile: %v", err)
	}
	if err := Validate(validFreeTextFullCaseBody(), catalog); err != nil {
		t.Fatalf("caller-less free_text scenario (112-5a shape) still valid: %v", err)
	}
	for name, mutate := range map[string]func(*Body){
		"missing persona": func(b *Body) { b.Intake112.Dialogue.Caller.Persona = "" },
		"missing opening text": func(b *Body) {
			b.Intake112.Dialogue.Caller.Opening = Intake112Utterance{ID: "opening"}
		},
		"opening reveals unknown fact": func(b *Body) {
			b.Intake112.Dialogue.Caller.Opening.Reveals = []string{"nope"}
		},
		"opening reveals on_question fact": func(b *Body) {
			b.Intake112.Dialogue.Caller.Opening.Reveals = []string{"age"}
		},
		"missing statement with caller profile": func(b *Body) {
			b.Intake112.Dialogue.Facts[1].Statement = ""
		},
		"invalid ask_pattern regex": func(b *Body) {
			b.Intake112.Dialogue.Facts[1].AskPatterns = []string{"("}
		},
		"word boundary in ask_pattern": func(b *Body) {
			b.Intake112.Dialogue.Facts[1].AskPatterns = []string{`\bвозраст\b`}
		},
		"word boundary in disclosure_pattern": func(b *Body) {
			b.Intake112.Dialogue.Facts[1].DisclosurePatterns = []string{`\bлет\b`}
		},
		"empty answer_variant text": func(b *Body) {
			b.Intake112.Dialogue.Facts[1].AnswerVariants = []Intake112AnswerVariant{{When: "x", Text: ""}}
		},
		"invalid answer_variant when regex": func(b *Body) {
			b.Intake112.Dialogue.Facts[1].AnswerVariants = []Intake112AnswerVariant{{When: "(", Text: "x"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := validAICallerFullCaseBody()
			mutate(&invalid)
			if err := Validate(invalid, catalog); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
	}
	if err := Validate(validAICallerFullCaseBody(), catalog); err != nil {
		t.Fatalf("original body still valid after mutation subtests: %v", err)
	}
}

func TestValidateRejectsCallerProfileFieldsInPreparedDialogue(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_ambulance"] = ServiceRecord{Active: true}
	for name, mutate := range map[string]func(*Body){
		"caller on prepared full_case": func(b *Body) {
			b.Intake112.Dialogue.Caller = &Intake112CallerProfile{Persona: "x", Opening: Intake112Utterance{ID: "o", Text: "x"}}
		},
		"statement on prepared fact":    func(b *Body) { b.Intake112.Dialogue.Facts[0].Statement = "x" },
		"ask_patterns on prepared fact": func(b *Body) { b.Intake112.Dialogue.Facts[0].AskPatterns = []string{"x"} },
		"answer_variants on prepared fact": func(b *Body) {
			b.Intake112.Dialogue.Facts[0].AnswerVariants = []Intake112AnswerVariant{{Text: "x"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := validIntakeDialogueBody()
			mutate(&body)
			if err := Validate(body, catalog); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
	}
}

func TestValidateRejectsCallerModeOutsideFullCase(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_ambulance"] = ServiceRecord{Active: true}
	body := validIntakeDialogueBody()
	body.Intake112.CallerMode = CallerModeFreeText
	if err := Validate(body, catalog); err == nil {
		t.Fatal("caller_mode on incoming_call accepted")
	}
}

func TestValidateIntakeDialogueReferencesAndCycles(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_ambulance"] = ServiceRecord{Active: true}
	body := validIntakeDialogueBody()
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("valid dialogue: %v", err)
	}
	body.Intake112.Dialogue.Questions[0].AvailableAfter = []string{"flat"}
	if err := Validate(body, catalog); err == nil {
		t.Fatal("question dependency cycle accepted")
	}
	body = validIntakeDialogueBody()
	body.Intake112.Dialogue.Questions[0].Answer.Reveals = []string{"hidden_fact"}
	if err := Validate(body, catalog); err == nil {
		t.Fatal("unknown fact reference accepted")
	}
}

// validPilotBody is case 2 from slice-2-plan.md: incoming card has the
// wrong округ (ЮАР), and reference.field_corrections says to fix it
// before "accepted" — Validate must accept this mismatch, not reject it.
func validPilotBody() Body {
	return Body{
		TargetService: "dds_district",
		Card: Card{
			Number:              "881412",
			RegisteredAtOffsetS: -60,
			Applicant:           Applicant{Name: "Иванов", Phone: "+79161234567", Status: "witness"},
			Address:             Address{District: "Чертаново Южное", Street: "Чертановская улица", House: "58", Okrug: "ЮАР"},
			Incident: Incident{
				TypeCode:    "14080106",
				TypeName:    "Дерево упало во дворе",
				Description: "Учебное описание происшествия для пилотного кейса.",
			},
			NotificationList: []NotificationEntry{
				{Service: "dds_district", Status: ReactionAdded, Mine: true},
				{Service: "pilot_yuao_prefecture", Status: ReactionAdded},
				{Service: "pilot_nature_department", Status: ReactionAdded},
			},
		},
		Reference: Reference{
			PrimaryDecision: PrimaryDecision{Status: ReactionAccepted},
			Call:            Call{Required: false},
			FieldCorrections: []FieldCorrection{
				{Path: "/card/address/okrug", ExpectedValue: "ЮАО", BeforeStatus: ReactionAccepted},
			},
			PilotGoal: "accept_card",
		},
		Difficulty:   1,
		ExerciseType: ExerciseTypeDDSProcessing,
	}
}

func TestValidateAcceptsPilotBody(t *testing.T) {
	if err := Validate(validPilotBody(), pilotCatalog()); err != nil {
		t.Fatalf("Validate(pilot body) = %v, want nil", err)
	}
}

func TestValidateAllowsExpectedValueToDifferFromCard(t *testing.T) {
	// The whole point of field_corrections: card.address.okrug ("ЮАР") and
	// expected_value ("ЮАО") deliberately disagree.
	body := validPilotBody()
	if body.Card.Address.Okrug == body.Reference.FieldCorrections[0].ExpectedValue {
		t.Fatalf("test setup: card and expected_value must differ")
	}
	if err := Validate(body, pilotCatalog()); err != nil {
		t.Fatalf("Validate must not require card value == expected_value: %v", err)
	}
}

func TestValidateRejectsUnknownTargetService(t *testing.T) {
	body := validPilotBody()
	body.TargetService = "no_such_service"
	assertInvalidField(t, Validate(body, pilotCatalog()), "target_service")
}

func TestValidateRejectsInactiveTargetService(t *testing.T) {
	body := validPilotBody()
	catalog := pilotCatalog()
	svc := catalog.services["dds_district"]
	svc.Active = false
	catalog.services["dds_district"] = svc
	assertInvalidField(t, Validate(body, catalog), "target_service")
}

func TestValidateRejectsUnknownNotificationService(t *testing.T) {
	body := validPilotBody()
	body.Card.NotificationList = append(body.Card.NotificationList, NotificationEntry{Service: "ghost_service"})
	assertInvalidField(t, Validate(body, pilotCatalog()), "card.notification_list[3].service")
}

func TestValidateRejectsZeroMine(t *testing.T) {
	body := validPilotBody()
	for i := range body.Card.NotificationList {
		body.Card.NotificationList[i].Mine = false
	}
	assertInvalidField(t, Validate(body, pilotCatalog()), "card.notification_list")
}

func TestValidateRejectsTwoMine(t *testing.T) {
	body := validPilotBody()
	body.Card.NotificationList = append(body.Card.NotificationList,
		NotificationEntry{Service: "dds_district", Mine: true})
	assertInvalidField(t, Validate(body, pilotCatalog()), "card.notification_list")
}

func TestValidateRejectsMineNotTargetService(t *testing.T) {
	body := validPilotBody()
	body.Card.NotificationList[0].Mine = false
	body.Card.NotificationList[1].Mine = true // pilot_yuao_prefecture, not target_service
	assertInvalidField(t, Validate(body, pilotCatalog()), "card.notification_list[1].mine")
}

func TestValidateRejectsUnknownClassifierCode(t *testing.T) {
	body := validPilotBody()
	body.Card.Incident.TypeCode = "00000000"
	assertInvalidField(t, Validate(body, pilotCatalog()), "card.incident.type_code")
}

func TestValidateRejectsClassifierNameMismatch(t *testing.T) {
	body := validPilotBody()
	body.Card.Incident.TypeName = "Другое название"
	assertInvalidField(t, Validate(body, pilotCatalog()), "card.incident.type_name")
}

func TestValidateRejectsDuplicateContactKeys(t *testing.T) {
	body := validPilotBody()
	body.Contacts = []Contact{{Key: "a"}, {Key: "a"}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "contacts[1].key")
}

func TestValidateRejectsDuplicateEventKeys(t *testing.T) {
	body := validPilotBody()
	body.Contacts = []Contact{{Key: "c1"}}
	body.Events = []Event{
		{Key: "e1", Since: "accepted", Delivery: "notice", From: "c1"},
		{Key: "e1", Since: "accepted", Delivery: "notice", From: "c1"},
	}
	assertInvalidField(t, Validate(body, pilotCatalog()), "events[1].key")
}

func TestValidateRejectsEventFromUnknownContact(t *testing.T) {
	body := validPilotBody()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "phone_incoming", From: "nobody"}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "events[0].from")
}

func TestValidateRejectsSpawnScenarioWithoutStableReference(t *testing.T) {
	body := validPilotBody()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario"}}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "events[0].spawn.scenario_key")
}

func TestValidateRejectsLegacyDuplicateSpawn(t *testing.T) {
	body := validPilotBody()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "duplicate", Variation: "legacy"}}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "events[0].spawn.kind")
}

func TestValidateRejectsSpawnScenarioWithUnknownStableReference(t *testing.T) {
	body := validPilotBody()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario", ScenarioKey: "missing", Version: 1}}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "events[0].spawn")
}

func TestValidateAcceptsCompatiblePublishedSpawnScenario(t *testing.T) {
	body := validPilotBody()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario", ScenarioKey: "spawn-target", Version: 1}}}
	catalog := pilotCatalog()
	catalog.versions[scenarioVersionRefKey("spawn-target", 1)] = ScenarioVersionReference{
		Status: "superseded", Published: true, ExerciseType: body.ExerciseType, TargetService: body.TargetService,
	}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("Validate(compatible spawn scenario) = %v, want nil", err)
	}
}

func TestValidateRejectsIncompatibleSpawnScenario(t *testing.T) {
	body := validPilotBody()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario", ScenarioKey: "spawn-target", Version: 1}}}

	tests := map[string]ScenarioVersionReference{
		"draft": {Status: "draft", ExerciseType: body.ExerciseType, TargetService: body.TargetService},
		"unapproved superseded": {
			Status: "superseded", ExerciseType: body.ExerciseType, TargetService: body.TargetService,
		},
		"exercise type": {
			Status: "approved", Published: true, ExerciseType: ExerciseType("operator112_intake"), TargetService: body.TargetService,
		},
		"target service": {Status: "approved", Published: true, ExerciseType: body.ExerciseType, TargetService: "another_service"},
	}
	for name, ref := range tests {
		t.Run(name, func(t *testing.T) {
			catalog := pilotCatalog()
			catalog.versions[scenarioVersionRefKey("spawn-target", 1)] = ref
			assertInvalidField(t, Validate(body, catalog), "events[0].spawn")
		})
	}
}

func TestValidateRejectsCallRequiredWithUnknownContact(t *testing.T) {
	body := validPilotBody()
	body.Reference.Call = Call{Required: true, To: "nobody"}
	assertInvalidField(t, Validate(body, pilotCatalog()), "reference.call.to")
}

func TestValidateAllowsCallNotRequiredWithoutContacts(t *testing.T) {
	body := validPilotBody() // contacts is empty, call.required is false
	if err := Validate(body, pilotCatalog()); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestValidateRejectsPrimaryDecisionUnreachable(t *testing.T) {
	body := validPilotBody()
	body.Reference.PrimaryDecision.Status = ReactionCompletedWithoutTeam
	assertInvalidField(t, Validate(body, pilotCatalog()), "reference.primary_decision.status")
}

func TestValidateRejectsExpectedChainNotAWorkflowEdge(t *testing.T) {
	body := validPilotBody()
	body.Reference.ExpectedChain = []Reaction{ReactionCompleted} // no accepted->completed edge in pilotWorkflow
	assertInvalidField(t, Validate(body, pilotCatalog()), "reference.expected_chain[0]")
}

func TestValidateRejectsFieldCorrectionBeforeStatusNotInWorkflow(t *testing.T) {
	body := validPilotBody()
	body.Reference.FieldCorrections[0].BeforeStatus = ReactionCompleted
	assertInvalidField(t, Validate(body, pilotCatalog()), "reference.field_corrections[0].before_status")
}

func TestValidateRejectsUnknownScoringCriterion(t *testing.T) {
	body := validPilotBody()
	body.Reference.Scoring = &Scoring{Critical: []string{"NO_SUCH_CRITERION"}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "reference.scoring.critical")
}

func TestValidateAllowsKnownScoringCriterion(t *testing.T) {
	body := validPilotBody()
	body.Reference.Scoring = &Scoring{Critical: []string{"D_PRIMARY"}, Disabled: []string{"C_CALL_MADE"}}
	if err := Validate(body, pilotCatalog()); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func assertInvalidField(t *testing.T, err error, wantField string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Validate = nil, want a ValidationError for field %q", wantField)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Validate error = %v (%T), want *ValidationError", err, err)
	}
	if ve.Field != wantField {
		t.Fatalf("Validate error field = %q, want %q (%v)", ve.Field, wantField, err)
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(err, ErrValidation) = false")
	}
}

// --- 112-6/ADR-026: intake112.reference extensions ---------------------

// pilotIntakeCatalog is a minimal single-profile intake catalog fixture
// for expected_profiles validation — mirrors seed/intake-catalog.json's
// shape (id/version/name/fields) without pulling in the real file.
func pilotIntakeCatalog() IntakeCatalog {
	return IntakeCatalog{
		Version: 1,
		Types:   []IntakeIncidentType{{ID: "gas_explosion", Name: "Взрыв газа", ProfileIDs: []string{"104"}}},
		Profiles: []IntakeProfile{{
			ID: "104", Version: 1, Name: "Газовая служба",
			Fields: []IntakeProfileField{
				{ID: "smell", Label: "Запах газа", Kind: "single", Options: []string{"yes", "no"}},
				{ID: "signs", Label: "Признаки", Kind: "multiple", Options: []string{"hissing", "smell", "visible_leak"}},
			},
		}},
	}
}

func TestValidateExpectedCardAllowedInFullCase(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.ExpectedCard = &Intake112ExpectedCard{
		ApplicantStatus: "witness", Address: Intake112Address{City: "Москва", Street: "Тверская"},
	}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("expected_card should be allowed for full_case since ADR-026: %v", err)
	}
}

func TestValidateExpectedCardContradictsFact(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	// validFullCaseBody's own dialogue fact for /address/city is "Москва".
	body.Intake112.Reference.ExpectedCard = &Intake112ExpectedCard{Address: Intake112Address{City: "Санкт-Петербург"}}
	assertInvalidField(t, Validate(body, catalog), "intake112.reference.expected_card")
}

func TestValidateExpectedCardAgreesWithFactAfterNormalization(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	// Same city, different token order/case — normalize.TokenSetEqual must
	// still accept it (the fact says "Москва").
	body.Intake112.Reference.ExpectedCard = &Intake112ExpectedCard{Address: Intake112Address{City: "москва"}}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("normalized-equal expected_card should validate: %v", err)
	}
}

func TestValidateAlternativesUnknownPath(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.Alternatives = map[string][]string{"expected_card.not_a_field": {"x"}}
	assertInvalidField(t, Validate(body, catalog), "intake112.reference.alternatives")
}

func TestValidateAlternativesKnownPath(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.Alternatives = map[string][]string{"expected_card.address.street": {"Тверская улица"}}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("known alternatives path should validate: %v", err)
	}
}

func TestValidateExpectedProfilesRequiresCatalog(t *testing.T) {
	catalog := pilotCatalog() // no intakeCatalog set
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.ExpectedProfiles = map[string]map[string]Intake112ExpectedProfileValue{
		"104": {"smell": {Value: "yes"}},
	}
	assertInvalidField(t, Validate(body, catalog), "intake112.reference.expected_profiles")
}

func TestValidateExpectedProfilesUnknownProfileFieldOption(t *testing.T) {
	ic := pilotIntakeCatalog()
	base := pilotCatalog()
	base.services["pilot_gas_104"] = ServiceRecord{Active: true}
	base.intakeCatalog = &ic
	for name, profiles := range map[string]map[string]map[string]Intake112ExpectedProfileValue{
		"unknown profile":           {"999": {"smell": {Value: "yes"}}},
		"unknown field":             {"104": {"nope": {Value: "yes"}}},
		"unknown option":            {"104": {"smell": {Value: "maybe"}}},
		"scalar for multiple field": {"104": {"signs": {Value: "hissing"}}},
		"array for single field":    {"104": {"smell": {Values: []string{"yes"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			body := validFullCaseBody()
			body.Intake112.Reference.ExpectedProfiles = profiles
			if err := Validate(body, base); err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
		})
	}
}

func TestValidateExpectedProfilesValid(t *testing.T) {
	ic := pilotIntakeCatalog()
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	catalog.intakeCatalog = &ic
	body := validFullCaseBody()
	body.Intake112.Reference.ExpectedProfiles = map[string]map[string]Intake112ExpectedProfileValue{
		"104": {"smell": {Value: "yes"}, "signs": {Values: []string{"hissing", "smell"}}},
	}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("valid expected_profiles should validate: %v", err)
	}
}

func TestValidateOperator112ScoringUnknownCriterion(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.Scoring = &Scoring{Disabled: []string{"NO_SUCH_112_CRITERION"}}
	assertInvalidField(t, Validate(body, catalog), "reference.scoring.disabled")
}

func TestValidateOperator112ScoringKnownCriterion(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.Scoring = &Scoring{Disabled: []string{"DESCRIPTION_PRESENT"}}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("known operator112 rubric-v2 criterion should validate: %v", err)
	}
}

// TestValidateOperator112ScoringKnownV3Criterion is ADR-028's own
// counterpart: DESCRIPTION_CONTENT only exists in rubric-v3, not v2, yet
// a scenario authored before a lesson decides which version it will run
// under must still be able to reference it — operator112RubricCriterionIDs
// is the union of every version, not just rubric.operator112.json's own.
func TestValidateOperator112ScoringKnownV3Criterion(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.Scoring = &Scoring{Disabled: []string{"DESCRIPTION_CONTENT"}}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("known operator112 rubric-v3 criterion should validate: %v", err)
	}
}

func TestValidateDescriptionQuestions(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	for name, tc := range map[string]struct {
		mutate func(*Body)
		field  string
	}{
		"empty id": {func(b *Body) {
			b.Intake112.Reference.DescriptionQuestions = []Intake112DescriptionQuestion{{ID: "", Question: "Указано ли …?"}}
		}, "intake112.reference.description_questions[0]"},
		"empty question": {func(b *Body) {
			b.Intake112.Reference.DescriptionQuestions = []Intake112DescriptionQuestion{{ID: "smell", Question: ""}}
		}, "intake112.reference.description_questions[0]"},
		"duplicate id": {func(b *Body) {
			b.Intake112.Reference.DescriptionQuestions = []Intake112DescriptionQuestion{
				{ID: "smell", Question: "Указано ли, что пахнет газом?"},
				{ID: "smell", Question: "Указано ли, что запах сильный?"},
			}
		}, "intake112.reference.description_questions[1]"},
	} {
		t.Run(name, func(t *testing.T) {
			body := validFullCaseBody()
			tc.mutate(&body)
			assertInvalidField(t, Validate(body, catalog), tc.field)
		})
	}
	valid := validFullCaseBody()
	valid.Intake112.Reference.DescriptionQuestions = []Intake112DescriptionQuestion{
		{ID: "smell", Question: "Указано ли, что ощущается запах газа?"},
		{ID: "victims", Question: "Указано ли число пострадавших?"},
	}
	if err := Validate(valid, catalog); err != nil {
		t.Fatalf("valid description_questions should validate: %v", err)
	}
}

func TestValidateExpectedServicesAllowedForCardOnly(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := Body{ExerciseType: ExerciseTypeOperator112Intake, Intake112: &Intake112{
		Mode: "card_only",
		Reference: Intake112Reference{
			ExpectedTypes: []string{"gas_explosion"}, CaseDescription: "Запах газа",
			ExpectedServices: []string{"pilot_gas_104"},
		},
	}}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("card_only with expected_services should validate since ADR-026: %v", err)
	}
}
