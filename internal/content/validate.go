package content

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"emsim/internal/content/normalize"
)

// Catalog is the domain-facing reference-data lookup Validate needs for
// services, classifier types, referenced scenario versions, and (112-6/
// ADR-026) the intake profile-card catalog. It exposes plain values
// rather than contexts, transactions, or SQL; the production adapter
// resolves those values through the import transaction while unit tests
// use maps. Validate therefore remains independent of persistence.
type Catalog interface {
	Service(code string) (ServiceRecord, bool)
	ClassifierType(code string) (name string, known bool)
	ScenarioVersion(key string, version int) (ScenarioVersionReference, bool)
	// IntakeCatalog returns the latest imported operator-112 profile
	// catalog (seed/intake-catalog.json), or false if none has been
	// imported yet. A scenario referencing expected_profiles before any
	// catalog import fails validation the same way an unknown service
	// code does (ErrValidation), rather than silently accepting an
	// unverifiable reference.
	IntakeCatalog() (IntakeCatalog, bool)
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
			intake.Reference.RecipientService != "" {
			return invalid("intake112", "invalid_card_only_case")
		}
		return validateIntake112Reference(intake, catalog)
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
		intake.Reference.RecipientService != "" ||
		len(intake.Reference.ExpectedTypes) == 0 || intake.Reference.CaseDescription == "" ||
		len(intake.Reference.ExpectedServices) == 0 {
		return invalid("intake112", "invalid_full_case")
	}
	if err := validateIntake112Reference(intake, catalog); err != nil {
		return err
	}
	if intake.CallerMode == CallerModeFreeText {
		return validateIntake112FreeTextDialogue(*intake.Dialogue)
	}
	return validateIntake112Dialogue(*intake.Dialogue)
}

// validateIntake112Reference is 112-6/ADR-026's own semantic validation
// over intake112.reference, shared by every mode: it does not care
// whether ExpectedCard/ExpectedServices/ExpectedProfiles/Alternatives/
// Scoring are present at all (every one of them is optional — ADR-026's
// "эталон отсутствует" rule is exactly for the case where they are not),
// only that whatever IS present is internally consistent and resolvable
// against the catalog/rubric/dialogue. incoming_call's own required
// expected_card is checked by its own caller before this runs.
func validateIntake112Reference(intake *Intake112, catalog Catalog) error {
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
	for path := range intake.Reference.Alternatives {
		if !validExpectedCardFieldPath(path) {
			return invalid("intake112.reference.alternatives", fmt.Sprintf("unknown_path:%s", path))
		}
	}
	if len(intake.Reference.ExpectedProfiles) > 0 {
		ic, ok := catalog.IntakeCatalog()
		if !ok {
			return invalid("intake112.reference.expected_profiles", "no_intake_catalog_imported")
		}
		profiles := make(map[string]IntakeProfile, len(ic.Profiles))
		for _, p := range ic.Profiles {
			profiles[p.ID] = p
		}
		for profileID, answers := range intake.Reference.ExpectedProfiles {
			profile, ok := profiles[profileID]
			if !ok {
				return invalid("intake112.reference.expected_profiles", fmt.Sprintf("unknown_profile:%s", profileID))
			}
			fields := make(map[string]IntakeProfileField, len(profile.Fields))
			for _, f := range profile.Fields {
				fields[f.ID] = f
			}
			for fieldID, expected := range answers {
				field, ok := fields[fieldID]
				if !ok {
					return invalid("intake112.reference.expected_profiles", fmt.Sprintf("unknown_field:%s.%s", profileID, fieldID))
				}
				values := expected.Values
				if expected.Values == nil {
					values = []string{expected.Value}
				}
				if field.Kind == "multiple" && expected.Values == nil {
					return invalid("intake112.reference.expected_profiles", fmt.Sprintf("expected_array:%s.%s", profileID, fieldID))
				}
				if field.Kind != "multiple" && expected.Values != nil {
					return invalid("intake112.reference.expected_profiles", fmt.Sprintf("expected_scalar:%s.%s", profileID, fieldID))
				}
				if field.Kind == "single" || field.Kind == "multiple" {
					options := make(map[string]bool, len(field.Options))
					for _, o := range field.Options {
						options[o] = true
					}
					for _, v := range values {
						if !options[v] {
							return invalid("intake112.reference.expected_profiles", fmt.Sprintf("unknown_option:%s.%s=%s", profileID, fieldID, v))
						}
					}
				}
			}
		}
	}
	if intake.Reference.Scoring != nil {
		if err := validateScoringAgainst(intake.Reference.Scoring, operator112RubricCriterionIDs); err != nil {
			return err
		}
	}
	if err := validateDescriptionQuestions(intake.Reference.DescriptionQuestions); err != nil {
		return err
	}
	return validateIntake112ExpectedCardAgainstFacts(intake)
}

