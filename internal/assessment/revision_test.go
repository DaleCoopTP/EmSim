package assessment

import (
	"errors"
	"testing"
)

func twoCriterionRubric() Rubric {
	return Rubric{
		PassThreshold: 70, CriticalCap: 40,
		Criteria: []RubricCriterion{
			{ID: "A", Weight: 60, Kind: "deterministic", Rule: "a"},
			{ID: "B", Weight: 40, Kind: "deterministic", Rule: "b"},
		},
	}
}

func TestValidateRevisionRequiresReasonLength(t *testing.T) {
	_, err := ValidateRevision(RevisionInput{Reason: "ok", BaseRevision: 0, Criteria: []CriterionResult{
		{ID: "A", Status: CriterionMet}, {ID: "B", Status: CriterionMet},
	}}, twoCriterionRubric(), nil)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation for a 2-character reason", err)
	}
}

func TestValidateRevisionBaseZeroRequiresFullSet(t *testing.T) {
	_, err := ValidateRevision(RevisionInput{
		Reason: "reviewed manually", BaseRevision: 0,
		Criteria: []CriterionResult{{ID: "A", Status: CriterionMet}},
	}, twoCriterionRubric(), nil)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation for a missing required criterion B", err)
	}
}

func TestValidateRevisionBaseZeroFullSetSucceeds(t *testing.T) {
	merged, err := ValidateRevision(RevisionInput{
		Reason: "reviewed manually", BaseRevision: 0,
		Criteria: []CriterionResult{{ID: "B", Status: CriterionNotMet}, {ID: "A", Status: CriterionMet}},
	}, twoCriterionRubric(), nil)
	if err != nil {
		t.Fatalf("ValidateRevision: %v", err)
	}
	if len(merged) != 2 || merged[0].ID != "A" || merged[1].ID != "B" {
		t.Fatalf("merged = %+v, want [A,B] in rubric order", merged)
	}
	if merged[0].Weight != 60 || merged[1].Weight != 40 {
		t.Fatalf("merged weights = %v/%v, want the rubric's own 60/40", merged[0].Weight, merged[1].Weight)
	}
}

func TestValidateRevisionRejectsUnavailable(t *testing.T) {
	_, err := ValidateRevision(RevisionInput{
		Reason: "reviewed manually", BaseRevision: 0,
		Criteria: []CriterionResult{{ID: "A", Status: CriterionMet}, {ID: "B", Status: CriterionUnavailable}},
	}, twoCriterionRubric(), nil)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation (unavailable must be resolved)", err)
	}
}

func TestValidateRevisionRejectsUnknownCriterion(t *testing.T) {
	_, err := ValidateRevision(RevisionInput{
		Reason: "reviewed manually", BaseRevision: 0,
		Criteria: []CriterionResult{{ID: "A", Status: CriterionMet}, {ID: "Z", Status: CriterionMet}},
	}, twoCriterionRubric(), nil)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation for an unknown criterion id", err)
	}
}

func TestValidateRevisionPartialCopiesFromCurrent(t *testing.T) {
	current := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 60},
		{ID: "B", Status: CriterionNotMet, Weight: 40},
	}
	merged, err := ValidateRevision(RevisionInput{
		Reason: "corrected A only", BaseRevision: 1,
		Criteria: []CriterionResult{{ID: "A", Status: CriterionPartial}},
	}, twoCriterionRubric(), current)
	if err != nil {
		t.Fatalf("ValidateRevision: %v", err)
	}
	if merged[0].Status != CriterionPartial {
		t.Fatalf("A = %+v, want the new partial value", merged[0])
	}
	if merged[1].Status != CriterionNotMet {
		t.Fatalf("B = %+v, want copied unchanged from current", merged[1])
	}
}

func TestValidateRevisionPartialWithoutPriorResolvedValueFails(t *testing.T) {
	current := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 60},
		{ID: "B", Status: CriterionUnavailable, Weight: 40},
	}
	_, err := ValidateRevision(RevisionInput{
		Reason: "corrected A only", BaseRevision: 1,
		Criteria: []CriterionResult{{ID: "A", Status: CriterionPartial}},
	}, twoCriterionRubric(), current)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation (B has no resolved prior value to copy)", err)
	}
}

func TestApplyOverrideRecomputesPassed(t *testing.T) {
	effective := Rubric{PassThreshold: 70}
	base := ScoreResult{Status: StatusReady, Score: floatPtr(50), Passed: boolPtr(false)}
	overridden := ApplyOverride(base, floatPtr(85), effective)
	if overridden.Score == nil || *overridden.Score != 85 {
		t.Fatalf("score = %v, want 85", overridden.Score)
	}
	if overridden.Passed == nil || !*overridden.Passed {
		t.Fatal("passed must recompute to true for 85 >= 70")
	}
}

func TestApplyOverrideNilLeavesResultUnchanged(t *testing.T) {
	base := ScoreResult{Status: StatusReady, Score: floatPtr(50), Passed: boolPtr(true)}
	got := ApplyOverride(base, nil, Rubric{PassThreshold: 70})
	if *got.Score != 50 || !*got.Passed {
		t.Fatalf("got = %+v, want unchanged", got)
	}
}

func floatPtr(f float64) *float64 { return &f }
func boolPtr(b bool) *bool        { return &b }
