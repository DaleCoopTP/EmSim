package training

import (
	"testing"

	"emsim/internal/content"
)

// TestRubricVersionForNewLessonDDSIgnoresJudgeFlag is ADR-028's own
// isolation requirement: judgeEnabled must never change dds_processing's
// own rubric version — only content.RubricVersionFor decides it, exactly
// as before this ADR.
func TestRubricVersionForNewLessonDDSIgnoresJudgeFlag(t *testing.T) {
	want, err := content.RubricVersionFor(content.ExerciseTypeDDSProcessing)
	if err != nil {
		t.Fatal(err)
	}
	// ДДС-3/ADR-032: a newly created DDS lesson freezes dds/rubric-v2,
	// not the pre-ADR-030 dds/rubric-v1 an already-running lesson may
	// still carry.
	if want != "dds/rubric-v2" {
		t.Fatalf("content.RubricVersionFor(dds_processing) = %q, want dds/rubric-v2", want)
	}
	for _, judgeEnabled := range []bool{false, true} {
		got, err := rubricVersionForNewLesson(content.ExerciseTypeDDSProcessing, judgeEnabled)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("rubricVersionForNewLesson(dds, %v) = %q, want %q", judgeEnabled, got, want)
		}
	}
}

// TestRubricVersionForNewLessonOperator112SelectsByJudge exercises
// ADR-028's own selector for the exercise_type it actually affects.
func TestRubricVersionForNewLessonOperator112SelectsByJudge(t *testing.T) {
	off, err := rubricVersionForNewLesson(content.ExerciseTypeOperator112Intake, false)
	if err != nil {
		t.Fatal(err)
	}
	if off != "operator112/rubric-v2" {
		t.Fatalf("rubricVersionForNewLesson(operator112, false) = %q, want operator112/rubric-v2", off)
	}
	on, err := rubricVersionForNewLesson(content.ExerciseTypeOperator112Intake, true)
	if err != nil {
		t.Fatal(err)
	}
	if on != "operator112/rubric-v3" {
		t.Fatalf("rubricVersionForNewLesson(operator112, true) = %q, want operator112/rubric-v3", on)
	}
}
