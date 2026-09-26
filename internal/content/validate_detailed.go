package content

import (
	"errors"
	"fmt"
	"sort"
)

// Severity is ValidationIssue's own error/warning distinction (112-7/
// ADR-027, openapi.yaml's ValidationIssue): error blocks the editor's
// preview and approve; warning is informational only.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// ValidationIssue is ValidateDetailed's own element — openapi.yaml's
// ValidationIssue, field for field. Path is a JSON-Pointer-ish location
// inside Body (not necessarily a real json.Pointer — the same loose
// dotted/bracketed spelling *ValidationError.Field already uses).
type ValidationIssue struct {
	Path     string
	Code     string
	Severity Severity
	Message  string
}

// ValidateDetailed is Validate's issue-collecting counterpart for the
// operator-112 scenario editor (112-7/ADR-027): an editing session needs
// every problem in one round trip, not just the first the way file
// import's Validate stops at. It first runs the existing Validate
// unchanged — that pass keeps its historical first-error behavior, since
// a structurally incoherent body (wrong mode combination, missing
// required dialogue shape, an already-covered reference/scoring/catalog
// reference) has no safe way to run the richer per-field checks below
// without duplicating Validate's own shape assumptions wholesale. Once
// Validate finds the body structurally sound, ValidateDetailed runs the
// collector checks that can report several independent problems at once
// (an expected-card field no fact can ever reveal, a profile card outside
// the case's own incident types, an ambiguous ask pattern, a declared
// service list diverging from what service_rules would suggest) — this
// is the set 112-7's editor actually iterates against, once the coarser
// per-field errors Validate already reports are fixed one at a time (the
// same path a scenario reaches structural validity through today, via
// file import's own error field).
//
// A non-nil error return is an infrastructure failure (e.g. the rubric or
// intake catalog embed cannot be read) — never a per-field problem, which
// always comes back as an issue instead.
func ValidateDetailed(body Body, catalog Catalog) ([]ValidationIssue, error) {
	if err := Validate(body, catalog); err != nil {
		var verr *ValidationError
		if errors.As(err, &verr) {
			return []ValidationIssue{{Path: verr.Field, Code: verr.Reason, Severity: SeverityError}}, nil
		}
		return nil, err
	}
	if body.ExerciseType != ExerciseTypeOperator112Intake || body.Intake112 == nil {
		return nil, nil
	}
	return collectIntake112AuthoringIssues(*body.Intake112, catalog), nil
}

func collectIntake112AuthoringIssues(intake Intake112, catalog Catalog) []ValidationIssue {
	var issues []ValidationIssue
	issues = append(issues, unreachableExpectedFieldIssues(intake)...)
	issues = append(issues, profileOutsideExpectedTypesIssues(intake, catalog)...)
	issues = append(issues, servicesDivergeFromRulesIssues(intake, catalog)...)
	issues = append(issues, ambiguousAskPatternIssues(intake)...)
	return issues
}

// expectedCardScorablePaths is every dialogue-fact card_path 112-6/
// ADR-026 actually scores in ADDRESS_FIELDS/the rest of ExpectedCard —
// the same set expectedCardFieldValue resolves, minus object/landmark
// (excluded from scoring by the user's own decision, ADR-026).
var expectedCardScorablePaths = []string{
	"/applicant_name", "/applicant_status", "/age", "/incident_type", "/complaint", "/victims_count",
	"/address/country", "/address/region", "/address/okrug", "/address/district", "/address/city",
	"/address/street", "/address/house", "/address/building", "/address/structure", "/address/flat",
	"/address/entrance", "/address/floor",
}

func addressRegionPath(path string) bool {
	switch path {
	case "/address/country", "/address/region", "/address/okrug":
		return true
	}
	return false
}

// unreachableExpectedFieldIssues is ADR-027's own check: an expected_card
// field with a reference value the trainee has no way to ever produce.
// It only applies to a free-text caller profile — the editor's only
// authoring surface (112-7 §2 decision 4) — since a prepared dialogue's
// reachability is already fully checked by validateIntake112Dialogue's
// own reveals/reachability pass inside Validate. A field is reachable
// when some fact carries its card_path with Knowledge="initial" (open
// from the first turn, aicaller.OpenFacts) or Knowledge="on_question"
// with at least one AskPatterns entry (the operator can trigger it,
// aicaller.FactAsked); a fact with Knowledge="unknown" for that path, or
// no fact at all, or an on_question fact with no AskPatterns, can never
// surface the value — country/region/okrug are downgraded to a warning
// since the operator fills them in from judgement, not from the caller.
func unreachableExpectedFieldIssues(intake Intake112) []ValidationIssue {
	if intake.CallerMode != CallerModeFreeText || intake.Dialogue == nil || intake.Reference.ExpectedCard == nil {
		return nil
	}
	byPath := make(map[string]Intake112Fact, len(intake.Dialogue.Facts))
	for _, fact := range intake.Dialogue.Facts {
		if fact.CardPath != "" {
			byPath[fact.CardPath] = fact
		}
	}
	var issues []ValidationIssue
	for _, path := range expectedCardScorablePaths {
		value, ok := expectedCardFieldValue(intake.Reference.ExpectedCard, path)
		if !ok || value == "" {
			continue
		}
		fact, hasFact := byPath[path]
		reachable := hasFact && (fact.Knowledge == "initial" || (fact.Knowledge == "on_question" && len(fact.AskPatterns) > 0))
		if reachable {
			continue
		}
		severity := SeverityError
		if addressRegionPath(path) {
			severity = SeverityWarning
		}
		issues = append(issues, ValidationIssue{
			Path: "intake112.reference.expected_card" + path, Code: "unreachable_expected_field", Severity: severity,
			Message: fmt.Sprintf("эталон задаёт значение для %s, но заявитель никогда не может его сообщить", path),
		})
	}
	return issues
}

