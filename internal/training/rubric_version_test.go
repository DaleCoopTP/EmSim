package training

import (
	"testing"

	"emsim/internal/content"
)

// TestRubricVersionForNewLessonDDSSelectsByJudge is ADR-034's selector
// for dds_processing: judge off keeps content.RubricVersionFor's own
// dds/rubric-v2 exactly as before ДДС-4 (never the pre-ADR-030 v1 an
// already-running lesson may still carry), judge on freezes dds/rubric-v3.
func TestRubricVersionForNewLessonDDSSelectsByJudge(t *testing.T) {
	want, err := content.RubricVersionFor(content.ExerciseTypeDDSProcessing)
	if err != nil {
		t.Fatal(err)
	}
	if want != "dds/rubric-v2" {
		t.Fatalf("content.RubricVersionFor(dds_processing) = %q, want dds/rubric-v2", want)
	}
	off, err := rubricVersionForNewLesson(content.ExerciseTypeDDSProcessing, false)
	if err != nil {
		t.Fatal(err)
	}
	if off != want {
		t.Fatalf("rubricVersionForNewLesson(dds, false) = %q, want %q", off, want)
	}
	on, err := rubricVersionForNewLesson(content.ExerciseTypeDDSProcessing, true)
	if err != nil {
		t.Fatal(err)
	}
	if on != "dds/rubric-v3" {
		t.Fatalf("rubricVersionForNewLesson(dds, true) = %q, want dds/rubric-v3", on)
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
