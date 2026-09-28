package assessment

import (
	"reflect"
	"testing"

	"emsim/internal/content"
	"emsim/internal/training"
)

func ddsV2(t *testing.T) Rubric {
	t.Helper()
	r, err := LoadRubric(content.ExerciseTypeDDSProcessing, "dds/rubric-v2")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func weightOf(r Rubric, id string) (float64, bool) {
	c, ok := r.ByID(id)
	return c.Weight, ok
}

func TestMergeLessonWithoutLessonScoringIsMerge(t *testing.T) {
	base := ddsV2(t)
	scenario := &content.Scoring{Weights: map[string]float64{"T_OPEN": 30}, Critical: []string{"S_SEQUENCE"}, Disabled: []string{"C_CALLS"}}
	if got, want := MergeLesson(base, nil, scenario), Merge(base, scenario); !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeLesson(nil lesson) differs from Merge:\n got %+v\nwant %+v", got, want)
	}
	if got, want := MergeLesson(base, nil, nil), Merge(base, nil); !reflect.DeepEqual(got, want) {
		t.Fatal("MergeLesson(nil, nil) differs from Merge(nil)")
	}
}

func TestMergeLessonLayerOrder(t *testing.T) {
	base := ddsV2(t)
	lessonWeights := map[string]float64{}
	for _, c := range base.Criteria {
		lessonWeights[c.ID] = 0
	}
	lessonWeights["T_OPEN"] = 60
	lessonWeights["D_PRIMARY"] = 40
	lesson := &training.LessonScoring{Weights: lessonWeights, PassThreshold: 85}
	scenario := &content.Scoring{
		Weights:  map[string]float64{"T_OPEN": 5, "D_PRIMARY": 5}, // the lesson's weights win
		Critical: []string{"T_PROGRESS"},                          // scenario critical still applies
		Disabled: []string{"C_CALLS"},                             // scenario disabled still applies
	}
	merged := MergeLesson(base, lesson, scenario)

	if w, _ := weightOf(merged, "T_OPEN"); w != 60 {
		t.Fatalf("T_OPEN weight=%v: the lesson's 60 must beat the scenario's 5", w)
	}
	if w, _ := weightOf(merged, "D_PRIMARY"); w != 40 {
		t.Fatalf("D_PRIMARY weight=%v want 40", w)
	}
	if _, ok := merged.ByID("C_CALLS"); ok {
		t.Fatal("scenario-disabled criterion must stay dropped even though the lesson weighs it")
	}
	if c, _ := merged.ByID("T_PROGRESS"); !c.Critical {
		t.Fatal("scenario-critical must still apply")
	}
	if merged.PassThreshold != 85 {
		t.Fatalf("threshold=%v want the lesson's 85", merged.PassThreshold)
	}
	if merged.CriticalCap != base.CriticalCap {
		t.Fatalf("critical_cap=%v must stay the rubric's %v", merged.CriticalCap, base.CriticalCap)
	}
	if w, _ := weightOf(base, "T_OPEN"); w == 60 {
		t.Fatal("MergeLesson mutated the shared base rubric")
	}
}

func TestMergeLessonScenarioWeightsApplyOnlyWithoutLessonScoring(t *testing.T) {
	base := ddsV2(t)
	scenario := &content.Scoring{Weights: map[string]float64{"T_OPEN": 25}}
	if w, _ := weightOf(MergeLesson(base, nil, scenario), "T_OPEN"); w != 25 {
		t.Fatalf("without lesson scoring the scenario weight must apply, got %v", w)
	}
}

// TestLessonThresholdDecidesPassed drives Score with the same results under
// two thresholds: only the lesson's own threshold changes passed.
func TestLessonThresholdDecidesPassed(t *testing.T) {
	base := ddsV2(t)
	weights := map[string]float64{}
	for _, c := range base.Criteria {
		weights[c.ID] = 0
	}
	weights["T_OPEN"] = 75
	weights["D_PRIMARY"] = 25
	results := []CriterionResult{
		{ID: "T_OPEN", Status: CriterionMet, Weight: 75},
		{ID: "D_PRIMARY", Status: CriterionNotMet, Weight: 25},
	}
	for _, tc := range []struct {
		threshold float64
		passed    bool
	}{{70, true}, {75, true}, {80, false}} {
		effective := MergeLesson(base, &training.LessonScoring{Weights: weights, PassThreshold: tc.threshold}, nil)
		got := Score(results, effective)
		if got.Score == nil || *got.Score != 75 || got.Passed == nil || *got.Passed != tc.passed {
			t.Fatalf("threshold %v: %+v (score 75, want passed=%v)", tc.threshold, got, tc.passed)
		}
	}
}