// validateDescriptionQuestions is ADR-028's own authoring check: every
// question needs a non-empty id, unique among its siblings (the LLM
// judge's answer map is keyed by these ids — a duplicate would silently
// collapse two questions into one scored slot), and non-empty text.
// scenario.schema.json already bounds count/length/id shape; this only
// adds the cross-item uniqueness JSON Schema itself cannot express.
func validateDescriptionQuestions(questions []Intake112DescriptionQuestion) error {
	seen := make(map[string]bool, len(questions))
	for i, q := range questions {
		field := fmt.Sprintf("intake112.reference.description_questions[%d]", i)
		if strings.TrimSpace(q.ID) == "" || strings.TrimSpace(q.Question) == "" {
			return invalid(field, "empty")
		}
		if seen[q.ID] {
			return invalid(field, "duplicate_id")
		}
		seen[q.ID] = true
	}
	return nil
}

// validateIntake112ExpectedCardAgainstFacts is 112-6/ADR-026/slice-
// 112-6-plan.md's c2 cross-check: a scenario author who fills in both a
// dialogue fact and the closed reference for the same card field must
// not contradict themselves — the trainee has no way to reconcile a
// caller who says one address and a reference that scores another. Only
// facts with a non-empty Value are compared (an "unknown" fact has
// nothing to compare); a card_path outside ExpectedCard's own fields
// (e.g. a free_text narrative fact with no card_path at all) is skipped.
func validateIntake112ExpectedCardAgainstFacts(intake *Intake112) error {
	if intake.Reference.ExpectedCard == nil || intake.Dialogue == nil {
		return nil
	}
	for _, fact := range intake.Dialogue.Facts {
		if fact.CardPath == "" || fact.Value == "" {
			continue
		}
		expected, ok := expectedCardFieldValue(intake.Reference.ExpectedCard, fact.CardPath)
		if !ok || expected == "" {
			continue
		}
		if !normalize.TokenSetEqual(fact.Value, expected) {
			return invalid("intake112.reference.expected_card", fmt.Sprintf("contradicts_fact:%s", fact.CardPath))
		}
	}
	return nil
}

// expectedCardFieldValue maps a dialogue fact's card_path (the same
// vocabulary validIntake112CardPath accepts) onto the matching
// Intake112ExpectedCard field, or ("", false) for a path ExpectedCard has
// no field for (object/code/descriptive — 112-6/ADR-026 does not score
// them, so there is nothing to contradict).
func expectedCardFieldValue(card *Intake112ExpectedCard, path string) (string, bool) {
	switch path {
	case "/applicant_name":
		return card.ApplicantName, true
	case "/applicant_status":
		return card.ApplicantStatus, true
	case "/age":
		if card.Age == 0 {
			return "", false
		}
		return strconv.Itoa(card.Age), true
	case "/incident_type":
		return card.IncidentType, true
	case "/complaint":
		return card.Complaint, true
	case "/victims_count":
		if card.VictimsCount == 0 {
			return "", false
		}
		return strconv.Itoa(card.VictimsCount), true
	}
	const prefix = "/address/"
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return "", false
	}
	a := card.Address
	switch path[len(prefix):] {
	case "country":
		return a.Country, true
	case "region":
		return a.Region, true
	case "okrug":
		return a.Okrug, true
	case "district":
		return a.District, true
	case "city":
		return a.City, true
	case "street":
		return a.Street, true
	case "house":
		return a.House, true
	case "building":
		return a.Building, true
	case "structure":
		return a.Structure, true
	case "flat":
		return a.Flat, true
	case "entrance":
		return a.Entrance, true
	case "floor":
		return a.Floor, true
	case "landmark":
		return a.Landmark, true
	}
	return "", false
}

