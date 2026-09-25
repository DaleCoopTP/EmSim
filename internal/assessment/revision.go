package assessment

import (
	"fmt"
	"strings"
)

// RevisionInput is the caller-supplied half of a new expert revision —
// openapi.yaml's AssessmentRevision, decoded by the HTTP layer.
type RevisionInput struct {
	Reason        string
	Criteria      []CriterionResult
	ScoreOverride *float64
	BaseRevision  int
}

// ValidateRevision checks in against the rubric it will be scored with
// and, when BaseRevision > 0, the existing final assessment's own
// criteria (RFC-001 §7.4: "При base_revision=0 полный набор применимых
// критериев обязателен; иначе допускаются поправки, остальные
// копируются из текущей ревизии"). It returns the full, ordered
// (effective.Criteria order) criteria set to persist — never a partial
// one — so the caller never re-derives "copy the rest from current"
// logic itself. No returned criterion is ever CriterionUnavailable: the
// rubric requires "Все unavailable должны быть разрешены преподавателем"
// before it accepts a revision at all.
func ValidateRevision(in RevisionInput, effective Rubric, current []CriterionResult) ([]CriterionResult, error) {
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 3 || len(reason) > 2000 {
		return nil, validationErr("reason", "must be 3..2000 characters")
	}
	if in.BaseRevision < 0 {
		return nil, validationErr("base_revision", "must be >= 0")
	}

	given := make(map[string]CriterionResult, len(in.Criteria))
	for _, c := range in.Criteria {
		rc, ok := effective.ByID(c.ID)
		if !ok {
			return nil, validationErr("criteria", fmt.Sprintf("unknown or disabled criterion %q", c.ID))
		}
		if c.Status == CriterionUnavailable {
			return nil, validationErr("criteria", fmt.Sprintf("criterion %q must be resolved, not left unavailable", c.ID))
		}
		// 112-6/ADR-026: penalty_points is meaningful only for kind=penalty
		// (a subtraction, never a weighted 0..1 fraction) and must never be
		// negative — a negative "penalty" would silently inflate the score
		// past what any deterministic rule could produce.
		if rc.Kind == "penalty" {
			if c.PenaltyPoints != nil && *c.PenaltyPoints < 0 {
				return nil, validationErr("criteria", fmt.Sprintf("criterion %q: penalty_points must be >= 0", c.ID))
			}
		} else if c.PenaltyPoints != nil {
			return nil, validationErr("criteria", fmt.Sprintf("criterion %q: penalty_points only applies to a penalty criterion", c.ID))
		}
		given[c.ID] = c
	}
	currentByID := make(map[string]CriterionResult, len(current))
	for _, c := range current {
		currentByID[c.ID] = c
	}

	merged := make([]CriterionResult, 0, len(effective.Criteria))
	for _, rc := range effective.Criteria {
		if result, ok := given[rc.ID]; ok {
			result.Weight = rc.Weight
			result.Critical = rc.Critical
			merged = append(merged, result)
			continue
		}
		if in.BaseRevision == 0 {
			return nil, validationErr("criteria", fmt.Sprintf("missing required criterion %q", rc.ID))
		}
		existing, ok := currentByID[rc.ID]
		if !ok || existing.Status == CriterionUnavailable {
			return nil, validationErr("criteria", fmt.Sprintf("criterion %q has no prior resolved value to copy", rc.ID))
		}
		existing.Weight = rc.Weight
		existing.Critical = rc.Critical
		merged = append(merged, existing)
	}
	return merged, nil
}

// ApplyOverride replaces a computed ScoreResult's score with the
// instructor's own number, recomputing passed by the same threshold/
// critical rule Score itself uses (RFC-001 §7.4: "score_override эксперта
// заменяет вычисленный балл; passed пересчитывается по тем же правилам").
// A nil override leaves result untouched.
func ApplyOverride(result ScoreResult, override *float64, effective Rubric) ScoreResult {
	if override == nil {
		return result
	}
	score := *override
	passed := score >= effective.PassThreshold && len(result.CriticalErrors) == 0
	result.Score = &score
	result.Passed = &passed
	return result
}
