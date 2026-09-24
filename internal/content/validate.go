package content

import "fmt"

// Catalog is the domain-facing reference-data lookup Validate needs for
// services, classifier types, and referenced scenario versions. It exposes
// plain values rather than contexts, transactions, or SQL; the production
// adapter resolves those values through the import transaction while unit
// tests use maps. Validate therefore remains independent of persistence.
type Catalog interface {
	Service(code string) (ServiceRecord, bool)
	ClassifierType(code string) (name string, known bool)
	ScenarioVersion(key string, version int) (ScenarioVersionReference, bool)
}

// Validate checks a decoded Body against every semantic rule
// scenario.schema.json cannot express structurally (slice-2-plan.md's C2:
// service/classifier/scenario-version existence, notification_list shape,
// workflow reachability, scoring references, field_corrections). It assumes
// body already passed schema.Validator.ValidateFile — enum-restricted fields
// (Reaction values, field_corrections[].path, exercise_type) are not
// re-checked here.
//
// It deliberately does NOT check that a field_corrections entry's
// ExpectedValue differs from the card's current value at that path: the
// mismatch between the two is the error the trainee is meant to find
// during the exercise (training/dds, slice 3), not a defect in the
// prepared scenario — see FieldCorrection's doc comment.
func Validate(body Body, catalog Catalog) error {
	if !body.ExerciseType.Valid() {
		return invalid("exercise_type", "invalid")
	}
	if body.ExerciseType == ExerciseTypeOperator112Intake {
		return validateIntake112(body.Intake112, catalog)
	}

	target, ok := catalog.Service(body.TargetService)
	if !ok {
		return invalid("target_service", "unknown")
	}
	if !target.Active {
		return invalid("target_service", "inactive")
	}

	if err := validateNotificationList(body.Card.NotificationList, body.TargetService, catalog); err != nil {
		return err
	}
	if err := validateIncidentType(body.Card.Incident, catalog); err != nil {
		return err
	}

	contactKeys, err := validateContactKeys(body.Contacts)
	if err != nil {
		return err
	}
	if err := validateEvents(body.Events, contactKeys, body.ExerciseType, body.TargetService, catalog); err != nil {
		return err
	}
	if err := validateCall(body.Reference.Call, contactKeys); err != nil {
		return err
	}
	if err := validateWorkflowConsistency(body.Reference, target.Workflow); err != nil {
		return err
	}
	if err := validateFieldCorrections(body.Reference.FieldCorrections, target.Workflow); err != nil {
		return err
	}
	return validateScoring(body.Reference.Scoring)
}

func validateIntake112(intake *Intake112, catalog Catalog) error {
	if intake == nil {
		return invalid("intake112", "required")
	}
	if intake.Mode == "card_only" {
		if intake.CallerMode != "" || intake.Dialogue != nil || intake.Call != nil || len(intake.RecipientServices) != 0 ||
			len(intake.Reference.ExpectedTypes) == 0 || intake.Reference.CaseDescription == "" ||
			len(intake.Reference.ExpectedServices) != 0 ||
			intake.Reference.RecipientService != "" || intake.Reference.ExpectedCard != nil {
			return invalid("intake112", "invalid_card_only_case")
		}
		return nil
	}
	if intake.Mode == "full_case" {
		return validateIntake112FullCase(intake, catalog)
	}
	if intake.Mode != "" && intake.Mode != "incoming_call" {
		return invalid("intake112.mode", "invalid")
	}
	// CallerMode (112-5a/ADR-024) only ever applies to full_case's own
	// caller-chat window — incoming_call keeps 112-1's single scripted
	// call, never a free-text one.
	if intake.CallerMode != "" {
		return invalid("intake112.caller_mode", "full_case_only")
	}
	if intake.Call == nil || intake.Reference.ExpectedCard == nil {
		return invalid("intake112", "incomplete_incoming_call")
	}
	if len(intake.RecipientServices) != 1 {
		return invalid("intake112.recipient_services", "exactly_one_required")
	}
	code := intake.RecipientServices[0]
	service, ok := catalog.Service(code)
	if !ok {
		return invalid("intake112.recipient_services[0]", "unknown")
	}
	if !service.Active {
		return invalid("intake112.recipient_services[0]", "inactive")
	}
	if intake.Reference.RecipientService != code {
		return invalid("intake112.reference.recipient_service", "not_available")
	}
	if (len(intake.Call.Script) > 0) == (intake.Dialogue != nil) {
		return invalid("intake112.dialogue", "script_or_dialogue_required")
	}
	if intake.Dialogue != nil {
		return validateIntake112Dialogue(*intake.Dialogue)
	}
	return nil
}

