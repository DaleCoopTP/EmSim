package content

import "testing"

// TestValidateDetailedStructuralErrorIsSingleIssue is 112-7/ADR-027's own
// contract for a scenario Validate itself already rejects: ValidateDetailed
// must not silently swallow that rejection or somehow produce zero issues
// for an invalid body — it wraps Validate's own *ValidationError as the
// one error-severity issue, unchanged in Path/Code, and never runs the
// deeper collector on a structurally broken body.
func TestValidateDetailedStructuralErrorIsSingleIssue(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validFullCaseBody()
	body.Intake112.Reference.ExpectedTypes = nil // invalid_full_case
	issues, err := ValidateDetailed(body, catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	if len(issues) != 1 || issues[0].Severity != SeverityError || issues[0].Path != "intake112" {
		t.Fatalf("issues = %+v, want exactly one error issue on intake112", issues)
	}
}

// TestValidateDetailedCleanScenarioHasNoIssues guards against the
// collector checks below being noisy on an already-correct scenario.
func TestValidateDetailedCleanScenarioHasNoIssues(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	issues, err := ValidateDetailed(validAICallerFullCaseBody(), catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none for a clean scenario", issues)
	}
}

// twoTypeIntakeCatalog is profileOutsideExpectedTypesIssues/
// servicesDivergeFromRulesIssues' own fixture: two incident types, each
// bringing in its own profile card, plus one field-conditioned service
// rule — richer than pilotIntakeCatalog's single profile/no rules.
func twoTypeIntakeCatalog() IntakeCatalog {
	return IntakeCatalog{
		Version: 1,
		Types: []IntakeIncidentType{
			{ID: "gas_explosion", Name: "Взрыв газа", ProfileIDs: []string{"104"}},
			{ID: "road_traffic_fire", Name: "ДТП с пламенем", ProfileIDs: []string{"101"}},
		},
		Profiles: []IntakeProfile{
			{ID: "104", Version: 1, Name: "Газ", Fields: []IntakeProfileField{
				{ID: "smell", Label: "Запах газа", Kind: "single", Options: []string{"yes", "no"}},
			}},
			{ID: "101", Version: 1, Name: "Пожар", Fields: []IntakeProfileField{
				{ID: "flame", Label: "Открытое пламя", Kind: "single", Options: []string{"yes", "no"}},
			}},
		},
		ServiceRules: []IntakeServiceRule{
			{ID: "gas_104", ProfileID: "104", FieldID: "smell", Equals: "yes", ServiceCode: "pilot_gas_104", Reason: "запах газа подтверждён"},
		},
	}
}

func TestUnreachableExpectedFieldIssues(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validAICallerFullCaseBody()
	// A fact for /address/street exists but with knowledge=on_question and
	// no ask_patterns at all — the operator has no way to ever trigger it,
	// so the classifier can never open it (aicaller.OpenFacts).
	body.Intake112.Dialogue.Facts = append(body.Intake112.Dialogue.Facts, Intake112Fact{
		ID: "street", Label: "Улица", CardPath: "/address/street", Knowledge: "on_question", Value: "Тверская", Statement: "Улица Тверская",
	})
	body.Intake112.Reference.ExpectedCard = &Intake112ExpectedCard{
		Address: Intake112Address{Street: "Тверская", Country: "Россия"}, // country: no fact at all, downgraded to warning
	}
	issues, err := ValidateDetailed(body, catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	byPath := map[string]ValidationIssue{}
	for _, issue := range issues {
		byPath[issue.Path] = issue
	}
	street, ok := byPath["intake112.reference.expected_card/address/street"]
	if !ok || street.Code != "unreachable_expected_field" || street.Severity != SeverityError {
		t.Fatalf("expected an error unreachable_expected_field for street, got %+v", issues)
	}
	country, ok := byPath["intake112.reference.expected_card/address/country"]
	if !ok || country.Code != "unreachable_expected_field" || country.Severity != SeverityWarning {
		t.Fatalf("expected a warning unreachable_expected_field for country, got %+v", issues)
	}
}

func TestUnreachableExpectedFieldReachableViaAskPatterns(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validAICallerFullCaseBody()
	body.Intake112.Dialogue.Facts = append(body.Intake112.Dialogue.Facts, Intake112Fact{
		ID: "street", Label: "Улица", CardPath: "/address/street", Knowledge: "on_question", Value: "Тверская",
		Statement: "Улица Тверская", AskPatterns: []string{"адрес"},
	})
	body.Intake112.Reference.ExpectedCard = &Intake112ExpectedCard{Address: Intake112Address{Street: "Тверская"}}
	issues, err := ValidateDetailed(body, catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	for _, issue := range issues {
		if issue.Code == "unreachable_expected_field" {
			t.Fatalf("street is reachable via ask_patterns, unexpected issue: %+v", issue)
		}
	}
}

func TestProfileOutsideExpectedTypesIssue(t *testing.T) {
	ic := twoTypeIntakeCatalog()
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	catalog.intakeCatalog = &ic
	body := validFullCaseBody()
	body.Intake112.Reference.ExpectedTypes = []string{"gas_explosion"} // brings in profile 104 only
	body.Intake112.Reference.ExpectedProfiles = map[string]map[string]Intake112ExpectedProfileValue{
		"101": {"flame": {Value: "yes"}}, // valid profile/field, but not reachable from gas_explosion
	}
	issues, err := ValidateDetailed(body, catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if issue.Code == "profile_outside_expected_types" && issue.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected profile_outside_expected_types, got %+v", issues)
	}
}

func TestServicesDivergeFromRulesIssue(t *testing.T) {
	ic := twoTypeIntakeCatalog()
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	catalog.intakeCatalog = &ic
	body := validFullCaseBody()
	body.Intake112.Reference.ExpectedTypes = []string{"gas_explosion"}
	body.Intake112.Reference.ExpectedProfiles = map[string]map[string]Intake112ExpectedProfileValue{
		"104": {"smell": {Value: "yes"}}, // triggers the gas_104 rule
	}
	body.Intake112.Reference.ExpectedServices = []string{"pilot_gas_104"} // matches — no divergence yet

	issues, err := ValidateDetailed(body, catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	for _, issue := range issues {
		if issue.Code == "services_diverge_from_rules" {
			t.Fatalf("declared services already match the rule, unexpected divergence: %+v", issue)
		}
	}

	catalog.services["pilot_ambulance"] = ServiceRecord{Active: true}
	body.Intake112.Reference.ExpectedServices = []string{"pilot_ambulance"} // diverges from the rule's own suggestion
	issues, err = ValidateDetailed(body, catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if issue.Code == "services_diverge_from_rules" && issue.Severity == SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected services_diverge_from_rules, got %+v", issues)
	}
}

func TestAmbiguousAskPatternIssue(t *testing.T) {
	catalog := pilotCatalog()
	catalog.services["pilot_gas_104"] = ServiceRecord{Active: true}
	body := validAICallerFullCaseBody()
	// Both facts already in the fixture ask about different phrases; add a
	// third fact that shares its first ask pattern with "age".
	body.Intake112.Dialogue.Facts = append(body.Intake112.Dialogue.Facts, Intake112Fact{
		ID: "victims_count", Label: "Пострадавшие", Knowledge: "on_question", Value: "0",
		Statement: "Пострадавших нет", AskPatterns: []string{"возраст"},
	})
	issues, err := ValidateDetailed(body, catalog)
	if err != nil {
		t.Fatalf("ValidateDetailed error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if issue.Code == "ambiguous_ask_pattern" && issue.Severity == SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ambiguous_ask_pattern, got %+v", issues)
	}
}
