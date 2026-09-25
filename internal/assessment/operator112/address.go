package operator112

import (
	"fmt"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/content/normalize"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"
)

// addressFieldsRule is ADDRESS_FIELDS (35 points, ADR-026 §2.1): each
// rubric.operator112.json params.fields[] entry compares one address
// component of the scored snapshot against reference.expected_card.
// address's own field, normalized (internal/content/normalize) and
// accepting reference.alternatives. A field the reference leaves empty
// is not_applicable within the block (interpretation §10.5 — it neither
// helps nor hurts); a reference with no expected_card at all makes the
// whole block a plain not_met/0 (ADR-026's "эталон отсутствует" rule —
// the same outcome as getting every field wrong, not a different
// status).
//
// A mismatched field that looks like the AI caller's own paraphrase
// (aiDivergent, 112-6's c6) marks the *whole* block unavailable rather
// than just that one field — CriterionResult has no per-field score of
// its own, only Details, and Score.Compute's unavailable rule already
// operates at the criterion level.
func addressFieldsRule(ev trainingintake.EvidenceBody, intake *content.Intake112, snapshot training.IntakeCard, c assessment.RubricCriterion) assessment.CriterionResult {
	ref := intake.Reference
	var fields []addressFieldParam
	_ = decodeParam(c.Params, "fields", &fields)
	noReferenceExplanation := paramString(c.Params, "no_reference_explanation", "эталон не задан")

	if ref.ExpectedCard == nil {
		zero := 0.0
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionNotMet, Score: &zero, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Explanation: noReferenceExplanation,
		}
	}

	var totalPoints, earnedPoints float64
	var divergent bool
	details := make([]assessment.CriterionDetail, 0, len(fields))
	for _, f := range fields {
		expected := addressExpectedValue(ref.ExpectedCard.Address, f.Path)
		if expected == "" {
			details = append(details, assessment.CriterionDetail{
				Key: f.Path, Label: f.Label, MaxPoints: f.Points, Status: assessment.CriterionNotApplicable,
			})
			continue
		}
		totalPoints += f.Points
		actual := knownValue(addressActualField(snapshot.Address, f.Path))
		matched := actual != "" && normalize.Matches(actual, expected, alternativesFor(ref, "address."+f.Path))
		points := 0.0
		status := assessment.CriterionNotMet
		if matched {
			points, status = f.Points, assessment.CriterionMet
		} else if aiDivergent(ev, intake, actual) {
			status = assessment.CriterionUnavailable
			divergent = true
		}
		earnedPoints += points
		detail := assessment.CriterionDetail{Key: f.Path, Label: f.Label, Points: points, MaxPoints: f.Points, Status: status, Expected: strPtr(expected)}
		if actual != "" {
			detail.Actual = strPtr(actual)
		}
		details = append(details, detail)
	}

	if divergent {
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionUnavailable, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Details: details,
			Explanation: "заявитель мог сообщить иначе, чем в эталоне — требуется проверка преподавателя",
		}
	}

	if totalPoints == 0 {
		// Every field the rubric knows about is either absent from the
		// reference or the reference object had nothing scorable — same
		// "nothing to score against" outcome as no reference at all.
		zero := 0.0
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionNotMet, Score: &zero, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Explanation: noReferenceExplanation, Details: details,
		}
	}

	score := earnedPoints / totalPoints
	status := statusFromScore(score)
	return assessment.CriterionResult{
		ID: c.ID, Status: status, Score: &score, Weight: c.Weight, Critical: c.Critical,
		EvidenceRefs: evidenceRefs(ev), Details: details,
		Explanation: fmt.Sprintf("%.0f из %.0f баллов по полям адреса", earnedPoints, totalPoints),
	}
}

// statusFromScore maps a 0..1 fraction to met/partial/not_met — the
// three-way split every block criterion in this package uses.
func statusFromScore(score float64) assessment.CriterionStatus {
	switch {
	case score >= 1:
		return assessment.CriterionMet
	case score <= 0:
		return assessment.CriterionNotMet
	default:
		return assessment.CriterionPartial
	}
}

func strPtr(s string) *string { return &s }
