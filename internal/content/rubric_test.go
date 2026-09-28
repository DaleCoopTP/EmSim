package content

import "testing"

// TestRubricVersionForDDSIsV2 is ДДС-3/ADR-032: a newly created DDS
// lesson must freeze dds/rubric-v2 (T_PROGRESS/S_SEQUENCE/C_CALLS), not
// the pre-ADR-030 dds/rubric-v1 RubricVersion() still reads — that would
// silently keep scoring a card-editing/call-log workflow the trainee no
// longer performs.
func TestRubricVersionForDDSIsV2(t *testing.T) {
	got, err := RubricVersionFor(ExerciseTypeDDSProcessing)
	if err != nil {
		t.Fatal(err)
	}
	if got != "dds/rubric-v2" {
		t.Fatalf("RubricVersionFor(dds_processing) = %q, want dds/rubric-v2", got)
	}
	legacy, err := RubricVersion()
	if err != nil {
		t.Fatal(err)
	}
	if legacy != "dds/rubric-v1" {
		t.Fatalf("RubricVersion() = %q, want dds/rubric-v1 (rubric.default.json stays frozen for old lessons)", legacy)
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

// TestOperator112RubricVersionSelectsByJudge is ADR-028's own selector:
// judgeEnabled=false must exactly match RubricVersionFor's own
// operator112 case (never a silent v3 without a configured judge), and
// judgeEnabled=true must be a different, newer version that still has
// every rubric-v2 criterion (checked in evaluator_test.go/check.py, not
// duplicated here).
func TestOperator112RubricVersionSelectsByJudge(t *testing.T) {
	off, err := Operator112RubricVersion(false)
	if err != nil {
		t.Fatal(err)
	}
	want, err := RubricVersionFor(ExerciseTypeOperator112Intake)
	if err != nil {
		t.Fatal(err)
	}
	if off != want {
		t.Fatalf("Operator112RubricVersion(false) = %q, want %q (RubricVersionFor's own value)", off, want)
	}
	on, err := Operator112RubricVersion(true)
	if err != nil {
		t.Fatal(err)
	}
	if on != "operator112/rubric-v3" {
		t.Fatalf("Operator112RubricVersion(true) = %q, want operator112/rubric-v3", on)
	}
}
