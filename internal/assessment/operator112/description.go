package operator112

import (
	"emsim/internal/assessment"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"
)

// descriptionPresentRule is DESCRIPTION_PRESENT (ADR-026 §2.5): binary
// presence of the caller's own complaint, in their own words — no
// reference needed, no LLM judgment of quality (that is v3's job, after
// the current all-deterministic scope).
func descriptionPresentRule(ev trainingintake.EvidenceBody, snapshot training.IntakeCard, c assessment.RubricCriterion) assessment.CriterionResult {
	if knownValue(snapshot.Complaint) != "" {
		one := 1.0
		return assessment.CriterionResult{ID: c.ID, Status: assessment.CriterionMet, Score: &one, Weight: c.Weight, Critical: c.Critical, EvidenceRefs: evidenceRefs(ev)}
	}
	zero := 0.0
	return assessment.CriterionResult{
		ID: c.ID, Status: assessment.CriterionNotMet, Score: &zero, Weight: c.Weight, Critical: c.Critical,
		EvidenceRefs: evidenceRefs(ev), Explanation: "описание со слов заявителя не заполнено",
	}
}
