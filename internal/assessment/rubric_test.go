package assessment

import (
	"reflect"
	"testing"

	"emsim/internal/content"
)

func TestLoadDefaultParsesEmbeddedRubric(t *testing.T) {
	rubric, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if rubric.Version != "dds/rubric-v1" {
		t.Fatalf("version = %q, want dds/rubric-v1", rubric.Version)
	}
	if rubric.ExerciseType != content.ExerciseTypeDDSProcessing {
		t.Fatalf("exercise_type = %q", rubric.ExerciseType)
	}
	if _, ok := rubric.ByID("D_FIELD_CORRECTIONS"); !ok {
		t.Fatal("default rubric is missing D_FIELD_CORRECTIONS (ADR-019)")
	}
}

func TestMergeNilScoringIsExactlyDefault(t *testing.T) {
	base, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	merged := Merge(base, nil)
	if len(merged.Criteria) != len(base.Criteria) {
		t.Fatalf("merged criteria = %d, want %d", len(merged.Criteria), len(base.Criteria))
	}
	for i, c := range merged.Criteria {
		if !reflect.DeepEqual(c, base.Criteria[i]) {
			t.Fatalf("criterion %d = %+v, want %+v (unmodified)", i, c, base.Criteria[i])
		}
	}
}

func TestMergeWeightsCriticalAndDisabled(t *testing.T) {
	base, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	scoring := &content.Scoring{
		Weights:  map[string]float64{"G_ADDRESS": 40},
		Critical: []string{"G_ADDRESS"},
		Disabled: []string{"D_COMMENT_CONTENT"},
	}
	merged := Merge(base, scoring)
	if len(merged.Criteria) != len(base.Criteria)-1 {
		t.Fatalf("merged criteria = %d, want %d (one disabled)", len(merged.Criteria), len(base.Criteria)-1)
	}
	if _, ok := merged.ByID("D_COMMENT_CONTENT"); ok {
		t.Fatal("disabled criterion must be absent from the merged set")
	}
	address, ok := merged.ByID("G_ADDRESS")
	if !ok {
		t.Fatal("G_ADDRESS missing from merged rubric")
	}
	if address.Weight != 40 {
		t.Fatalf("G_ADDRESS weight = %v, want 40", address.Weight)
	}
	if !address.Critical {
		t.Fatal("G_ADDRESS must be critical after scoring.critical override")
	}

	// A criterion neither disabled nor mentioned in scoring.critical is
	// untouched — Merge only ever adds criticality, never removes it.
	primary, ok := merged.ByID("D_PRIMARY")
	if !ok {
		t.Fatal("D_PRIMARY missing from merged rubric")
	}
	if primary.Critical {
		t.Fatal("D_PRIMARY must not become critical from an unrelated scoring override")
	}
}

func TestMergeUnknownWeightOverrideIsIgnored(t *testing.T) {
	base, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	scoring := &content.Scoring{Weights: map[string]float64{"NOT_A_REAL_CRITERION": 99}}
	merged := Merge(base, scoring)
	if len(merged.Criteria) != len(base.Criteria) {
		t.Fatalf("merged criteria = %d, want %d unchanged (unknown id has nothing to weight)", len(merged.Criteria), len(base.Criteria))
	}
}

func TestLoadRubricByExactVersion(t *testing.T) {
	cases := []struct {
		exerciseType content.ExerciseType
		version      string
		wantID       string
		wantKind     string
	}{
		{content.ExerciseTypeDDSProcessing, "dds/rubric-v1", "D_FIELD_CORRECTIONS", "deterministic"},
		{content.ExerciseTypeOperator112Intake, "operator112/rubric-v1", "INTAKE_COMPLETENESS", "manual"},
		{content.ExerciseTypeOperator112Intake, "operator112/rubric-v2", "ADDRESS_FIELDS", "deterministic"},
	}
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			rubric, err := LoadRubric(c.exerciseType, c.version)
			if err != nil {
				t.Fatalf("LoadRubric(%q, %q): %v", c.exerciseType, c.version, err)
			}
			if rubric.Version != c.version {
				t.Fatalf("version = %q, want %q", rubric.Version, c.version)
			}
			criterion, ok := rubric.ByID(c.wantID)
			if !ok {
				t.Fatalf("rubric %q is missing %q", c.version, c.wantID)
			}
			if criterion.Kind != c.wantKind {
				t.Fatalf("%s.kind = %q, want %q", c.wantID, criterion.Kind, c.wantKind)
			}
		})
	}
}

func TestLoadRubricUnknownVersionErrors(t *testing.T) {
	if _, err := LoadRubric(content.ExerciseTypeOperator112Intake, "operator112/rubric-v99"); err == nil {
		t.Fatal("LoadRubric with an unknown version must error, not silently fall back to current")
	}
	if _, err := LoadRubric(content.ExerciseTypeDDSProcessing, "operator112/rubric-v2"); err == nil {
		t.Fatal("LoadRubric must reject a version that belongs to a different exercise_type")
	}
}

func TestLoadRubricOperator112V1PreservedUnchanged(t *testing.T) {
	// 112-6/ADR-026 must not retroactively change the rubric an
	// in-progress operator112/rubric-v1 lesson scores against.
	rubric, err := LoadRubric(content.ExerciseTypeOperator112Intake, "operator112/rubric-v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rubric.Criteria) != 3 {
		t.Fatalf("operator112/rubric-v1 criteria = %d, want 3 (unchanged pre-112-6 manual rubric)", len(rubric.Criteria))
	}
	for _, c := range rubric.Criteria {
		if c.Kind != "manual" {
			t.Fatalf("operator112/rubric-v1 criterion %s.kind = %q, want manual", c.ID, c.Kind)
		}
	}
}

func TestScoringForDispatchesByExerciseType(t *testing.T) {
	ddsScoring := &content.Scoring{Disabled: []string{"X"}}
	if got := ScoringFor(content.Body{ExerciseType: content.ExerciseTypeDDSProcessing, Reference: content.Reference{Scoring: ddsScoring}}); got != ddsScoring {
		t.Fatal("ScoringFor must read body.Reference.Scoring for DDS")
	}
	intakeScoring := &content.Scoring{Disabled: []string{"Y"}}
	body112 := content.Body{
		ExerciseType: content.ExerciseTypeOperator112Intake,
		Intake112:    &content.Intake112{Reference: content.Intake112Reference{Scoring: intakeScoring}},
	}
	if got := ScoringFor(body112); got != intakeScoring {
		t.Fatal("ScoringFor must read body.Intake112.Reference.Scoring for operator112_intake")
	}
}
