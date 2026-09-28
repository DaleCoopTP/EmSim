package training

import (
	"errors"
	"testing"

	"emsim/internal/content"
)

// v2Weights returns dds/rubric-v2's own weights, the base each case perturbs.
func v2Weights(t *testing.T) map[string]float64 {
	t.Helper()
	d, err := content.DDSRubricDefaults("dds/rubric-v2")
	if err != nil {
		t.Fatal(err)
	}
	return d.Weights
}

func TestValidateLessonScoring(t *testing.T) {
	base := v2Weights(t)
	clone := func() map[string]float64 {
		out := make(map[string]float64, len(base))
		for k, v := range base {
			out[k] = v
		}
		return out
	}
	shifted := clone() // moves 10 points from D_PRIMARY to T_OPEN, still sums to 100
	shifted["D_PRIMARY"] -= 10
	shifted["T_OPEN"] += 10
	missing := clone()
	delete(missing, "T_OPEN")
	extra := clone()
	extra["G_GRAMMAR"] = 0
	unknown := clone()
	unknown["NOPE"] = 0
	negative := clone()
	negative["D_PRIMARY"] += 5
	negative["T_OPEN"] -= 15 // T_OPEN was 10: -5, sum stays 100
	over := clone()
	over["T_OPEN"] += 1
	zero := clone() // a zero weight is legal (informational criterion)
	zero["D_PRIMARY"] += zero["T_OPEN"]
	zero["T_OPEN"] = 0

	for _, tc := range []struct {
		name      string
		version   string
		scoring   LessonScoring
		wantField string // empty = valid
	}{
		{"unchanged weights", "dds/rubric-v2", LessonScoring{Weights: clone(), PassThreshold: 70}, ""},
		{"shifted weights and threshold 90", "dds/rubric-v2", LessonScoring{Weights: shifted, PassThreshold: 90}, ""},
		{"zero weight is informational", "dds/rubric-v2", LessonScoring{Weights: zero, PassThreshold: 70}, ""},
		{"threshold 0 and 100 are legal", "dds/rubric-v2", LessonScoring{Weights: clone(), PassThreshold: 100}, ""},
		{"threshold above 100", "dds/rubric-v2", LessonScoring{Weights: clone(), PassThreshold: 101}, "scoring.pass_threshold"},
		{"threshold below 0", "dds/rubric-v2", LessonScoring{Weights: clone(), PassThreshold: -1}, "scoring.pass_threshold"},
		{"missing criterion", "dds/rubric-v2", LessonScoring{Weights: missing, PassThreshold: 70}, "scoring.weights.T_OPEN"},
		{"criterion of another version", "dds/rubric-v2", LessonScoring{Weights: extra, PassThreshold: 70}, "scoring.weights.G_GRAMMAR"},
		{"unknown criterion", "dds/rubric-v2", LessonScoring{Weights: unknown, PassThreshold: 70}, "scoring.weights.NOPE"},
		{"negative weight", "dds/rubric-v2", LessonScoring{Weights: negative, PassThreshold: 70}, "scoring.weights.T_OPEN"},
		{"sum not 100", "dds/rubric-v2", LessonScoring{Weights: over, PassThreshold: 70}, "scoring.weights"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLessonScoring(tc.version, tc.scoring)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.wantField {
				t.Fatalf("err=%v want field %q", err, tc.wantField)
			}
		})
	}
}