// validateIntake112FullCase is ADR-023/slice-112-4-plan.md's combined
// mode: 112-2's caller dialogue plus 112-3's incident types, finished
// through notify_services rather than a single recipient service. It
// forbids incoming_call's own fields (recipient_services, script,
// expected_card, reference.recipient_service) — a scenario picks one
// mode, not a mix of both contracts.
func validateIntake112FullCase(intake *Intake112, catalog Catalog) error {
	if intake.CallerMode != "" && intake.CallerMode != CallerModePrepared && intake.CallerMode != CallerModeFreeText {
		return invalid("intake112.caller_mode", "invalid")
	}
	if intake.Call == nil || intake.Dialogue == nil || len(intake.Call.Script) > 0 ||
		len(intake.RecipientServices) != 0 ||
		intake.Reference.ExpectedCard != nil || intake.Reference.RecipientService != "" ||
		len(intake.Reference.ExpectedTypes) == 0 || intake.Reference.CaseDescription == "" ||
		len(intake.Reference.ExpectedServices) == 0 {
		return invalid("intake112", "invalid_full_case")
	}
	seen := make(map[string]bool, len(intake.Reference.ExpectedServices))
	for i, code := range intake.Reference.ExpectedServices {
		field := fmt.Sprintf("intake112.reference.expected_services[%d]", i)
		if code == "" || seen[code] {
			return invalid(field, "invalid_or_duplicate")
		}
		seen[code] = true
		service, ok := catalog.Service(code)
		if !ok {
			return invalid(field, "unknown")
		}
		if !service.Active {
			return invalid(field, "inactive")
		}
	}
	if intake.CallerMode == CallerModeFreeText {
		return validateIntake112FreeTextDialogue(*intake.Dialogue)
	}
	return validateIntake112Dialogue(*intake.Dialogue)
}

// validateIntake112FreeTextDialogue is CallerModeFreeText's own dialogue
// shape (112-5a/ADR-024): the applicant does not speak a scripted
// initial line or answer scripted questions — the trainee writes freely
// in the caller-chat window and a CallerReplier answers asynchronously.
// Only Facts are validated (the future model's own knowledge input,
// unused by 112-5a's stub); Initial/Questions carry no meaning for this
// caller_mode and must stay empty so a scenario cannot mix both dialogue
// shapes. Unlike validateIntake112Dialogue, there is no reveals/reachability
// check — free text has no scripted reveal mechanism.
func validateIntake112FreeTextDialogue(dialogue Intake112Dialogue) error {
	if dialogue.Initial.ID != "" || dialogue.Initial.Text != "" || len(dialogue.Initial.Reveals) != 0 || len(dialogue.Questions) != 0 {
		return invalid("intake112.dialogue", "free_text_forbids_initial_or_questions")
	}
	if len(dialogue.Facts) == 0 {
		return invalid("intake112.dialogue.facts", "required")
	}
	ids := make(map[string]bool, len(dialogue.Facts))
	paths := make(map[string]bool, len(dialogue.Facts))
	for i, fact := range dialogue.Facts {
		field := fmt.Sprintf("intake112.dialogue.facts[%d]", i)
		if fact.ID == "" || ids[fact.ID] {
			return invalid(field+".id", "missing_or_duplicate")
		}
		if !validIntake112CardPath(fact.CardPath) || paths[fact.CardPath] {
			return invalid(field+".card_path", "invalid_or_duplicate")
		}
		if fact.Knowledge == "unknown" {
			if fact.Value != "" {
				return invalid(field+".value", "unknown_has_value")
			}
		} else if (fact.Knowledge != "initial" && fact.Knowledge != "on_question") || fact.Value == "" {
			return invalid(field+".knowledge", "invalid")
		}
		ids[fact.ID], paths[fact.CardPath] = true, true
	}
	return nil
}

