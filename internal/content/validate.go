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
