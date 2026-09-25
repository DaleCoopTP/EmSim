package assessment

import "math"

// ScoreResult is Score's output — the four columns one rule/judge pass
// determines together (RFC-001 §7.4/ADR-013): they are never set
// independently, so there is no way to end up with e.g. a non-nil score
// and a needs_review status.
type ScoreResult struct {
	Status         Status
	Score          *float64 // 0..100, nil unless Status == StatusReady
	Passed         *bool
	CriticalErrors []string
}

// Score aggregates one evaluator pass's CriterionResult set against the
// effective rubric it was computed from (ADR-013/016 A3/A4). Disabled
// criteria never appear in results at all — Merge already dropped them
// from the rubric the evaluator ran against, so there is nothing to
// filter here. not_applicable is excluded from normalization. Any
// remaining unavailable makes the whole assessment needs_review with
// score/passed left nil: unavailable is never silently excluded merely
// to manufacture a complete-looking score (this is the one rule ADR-016
// A3 is about). A critical criterion that came back not_met is recorded
// even though the resulting status may already be needs_review — a
// later expert revision needs to see it too.
//
// results is expected in a stable order (the evaluator iterates
// effective.Criteria in order), so CriticalErrors comes out deterministic
// across repeated runs of the same input — required for
// assessment_inputs' own reproducibility guarantee (ADR-006).
func Score(results []CriterionResult, effective Rubric) ScoreResult {
	var unavailable bool
	var criticalErrors []string
	type weighted struct {
		weight float64
		value  float64
	}
	var applicable []weighted
	var totalPenalty float64
	kindByID := make(map[string]string, len(effective.Criteria))
	for _, c := range effective.Criteria {
		kindByID[c.ID] = c.Kind
	}
	for _, r := range results {
		// 112-6/ADR-026: a penalty criterion never enters weight
		// normalization at all — it has no "correct" fraction of the
		// 100-point total the way met/partial/not_met criteria do, only
		// points subtracted after normalization. not_applicable is the
		// objective-inapplicability case (the milestone this penalty
		// depends on was never reached) and simply contributes nothing,
		// same as a nil PenaltyPoints. unavailable is the one status a
		// penalty and an ordinary criterion share: 112-6's AI-caller
		// divergence carve-out (ADR-026 — a free_text field that
		// disagrees with the reference but matches what the model/
		// fallback caller actually said) can mark a penalty unavailable
		// too, and that must force needs_review exactly like it does for
		// any other criterion, not silently charge/skip the penalty.
		if kindByID[r.ID] == "penalty" {
			if r.Status == CriterionUnavailable {
				unavailable = true
				continue
			}
			if r.PenaltyPoints != nil {
				totalPenalty += *r.PenaltyPoints
			}
			continue
		}
		switch r.Status {
		case CriterionNotApplicable:
			continue
		case CriterionUnavailable:
			unavailable = true
			continue
		}
		if r.Critical && r.Status == CriterionNotMet {
			criticalErrors = append(criticalErrors, r.ID)
		}
		applicable = append(applicable, weighted{weight: r.Weight, value: criterionValue(r)})
	}
	if unavailable {
		return ScoreResult{Status: StatusNeedsReview, CriticalErrors: criticalErrors}
	}

	var totalWeight float64
	for _, w := range applicable {
		totalWeight += w.weight
	}
	var score float64
	if totalWeight > 0 {
		for _, w := range applicable {
			score += (w.weight / totalWeight) * 100 * w.value
		}
	}
	if len(criticalErrors) > 0 && score > effective.CriticalCap {
		score = effective.CriticalCap
	}
	score -= totalPenalty
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	score = math.Round(score*100) / 100
	passed := score >= effective.PassThreshold && len(criticalErrors) == 0
	return ScoreResult{Status: StatusReady, Score: &score, Passed: &passed, CriticalErrors: criticalErrors}
}

// criterionValue maps one criterion's status to the 0..1 value Score
// weighs it by. partial defaults to 0.5 (RFC-001/ADR-014's only
// partial-credit value in MVP) unless the evaluator set a more specific
// Score.
func criterionValue(r CriterionResult) float64 {
	switch r.Status {
	case CriterionMet:
		return 1
	case CriterionNotMet:
		return 0
	case CriterionPartial:
		if r.Score != nil {
			return *r.Score
		}
		return 0.5
	default:
		return 0
	}
}