// validExpectedCardFieldPath reports whether path is one
// Intake112Reference.Alternatives may key on — the same vocabulary
// expectedCardFieldValue reads from, prefixed by "expected_card." (the
// scenario-author-facing spelling, distinct from a dialogue fact's own
// card_path which has no such prefix).
func validExpectedCardFieldPath(path string) bool {
	const prefix = "expected_card."
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return false
	}
	suffix := path[len(prefix):]
	switch suffix {
	case "applicant_name", "applicant_status", "age", "incident_type", "complaint", "victims_count":
		return true
	}
	const addrPrefix = "address."
	if len(suffix) <= len(addrPrefix) || suffix[:len(addrPrefix)] != addrPrefix {
		return false
	}
	switch suffix[len(addrPrefix):] {
	case "country", "region", "okrug", "district", "city", "street", "house", "building",
		"structure", "flat", "entrance", "floor", "landmark":
		return true
	}
	return false
}

// validateIntake112FreeTextDialogue is CallerModeFreeText's own dialogue
// shape (112-5a/ADR-024, 112-5b/ADR-025): the applicant does not speak a
// scripted initial line or answer scripted questions — the trainee writes
// freely in the caller-chat window and a CallerReplier answers
// asynchronously. Initial/Questions carry no meaning for this caller_mode
// and must stay empty so a scenario cannot mix both dialogue shapes.
// Unlike validateIntake112Dialogue, there is no reveals/reachability
// check over Questions — free text has no scripted reveal mechanism.
//
// Caller is optional (112-5b, minimal schema extension "a" — see
// slice-112-5b-plan.md): a scenario written for 112-5a's stub, or one that
// intentionally keeps answering through the stub under CALLER_REPLIER=llm,
// omits it and is still valid. When present, it turns on the AI caller
// adapter's requirements: every initial/on_question fact needs a
// Statement to build a reply around, and Opening.Reveals may only name
// initial facts (the applicant cannot open the call by revealing
// something it does not yet know).
func validateIntake112FreeTextDialogue(dialogue Intake112Dialogue) error {
	if dialogue.Initial.ID != "" || dialogue.Initial.Text != "" || len(dialogue.Initial.Reveals) != 0 || len(dialogue.Questions) != 0 {
		return invalid("intake112.dialogue", "free_text_forbids_initial_or_questions")
	}
	if len(dialogue.Facts) == 0 {
		return invalid("intake112.dialogue.facts", "required")
	}
	facts := make(map[string]Intake112Fact, len(dialogue.Facts))
	paths := make(map[string]bool, len(dialogue.Facts))
	for i, fact := range dialogue.Facts {
		field := fmt.Sprintf("intake112.dialogue.facts[%d]", i)
		if fact.ID == "" || facts[fact.ID].ID != "" {
			return invalid(field+".id", "missing_or_duplicate")
		}
		if fact.CardPath != "" {
			if !validIntake112CardPath(fact.CardPath) || paths[fact.CardPath] {
				return invalid(field+".card_path", "invalid_or_duplicate")
			}
			paths[fact.CardPath] = true
		}
		if fact.Knowledge == "unknown" {
			if fact.Value != "" {
				return invalid(field+".value", "unknown_has_value")
			}
		} else if (fact.Knowledge != "initial" && fact.Knowledge != "on_question") || fact.Value == "" {
			return invalid(field+".knowledge", "invalid")
		}
		if dialogue.Caller != nil && fact.Knowledge != "unknown" && fact.Statement == "" {
			return invalid(field+".statement", "required_with_caller_profile")
		}
		if err := validateIntake112FactPatterns(field, fact); err != nil {
			return err
		}
		facts[fact.ID] = fact
	}
	if dialogue.Caller == nil {
		return nil
	}
	if dialogue.Caller.Persona == "" || dialogue.Caller.Opening.Text == "" {
		return invalid("intake112.dialogue.caller", "incomplete")
	}
	for _, id := range dialogue.Caller.Opening.Reveals {
		fact, exists := facts[id]
		if !exists || fact.Knowledge != "initial" {
			return invalid("intake112.dialogue.caller.opening.reveals", "invalid_fact_reference")
		}
	}
	return nil
}

