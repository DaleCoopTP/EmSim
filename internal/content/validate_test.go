package content

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeCatalog is an in-memory Catalog for Validate's tests — no pgx.Tx,
// no I/O, matching Catalog's doc comment that Validate itself performs
// none.
type fakeCatalog struct {
	services   map[string]ServiceRecord
	classifier map[string]string // code -> name
	versions   map[uuid.UUID]ScenarioVersionReference
}

func (c fakeCatalog) Service(code string) (ServiceRecord, bool) {
	s, ok := c.services[code]
	return s, ok
}

func (c fakeCatalog) ClassifierType(code string) (string, bool) {
	name, ok := c.classifier[code]
	return name, ok
}

func (c fakeCatalog) ScenarioVersion(id uuid.UUID) (ScenarioVersionReference, bool) {
	ref, ok := c.versions[id]
	return ref, ok
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
		versions: map[uuid.UUID]ScenarioVersionReference{},
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

func TestValidateRejectsSpawnScenarioWithoutVersionID(t *testing.T) {
	body := validPilotBody()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario"}}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "events[0].spawn.scenario_version_id")
}

func TestValidateRejectsSpawnScenarioWithUnknownVersionID(t *testing.T) {
	body := validPilotBody()
	id := uuid.New()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario", ScenarioVersionID: &id}}}
	assertInvalidField(t, Validate(body, pilotCatalog()), "events[0].spawn.scenario_version_id")
}

func TestValidateAcceptsCompatiblePublishedSpawnScenario(t *testing.T) {
	body := validPilotBody()
	id := uuid.New()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario", ScenarioVersionID: &id}}}
	catalog := pilotCatalog()
	catalog.versions[id] = ScenarioVersionReference{
		Status: "superseded", Published: true, ExerciseType: body.ExerciseType, TargetService: body.TargetService,
	}
	if err := Validate(body, catalog); err != nil {
		t.Fatalf("Validate(compatible spawn scenario) = %v, want nil", err)
	}
}

func TestValidateRejectsIncompatibleSpawnScenario(t *testing.T) {
	body := validPilotBody()
	id := uuid.New()
	body.Events = []Event{{Key: "e1", Since: "accepted", Delivery: "spawn_card", Spawn: &EventSpawn{Kind: "scenario", ScenarioVersionID: &id}}}

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
			catalog.versions[id] = ref
			assertInvalidField(t, Validate(body, catalog), "events[0].spawn.scenario_version_id")
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