// profileOutsideExpectedTypesIssues catches a reference.expected_profiles
// entry for a profile card no expected_types actually brings in
// (intake-catalog.json's types[].profile_ids) — Validate's own
// expected_profiles check only verifies the profile/field/option ids
// exist in the catalog at all, not that this particular scenario would
// ever show that card to the trainee.
func profileOutsideExpectedTypesIssues(intake Intake112, catalog Catalog) []ValidationIssue {
	if len(intake.Reference.ExpectedProfiles) == 0 {
		return nil
	}
	ic, ok := catalog.IntakeCatalog()
	if !ok {
		return nil // Validate already rejects this combination without a catalog
	}
	profileIDsByType := make(map[string][]string, len(ic.Types))
	for _, t := range ic.Types {
		profileIDsByType[t.ID] = t.ProfileIDs
	}
	reachable := make(map[string]bool)
	for _, typeID := range intake.Reference.ExpectedTypes {
		for _, profileID := range profileIDsByType[typeID] {
			reachable[profileID] = true
		}
	}
	profileIDs := make([]string, 0, len(intake.Reference.ExpectedProfiles))
	for id := range intake.Reference.ExpectedProfiles {
		profileIDs = append(profileIDs, id)
	}
	sort.Strings(profileIDs)
	var issues []ValidationIssue
	for _, profileID := range profileIDs {
		if reachable[profileID] {
			continue
		}
		issues = append(issues, ValidationIssue{
			Path: "intake112.reference.expected_profiles." + profileID, Code: "profile_outside_expected_types", Severity: SeverityError,
			Message: fmt.Sprintf("карта %s не связана ни с одним из expected_types этого кейса", profileID),
		})
	}
	return issues
}

// servicesDivergeFromRulesIssues warns when the declared
// expected_services differs from what the catalog's own service_rules
// would suggest for this case's expected_types/expected_profiles — the
// same rule internal/training/operator112's decideProfileFlow evaluates
// at runtime for the trainee (reimplemented narrowly here over the
// reference rather than a live IntakeCard, to avoid content depending on
// training). A rule with a FieldID this case's expected_profiles never
// answers is skipped rather than guessed at — an unanswered field is not
// evidence either way, matching ADR-026's "эталон отсутствует" stance.
func servicesDivergeFromRulesIssues(intake Intake112, catalog Catalog) []ValidationIssue {
	if len(intake.Reference.ExpectedTypes) == 0 {
		return nil
	}
	ic, ok := catalog.IntakeCatalog()
	if !ok {
		return nil
	}
	profileIDsByType := make(map[string][]string, len(ic.Types))
	for _, t := range ic.Types {
		profileIDsByType[t.ID] = t.ProfileIDs
	}
	active := make(map[string]bool)
	for _, typeID := range intake.Reference.ExpectedTypes {
		for _, profileID := range profileIDsByType[typeID] {
			active[profileID] = true
		}
	}
	suggested := make(map[string]bool)
	for _, rule := range ic.ServiceRules {
		if !active[rule.ProfileID] {
			continue
		}
		if rule.FieldID == "" {
			suggested[rule.ServiceCode] = true
			continue
		}
		answers, hasProfile := intake.Reference.ExpectedProfiles[rule.ProfileID]
		if !hasProfile {
			continue
		}
		answer, hasField := answers[rule.FieldID]
		if !hasField {
			continue
		}
		if answer.Values != nil {
			for _, v := range answer.Values {
				if v == rule.Equals {
					suggested[rule.ServiceCode] = true
					break
				}
			}
			continue
		}
		if answer.Value == rule.Equals {
			suggested[rule.ServiceCode] = true
		}
	}
	declared := make(map[string]bool, len(intake.Reference.ExpectedServices))
	for _, code := range intake.Reference.ExpectedServices {
		declared[code] = true
	}
	if setsEqual(suggested, declared) {
		return nil
	}
	return []ValidationIssue{{
		Path: "intake112.reference.expected_services", Code: "services_diverge_from_rules", Severity: SeverityWarning,
		Message: "expected_services расходится с тем, что предложили бы service_rules для expected_types/expected_profiles этого кейса",
	}}
}

func setsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// ambiguousAskPatternIssues warns when two different facts share the
// same example ask phrase (their AskPatterns[0]) — a scenario author's
// sign that the classifier cannot tell the two facts apart from that
// phrase alone, even though both patterns individually compile fine.
func ambiguousAskPatternIssues(intake Intake112) []ValidationIssue {
	if intake.CallerMode != CallerModeFreeText || intake.Dialogue == nil {
		return nil
	}
	byExample := make(map[string][]string)
	for _, fact := range intake.Dialogue.Facts {
		if len(fact.AskPatterns) == 0 {
			continue
		}
		example := fact.AskPatterns[0]
		byExample[example] = append(byExample[example], fact.ID)
	}
	examples := make([]string, 0, len(byExample))
	for example := range byExample {
		examples = append(examples, example)
	}
	sort.Strings(examples)
	var issues []ValidationIssue
	for _, example := range examples {
		ids := byExample[example]
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		issues = append(issues, ValidationIssue{
			Path: "intake112.dialogue.facts", Code: "ambiguous_ask_pattern", Severity: SeverityWarning,
			Message: fmt.Sprintf("факты %v делят один и тот же образец ask_patterns[0] (%q)", ids, example),
		})
	}
	return issues
}
