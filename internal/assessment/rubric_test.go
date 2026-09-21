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