// validateIntake112FactPatterns rejects a caller-profile pattern that
// would silently misbehave in Go's RE2 engine or that is meaningless
// (empty). \b is disallowed project-wide for these fields: RE2's \b is
// ASCII word-boundary only and matches nothing useful around Cyrillic
// letters, so a scenario author's regex would compile but never fire —
// better to reject it at import than to ship a fact the classifier can
// never detect.
func validateIntake112FactPatterns(field string, fact Intake112Fact) error {
	for _, p := range fact.AskPatterns {
		if err := validateCallerRegexPattern(field+".ask_patterns", p); err != nil {
			return err
		}
	}
	for _, p := range fact.AskExcludePatterns {
		if err := validateCallerRegexPattern(field+".ask_exclude_patterns", p); err != nil {
			return err
		}
	}
	for _, p := range fact.DisclosurePatterns {
		if err := validateCallerRegexPattern(field+".disclosure_patterns", p); err != nil {
			return err
		}
	}
	for i, variant := range fact.AnswerVariants {
		vf := fmt.Sprintf("%s.answer_variants[%d]", field, i)
		if variant.Text == "" {
			return invalid(vf+".text", "required")
		}
		if variant.When != "" {
			if err := validateCallerRegexPattern(vf+".when", variant.When); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCallerRegexPattern(field, pattern string) error {
	if pattern == "" {
		return invalid(field, "empty")
	}
	if strings.Contains(pattern, `\b`) {
		return invalid(field, "word_boundary_forbidden")
	}
	if _, err := regexp.Compile("(?i)" + pattern); err != nil {
		return invalid(field, "invalid_regex")
	}
	return nil
}

// forbidIntake112CallerProfileFields rejects the 112-5b caller-profile
// fields on a prepared dialogue (validateIntake112Dialogue's own facts):
// they have no meaning without the free-text AI adapter that reads them,
// and allowing them on a scripted dialogue would let a scenario carry
// dead data that looks load-bearing.
func forbidIntake112CallerProfileFields(field string, fact Intake112Fact) error {
	if fact.Statement != "" || len(fact.AskPatterns) != 0 || len(fact.AskExcludePatterns) != 0 ||
		len(fact.AnswerVariants) != 0 || len(fact.DisclosurePatterns) != 0 {
		return invalid(field, "caller_profile_fields_forbidden")
	}
	return nil
}

func validateIntake112Dialogue(dialogue Intake112Dialogue) error {
	if dialogue.Initial.ID == "" || dialogue.Initial.Text == "" || len(dialogue.Questions) == 0 {
		return invalid("intake112.dialogue", "incomplete")
	}
	if dialogue.Caller != nil {
		return invalid("intake112.dialogue.caller", "free_text_only")
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
		if err := forbidIntake112CallerProfileFields(field, fact); err != nil {
			return err
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
		if !c.Role.Valid() {
			return nil, invalid(fmt.Sprintf("contacts[%d].role", i), "invalid")
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
		// ADR-031: call_ended is anchored on the first ended outgoing
		// call to since_contact, which must be a contact of this scenario.
		if e.Since == EventSinceCallEnded {
			if e.SinceContact == "" || !contactKeys[e.SinceContact] {
				return invalid(fmt.Sprintf("events[%d].since_contact", i), "unknown_contact")
			}
		} else if e.SinceContact != "" {
			return invalid(fmt.Sprintf("events[%d].since_contact", i), "not_allowed")
		}
		if e.Expects != nil {
			for j, fact := range e.Expects.CommentFacts {
				if strings.TrimSpace(fact) == "" {
					return invalid(fmt.Sprintf("events[%d].expects.comment_facts[%d]", i, j), "empty")
				}
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
	return validateScoringAgainst(scoring, rubricCriterionIDs)
}

// validateScoringAgainst checks scoring's criterion ids against idsFunc
// (rubricCriterionIDs for DDS, operator112RubricCriterionIDs for 112 —
// 112-6/ADR-026) — the same three-field check either rubric's reference.
// scoring must satisfy. scoring is assumed non-nil; callers check that.
func validateScoringAgainst(scoring *Scoring, idsFunc func() (map[string]bool, error)) error {
	ids, err := idsFunc()
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
