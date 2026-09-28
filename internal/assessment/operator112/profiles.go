package operator112

import (
	"fmt"
	"sort"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/content/normalize"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"
)

// expectedProfileIDs resolves reference.expected_types into the set of
// profile-card ids the catalog says those incident types require —
// PROFILE_CARDS' own denominator and P_EXTRA_PROFILE's own "expected"
// set. A nil catalog (defensively — every card_only/full_case item
// copies the catalog at offer time, so this should not happen in
// practice) yields no ids rather than panicking.
func expectedProfileIDs(catalog *content.IntakeCatalog, expectedTypes []string) []string {
	if catalog == nil || len(expectedTypes) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(expectedTypes))
	for _, t := range expectedTypes {
		wanted[t] = true
	}
	seen := map[string]bool{}
	var ids []string
	for _, t := range catalog.Types {
		if !wanted[t.ID] {
			continue
		}
		for _, pid := range t.ProfileIDs {
			if !seen[pid] {
				seen[pid] = true
				ids = append(ids, pid)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// profileFieldByID finds one field's own IntakeProfileField definition
// within a catalog profile, by id.
func profileFieldByID(catalog *content.IntakeCatalog, profileID, fieldID string) (content.IntakeProfileField, bool) {
	if catalog == nil {
		return content.IntakeProfileField{}, false
	}
	for _, p := range catalog.Profiles {
		if p.ID != profileID {
			continue
		}
		for _, f := range p.Fields {
			if f.ID == fieldID {
				return f, true
			}
		}
	}
	return content.IntakeProfileField{}, false
}

func profileNameByID(catalog *content.IntakeCatalog, profileID string) string {
	if catalog == nil {
		return profileID
	}
	for _, p := range catalog.Profiles {
		if p.ID == profileID {
			return p.Name
		}
	}
	return profileID
}

// profileCardsRule is PROFILE_CARDS (25 points, ADR-026 §2.2, "option
// 2" — split evenly among the expected cards, each card scored by its
// own fraction of correctly answered fields). A profile the reference
// has no expected_types for at all makes the whole block not_met/0
// (ADR-026's "эталон отсутствует" rule). An expected card the reference
// has no reference.expected_profiles entry for scores its own share as
// 0 (interpretation §10.5's card-level counterpart), distinct from a
// card the trainee simply never created (also 0, but reported
// differently in Details).
func profileCardsRule(ev trainingintake.EvidenceBody, ref content.Intake112Reference, snapshot training.IntakeCard, c assessment.RubricCriterion) assessment.CriterionResult {
	noReferenceExplanation := paramString(c.Params, "no_reference_explanation", "эталон не задан")
	missingCardExplanation := paramString(c.Params, "missing_reference_card_explanation", "карта не входит в заданный эталон")

	expectedIDs := expectedProfileIDs(ev.IntakeState.Catalog, ref.ExpectedTypes)
	if len(expectedIDs) == 0 {
		zero := 0.0
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionNotMet, Score: &zero, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Explanation: noReferenceExplanation,
		}
	}

	share := 1.0 / float64(len(expectedIDs))
	var earned float64
	details := make([]assessment.CriterionDetail, 0, len(expectedIDs))
	for _, id := range expectedIDs {
		label := profileNameByID(ev.IntakeState.Catalog, id)
		expectedAnswers, hasReference := ref.ExpectedProfiles[id]
		actualProfile, hasActual := snapshot.Profiles[id]

		if !hasReference {
			details = append(details, assessment.CriterionDetail{Key: id, Label: label, MaxPoints: share, Status: assessment.CriterionNotApplicable, Actual: profilePresenceLabel(hasActual)})
			continue
		}
		if !hasActual || len(expectedAnswers) == 0 {
			details = append(details, assessment.CriterionDetail{Key: id, Label: label, MaxPoints: share, Status: assessment.CriterionNotMet, Expected: strPtr(missingCardExplanation)})
			continue
		}
		var matched int
		for fieldID, expectedValue := range expectedAnswers {
			field, _ := profileFieldByID(ev.IntakeState.Catalog, id, fieldID)
			actual, ok := actualProfile.Answers[fieldID]
			if ok && profileAnswerMatches(field, actual, expectedValue) {
				matched++
			}
		}
		fraction := float64(matched) / float64(len(expectedAnswers))
		cardPoints := share * fraction
		earned += cardPoints
		details = append(details, assessment.CriterionDetail{
			Key: id, Label: label, Points: cardPoints, MaxPoints: share, Status: statusFromScore(fraction),
			Expected: strPtr(fmt.Sprintf("%d/%d полей", matched, len(expectedAnswers))),
		})
	}

	score := earned
	return assessment.CriterionResult{
		ID: c.ID, Status: statusFromScore(score), Score: &score, Weight: c.Weight, Critical: c.Critical,
		EvidenceRefs: evidenceRefs(ev), Details: details,
	}
}

func profilePresenceLabel(present bool) *string {
	if present {
		return strPtr("заполнена (не в эталоне)")
	}
	return nil
}

// profileAnswerMatches compares one profile field's actual answer
// (training.IntakeProfileAnswer: an expected "unknown" matches only the
// card's own "Неизвестно"; otherwise only State=="known" counts) against
// its expected value: single/text fields compare normalized as a single
// string (ADR-026 §2.2 — reusing address's own normalize rules rather
// than a separate exact-match path keeps a synonymous spelling like
// "да"/"Да " from being treated as wrong); multiple fields compare as
// sets, each element normalized the same way.
func profileAnswerMatches(field content.IntakeProfileField, actual training.IntakeProfileAnswer, expected content.Intake112ExpectedProfileValue) bool {
	if expected.Unknown {
		return actual.State == "unknown"
	}
	if actual.State != "known" {
		return false
	}
	if field.Kind == "multiple" || expected.Values != nil {
		return stringSetEqual(actual.Values, expected.Values)
	}
	return normalize.Equal(actual.Value, expected.Value)
}

func stringSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	na := make(map[string]bool, len(a))
	for _, v := range a {
		na[normalize.Value(v)] = true
	}
	for _, v := range b {
		if !na[normalize.Value(v)] {
			return false
		}
	}
	return true
}
