package assessment

import "testing"

func TestStripExpectedClearsExpectedKeepsActual(t *testing.T) {
	expected := "Тверская"
	actual := "Садовая"
	criteria := []CriterionResult{
		{
			ID: "ADDRESS_FIELDS", Status: CriterionPartial,
			Details: []CriterionDetail{
				{Key: "street", Label: "Улица", Points: 0, MaxPoints: 8, Status: CriterionNotMet, Actual: &actual, Expected: &expected},
			},
		},
		{ID: "DESCRIPTION_PRESENT", Status: CriterionMet},
	}
	stripped := StripExpected(criteria)
	if len(stripped) != 2 {
		t.Fatalf("len(stripped) = %d, want 2", len(stripped))
	}
	if stripped[0].Details[0].Expected != nil {
		t.Fatal("Expected must be cleared for a trainee-facing projection")
	}
	if stripped[0].Details[0].Actual == nil || *stripped[0].Details[0].Actual != "Садовая" {
		t.Fatal("Actual must be preserved")
	}
	// The original slice must not be mutated — StripExpected is a pure
	// projection, not an in-place edit of the stored assessment.
	if criteria[0].Details[0].Expected == nil {
		t.Fatal("StripExpected must not mutate its input")
	}
}
