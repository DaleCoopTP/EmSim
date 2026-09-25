package assessment

import "testing"

func TestScoreNormalizesAfterExclusions(t *testing.T) {
	effective := Rubric{PassThreshold: 70, CriticalCap: 40}
	results := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 50},
		{ID: "B", Status: CriterionNotMet, Weight: 50},
		{ID: "C", Status: CriterionNotApplicable, Weight: 1000}, // excluded: must not dilute the other two
	}
	got := Score(results, effective)
	if got.Status != StatusReady || got.Score == nil {
		t.Fatalf("got = %+v, want ready with a score", got)
	}
	// A and B are re-normalized to 50/50 of 100 -> 50*1 + 50*0 = 50.
	if *got.Score != 50 {
		t.Fatalf("score = %v, want 50", *got.Score)
	}
	if got.Passed == nil || *got.Passed {
		t.Fatalf("passed = %v, want false (50 < 70 threshold)", got.Passed)
	}
}

func TestScoreUnavailableForcesNeedsReviewWithNilScore(t *testing.T) {
	effective := Rubric{PassThreshold: 70, CriticalCap: 40}
	results := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 50},
		{ID: "B", Status: CriterionUnavailable, Weight: 50},
	}
	got := Score(results, effective)
	if got.Status != StatusNeedsReview {
		t.Fatalf("status = %q, want needs_review", got.Status)
	}
	if got.Score != nil || got.Passed != nil {
		t.Fatalf("score/passed = %v/%v, want nil/nil (ADR-016 A3: never perenormalize unavailable)", got.Score, got.Passed)
	}
}

func TestScoreCriticalNotMetCapsScoreAndFails(t *testing.T) {
	effective := Rubric{PassThreshold: 50, CriticalCap: 40}
	results := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 90},
		{ID: "B", Status: CriterionNotMet, Weight: 10, Critical: true},
	}
	got := Score(results, effective)
	// Uncapped: 90*1 + 10*0 = 90, well above both the 50 threshold and
	// the 40 cap — the critical failure must still cap it and fail it.
	if got.Score == nil || *got.Score != 40 {
		t.Fatalf("score = %v, want capped at 40", got.Score)
	}
	if got.Passed == nil || *got.Passed {
		t.Fatal("passed must be false when a critical criterion is not_met, regardless of score")
	}
	if len(got.CriticalErrors) != 1 || got.CriticalErrors[0] != "B" {
		t.Fatalf("critical_errors = %v, want [B]", got.CriticalErrors)
	}
}

func TestScorePartialUsesItsOwnValueOrDefaultHalf(t *testing.T) {
	half := 0.5
	quarter := 0.25
	effective := Rubric{PassThreshold: 0, CriticalCap: 100}
	results := []CriterionResult{
		{ID: "A", Status: CriterionPartial, Weight: 100, Score: &half},
	}
	got := Score(results, effective)
	if got.Score == nil || *got.Score != 50 {
		t.Fatalf("score = %v, want 50 (partial 0.5 * weight 100)", got.Score)
	}

	results[0].Score = &quarter
	got = Score(results, effective)
	if got.Score == nil || *got.Score != 25 {
		t.Fatalf("score = %v, want 25 (partial 0.25 * weight 100)", got.Score)
	}

	results[0].Score = nil
	got = Score(results, effective)
	if got.Score == nil || *got.Score != 50 {
		t.Fatalf("score = %v, want 50 (default partial value 0.5)", got.Score)
	}
}

func TestScoreAllNotApplicableYieldsZeroWithoutDivideByZero(t *testing.T) {
	effective := Rubric{PassThreshold: 70, CriticalCap: 40}
	results := []CriterionResult{
		{ID: "A", Status: CriterionNotApplicable, Weight: 50},
		{ID: "B", Status: CriterionNotApplicable, Weight: 50},
	}
	got := Score(results, effective)
	if got.Status != StatusReady || got.Score == nil || *got.Score != 0 {
		t.Fatalf("got = %+v, want ready with score 0", got)
	}
}

func TestScorePenaltySubtractsAfterNormalization(t *testing.T) {
	effective := Rubric{
		PassThreshold: 70, CriticalCap: 100,
		Criteria: []RubricCriterion{
			{ID: "A", Kind: "deterministic", Weight: 100},
			{ID: "P", Kind: "penalty"},
		},
	}
	ten := 10.0
	results := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 100},
		{ID: "P", PenaltyPoints: &ten},
	}
	got := Score(results, effective)
	if got.Status != StatusReady || got.Score == nil {
		t.Fatalf("got = %+v, want ready with a score", got)
	}
	if *got.Score != 90 {
		t.Fatalf("score = %v, want 90 (100 normalized - 10 penalty)", *got.Score)
	}
}

func TestScorePenaltyExcludedFromNormalization(t *testing.T) {
	// A penalty criterion must never dilute the weighted average the way
	// an ordinary criterion with a huge weight would (mirrors
	// TestScoreNormalizesAfterExclusions' not_applicable case).
	effective := Rubric{
		PassThreshold: 0, CriticalCap: 100,
		Criteria: []RubricCriterion{
			{ID: "A", Kind: "deterministic", Weight: 50},
			{ID: "B", Kind: "deterministic", Weight: 50},
			{ID: "P", Kind: "penalty"},
		},
	}
	zero := 0.0
	results := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 50},
		{ID: "B", Status: CriterionNotMet, Weight: 50},
		{ID: "P", Weight: 1000, PenaltyPoints: &zero},
	}
	got := Score(results, effective)
	if got.Score == nil || *got.Score != 50 {
		t.Fatalf("score = %v, want 50 (penalty's own huge Weight must not enter normalization)", got.Score)
	}
}

func TestScorePenaltyClampsToZero(t *testing.T) {
	effective := Rubric{
		PassThreshold: 1, CriticalCap: 100,
		Criteria: []RubricCriterion{
			{ID: "A", Kind: "deterministic", Weight: 100},
			{ID: "P", Kind: "penalty"},
		},
	}
	big := 500.0
	results := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 100},
		{ID: "P", PenaltyPoints: &big},
	}
	got := Score(results, effective)
	if got.Score == nil || *got.Score != 0 {
		t.Fatalf("score = %v, want 0 (clamped, never negative)", got.Score)
	}
	if got.Passed == nil || *got.Passed {
		t.Fatal("passed must be false at a clamped 0 score below any positive threshold")
	}
}

func TestScoreMultiplePenaltiesSum(t *testing.T) {
	effective := Rubric{
		PassThreshold: 0, CriticalCap: 100,
		Criteria: []RubricCriterion{
			{ID: "A", Kind: "deterministic", Weight: 100},
			{ID: "P1", Kind: "penalty"},
			{ID: "P2", Kind: "penalty"},
		},
	}
	five, ten := 5.0, 10.0
	results := []CriterionResult{
		{ID: "A", Status: CriterionMet, Weight: 100},
		{ID: "P1", PenaltyPoints: &five},
		{ID: "P2", PenaltyPoints: &ten},
	}
	got := Score(results, effective)
	if got.Score == nil || *got.Score != 85 {
		t.Fatalf("score = %v, want 85 (100 - 5 - 10)", got.Score)
	}
}