func validateIntake112Dialogue(dialogue Intake112Dialogue) error {
	if dialogue.Initial.ID == "" || dialogue.Initial.Text == "" || len(dialogue.Questions) == 0 {
		return invalid("intake112.dialogue", "incomplete")
	}
	facts := make(map[string]Intake112Fact, len(dialogue.Facts))
	paths := make(map[string]bool, len(dialogue.Facts))
	for i, fact := range dialogue.Facts {
		field := fmt.Sprintf("intake112.dialogue.facts[%d]", i)
		if fact.ID == "" || facts[fact.ID].ID != "" {
			return invalid(field+".id", "missing_or_duplicate")
		}
		if !validIntake112CardPath(fact.CardPath) || paths[fact.CardPath] {
			return invalid(field+".card_path", "invalid_or_duplicate")
		}
		if fact.Knowledge == "unknown" {
			if fact.Value != "" {
				return invalid(field+".value", "unknown_has_value")
			}
		} else if (fact.Knowledge != "initial" && fact.Knowledge != "on_question") || fact.Value == "" {
			return invalid(field+".knowledge", "invalid")
		}
		facts[fact.ID], paths[fact.CardPath] = fact, true
	}
	questions := make(map[string]Intake112Question, len(dialogue.Questions))
	utterances := map[string]bool{dialogue.Initial.ID: true}
	for i, question := range dialogue.Questions {
		field := fmt.Sprintf("intake112.dialogue.questions[%d]", i)
		if question.ID == "" || question.Text == "" || question.TopicID == "" || questions[question.ID].ID != "" {
			return invalid(field, "incomplete_or_duplicate")
		}
		if question.Answer.ID == "" || question.Answer.Text == "" || utterances[question.Answer.ID] {
			return invalid(field+".answer", "incomplete_or_duplicate")
		}
		questions[question.ID] = question
		utterances[question.Answer.ID] = true
	}
	revealed := make(map[string]bool, len(facts))
	validateReveals := func(path string, ids []string, initial bool) error {
		for _, id := range ids {
			fact, exists := facts[id]
			if !exists || (initial && fact.Knowledge != "initial") || (!initial && fact.Knowledge == "initial") {
				return invalid(path, "invalid_fact_reference")
			}
			revealed[id] = true
		}
		return nil
	}
	if err := validateReveals("intake112.dialogue.initial.reveals", dialogue.Initial.Reveals, true); err != nil {
		return err
	}
	for i, question := range dialogue.Questions {
		field := fmt.Sprintf("intake112.dialogue.questions[%d]", i)
		if err := validateReveals(field+".answer.reveals", question.Answer.Reveals, false); err != nil {
			return err
		}
		for _, preceding := range question.AvailableAfter {
			if _, exists := questions[preceding]; !exists || preceding == question.ID {
				return invalid(field+".available_after", "invalid_question_reference")
			}
		}
	}
	for id := range facts {
		if !revealed[id] {
			return invalid("intake112.dialogue.facts", "unreachable_"+id)
		}
	}
	visiting, visited := make(map[string]bool), make(map[string]bool)
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return false
		}
		if visited[id] {
			return true
		}
		visiting[id] = true
		for _, preceding := range questions[id].AvailableAfter {
			if !visit(preceding) {
				return false
			}
		}
		visiting[id], visited[id] = false, true
		return true
	}
	for id := range questions {
		if !visit(id) {
			return invalid("intake112.dialogue.questions", "prerequisite_cycle")
		}
	}
	return nil
}

func validIntake112CardPath(path string) bool {
	switch path {
	case "/applicant_name", "/applicant_status", "/age", "/incident_type", "/complaint",
		"/victims_present", "/victims_count", "/provided_phone", "/on_site_phone":
		return true
	}
	const prefix = "/address/"
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return false
	}
	switch path[len(prefix):] {
	case "country", "region", "city", "object", "okrug", "district", "street", "house", "building",
		"structure", "flat", "entrance", "floor", "code", "landmark", "descriptive":
		return true
	}
	return false
}

func validateNotificationList(list []NotificationEntry, targetService string, catalog Catalog) error {
	mineCount := 0
	for i, entry := range list {
		if _, known := catalog.Service(entry.Service); !known {
			return invalid(fmt.Sprintf("card.notification_list[%d].service", i), "unknown")
		}
		if entry.Mine {
			mineCount++
			if entry.Service != targetService {
				return invalid(fmt.Sprintf("card.notification_list[%d].mine", i), "not_target_service")
			}
		}
	}
	if mineCount != 1 {
		return invalid("card.notification_list", "exactly_one_mine_required")
	}
	return nil
}

func validateIncidentType(incident Incident, catalog Catalog) error {
	name, known := catalog.ClassifierType(incident.TypeCode)
	if !known {
		return invalid("card.incident.type_code", "unknown")
	}
	if name != incident.TypeName {
		return invalid("card.incident.type_name", "mismatch")
	}
	return nil
}

