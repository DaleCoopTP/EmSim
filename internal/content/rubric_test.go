package content

import "testing"

func TestRubricVersionForDDS(t *testing.T) {
	got, err := RubricVersionFor(ExerciseTypeDDSProcessing)
	if err != nil {
		t.Fatal(err)
	}
	want, err := RubricVersion()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("RubricVersionFor(dds) = %q, want %q (RubricVersion's own value)", got, want)
	}
}

func TestRubricVersionForOperator112IsV2(t *testing.T) {
	// 112-6/ADR-026: a newly created 112 lesson must freeze the
	// deterministic rubric, not the pre-112-6 manual-only v1 — that
	// would silently disable auto-scoring for every new lesson.
	got, err := RubricVersionFor(ExerciseTypeOperator112Intake)
	if err != nil {
		t.Fatal(err)
	}
	if got != "operator112/rubric-v2" {
		t.Fatalf("RubricVersionFor(operator112_intake) = %q, want operator112/rubric-v2", got)
	}
}

func TestRubricVersionForUnsupportedExerciseType(t *testing.T) {
	if _, err := RubricVersionFor(ExerciseType("not_a_real_type")); err == nil {
		t.Fatal("RubricVersionFor must error for an unknown exercise_type")
	}
}
