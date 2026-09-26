package content

import "testing"

func TestOperator112EditorEligible(t *testing.T) {
	free := Body{ExerciseType: ExerciseTypeOperator112Intake, Intake112: &Intake112{Mode: "full_case", CallerMode: CallerModeFreeText}}
	if !operator112EditorEligible(free) {
		t.Fatal("full_case+free_text should be eligible")
	}
	prepared := Body{ExerciseType: ExerciseTypeOperator112Intake, Intake112: &Intake112{Mode: "full_case"}}
	if operator112EditorEligible(prepared) {
		t.Fatal("prepared (empty caller_mode) full_case should not be eligible")
	}
	cardOnly := Body{ExerciseType: ExerciseTypeOperator112Intake, Intake112: &Intake112{Mode: "card_only"}}
	if operator112EditorEligible(cardOnly) {
		t.Fatal("card_only should not be eligible")
	}
	dds := Body{ExerciseType: ExerciseTypeDDSProcessing}
	if operator112EditorEligible(dds) {
		t.Fatal("dds_processing should not be eligible")
	}
}

func TestHasBlockingIssue(t *testing.T) {
	if hasBlockingIssue(nil) {
		t.Fatal("no issues should not block")
	}
	if hasBlockingIssue([]ValidationIssue{{Severity: SeverityWarning}}) {
		t.Fatal("only warnings should not block")
	}
	if !hasBlockingIssue([]ValidationIssue{{Severity: SeverityWarning}, {Severity: SeverityError}}) {
		t.Fatal("an error among warnings should block")
	}
}

func TestCanonicalizeOperator112BodyNormalizesNilArrays(t *testing.T) {
	svc := NewService(nil, nil)
	body := validAICallerFullCaseBody().Intake112
	raw, canonicalJSON, digest, err := svc.canonicalizeOperator112Body(Body{
		Schema: "emsim/scenario/v1", ExerciseType: ExerciseTypeOperator112Intake, Difficulty: 3, Intake112: body,
	})
	if err != nil {
		t.Fatalf("canonicalizeOperator112Body: %v", err)
	}
	if len(canonicalJSON) == 0 || digest == ([32]byte{}) {
		t.Fatal("expected non-empty canonical JSON and digest")
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("raw is %T, want map[string]any", raw)
	}
	// Hints has no omitempty (body.go) — json.Marshal(nil Hints) would
	// otherwise freeze it as an explicit null, which scenario.schema.json
	// (type: array, not nullable) rejects; canonicalizeOperator112Body
	// must normalize it to an empty array instead.
	hints, ok := obj["hints"].([]any)
	if !ok || len(hints) != 0 {
		t.Fatalf("hints = %#v, want an empty array", obj["hints"])
	}
}