func validateContactKeys(contacts []Contact) (map[string]bool, error) {
	keys := make(map[string]bool, len(contacts))
	for i, c := range contacts {
		if keys[c.Key] {
			return nil, invalid(fmt.Sprintf("contacts[%d].key", i), "duplicate")
		}
		keys[c.Key] = true
	}
	return keys, nil
}

func validateEvents(events []Event, contactKeys map[string]bool, exerciseType ExerciseType, targetService string, catalog Catalog) error {
	seenKeys := make(map[string]bool, len(events))
	for i, e := range events {
		if seenKeys[e.Key] {
			return invalid(fmt.Sprintf("events[%d].key", i), "duplicate")
		}
		seenKeys[e.Key] = true

		if e.Delivery == "phone_incoming" || e.Delivery == "notice" {
			if e.From == "" || !contactKeys[e.From] {
				return invalid(fmt.Sprintf("events[%d].from", i), "unknown_contact")
			}
		}
		if e.Delivery == "spawn_card" {
			if e.Spawn == nil {
				return invalid(fmt.Sprintf("events[%d].spawn", i), "required")
			}
			field := fmt.Sprintf("events[%d].spawn", i)
			if e.Spawn.Kind != "scenario" {
				return invalid(field+".kind", "unsupported")
			}
			if e.Spawn.ScenarioKey == "" {
				return invalid(field+".scenario_key", "required")
			}
			if e.Spawn.Version < 1 {
				return invalid(field+".version", "required")
			}
			ref, known := catalog.ScenarioVersion(e.Spawn.ScenarioKey, e.Spawn.Version)
			if !known {
				return invalid(field, "unknown")
			}
			if !ref.Published || (ref.Status != "approved" && ref.Status != "superseded") {
				return invalid(field, "not_approved")
			}
			if ref.ExerciseType != exerciseType {
				return invalid(field, "exercise_type_mismatch")
			}
			if ref.TargetService != targetService {
				return invalid(field, "target_service_mismatch")
			}
		}
	}
	return nil
}

func validateCall(call Call, contactKeys map[string]bool) error {
	if !call.Required {
		return nil
	}
	if call.To == "" || !contactKeys[call.To] {
		return invalid("reference.call.to", "unknown_contact")
	}
	return nil
}

// validateWorkflowConsistency checks that reference.primary_decision.status
// is reachable from "received" in the target service's workflow, and that
// expected_chain is a valid path of Transitions edges starting from it.
func validateWorkflowConsistency(ref Reference, workflow Workflow) error {
	reachable := workflow.reachableFrom(ReactionReceived)
	if !reachable[ref.PrimaryDecision.Status] {
		return invalid("reference.primary_decision.status", "unreachable_in_workflow")
	}

	current := ref.PrimaryDecision.Status
	for i, step := range ref.ExpectedChain {
		allowed := false
		for _, next := range workflow.Transitions[current] {
			if next == step {
				allowed = true
				break
			}
		}
		if !allowed {
			return invalid(fmt.Sprintf("reference.expected_chain[%d]", i), "not_a_workflow_transition")
		}
		current = step
	}
	return nil
}

func validateFieldCorrections(corrections []FieldCorrection, workflow Workflow) error {
	if len(corrections) == 0 {
		return nil
	}
	known := workflow.knownStatuses()
	for i, fc := range corrections {
		if !known[fc.BeforeStatus] {
			return invalid(fmt.Sprintf("reference.field_corrections[%d].before_status", i), "not_in_workflow")
		}
	}
	return nil
}

func validateScoring(scoring *Scoring) error {
	if scoring == nil {
		return nil
	}
	ids, err := rubricCriterionIDs()
	if err != nil {
		return fmt.Errorf("load rubric criteria: %w", err)
	}
	for id := range scoring.Weights {
		if !ids[id] {
			return invalid("reference.scoring.weights", fmt.Sprintf("unknown_criterion:%s", id))
		}
	}
	for _, id := range scoring.Critical {
		if !ids[id] {
			return invalid("reference.scoring.critical", fmt.Sprintf("unknown_criterion:%s", id))
		}
	}
	for _, id := range scoring.Disabled {
		if !ids[id] {
			return invalid("reference.scoring.disabled", fmt.Sprintf("unknown_criterion:%s", id))
		}
	}
	return nil
}
