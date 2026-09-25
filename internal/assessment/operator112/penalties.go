package operator112

import (
	"sort"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/content/normalize"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"
)

// penaltyResult builds one penalty criterion's CriterionResult —
// PenaltyPoints is always set (never nil) for an ordinary "charged or
// not" outcome; Score.Compute reads it regardless of Status, so Status
// here is purely a human-readable label for the review UI.
func penaltyResult(c assessment.RubricCriterion, points float64, explanation string, status assessment.CriterionStatus) assessment.CriterionResult {
	return assessment.CriterionResult{ID: c.ID, Status: status, Weight: c.Weight, Critical: c.Critical, PenaltyPoints: &points, Explanation: explanation}
}

// notApplicablePenalty is the objective-inapplicability case (ADR-026's
// own carve-out, distinct from "no reference"): the milestone this
// penalty depends on was never reached (e.g. no notify_services at
// all), so there is nothing to judge, not merely nothing to compare
// against. PenaltyPoints stays nil — Score.Compute only sums non-nil
// PenaltyPoints, so this contributes exactly like an ordinary 0 charge
// without claiming "nothing was wrong" was actually verified.
func notApplicablePenalty(c assessment.RubricCriterion, explanation string) assessment.CriterionResult {
	return assessment.CriterionResult{ID: c.ID, Status: assessment.CriterionNotApplicable, Weight: c.Weight, Critical: c.Critical, Explanation: explanation}
}

// penaltyAddressRegionRule is P_ADDRESS_REGION (ADR-026 §2.6, one-time
// penalty regardless of how many of country/region/okrug mismatch —
// interpretation §10.1).
func penaltyAddressRegionRule(ref content.Intake112Reference, snapshot training.IntakeCard, c assessment.RubricCriterion) assessment.CriterionResult {
	points := paramFloat(c.Params, "points", 10)
	noReference := paramString(c.Params, "no_reference_explanation", "эталон не задан")
	if ref.ExpectedCard == nil {
		return penaltyResult(c, 0, noReference, assessment.CriterionMet)
	}
	var anyDefined, mismatch bool
	for _, path := range []string{"country", "region", "okrug"} {
		expected := addressExpectedValue(ref.ExpectedCard.Address, path)
		if expected == "" {
			continue
		}
		anyDefined = true
		actual := knownValue(addressActualField(snapshot.Address, path))
		if !(actual != "" && normalize.Matches(actual, expected, alternativesFor(ref, "address."+path))) {
			mismatch = true
		}
	}
	if !anyDefined {
		return penaltyResult(c, 0, noReference, assessment.CriterionMet)
	}
	if mismatch {
		return penaltyResult(c, points, "страна или субъект не совпадают с эталоном", assessment.CriterionNotMet)
	}
	return penaltyResult(c, 0, "страна и субъект совпадают с эталоном", assessment.CriterionMet)
}

// penaltyApplicantNameRule is P_APPLICANT_NAME (ADR-026 §2.6): charged
// only if the applicant's name was actually disclosed in the
// conversation (interpretation §10.3 — an applicant who never gave a
// name is not a trainee error) and the filled value disagrees with the
// reference. A mismatch that looks like the AI caller's own paraphrase
// (aiDivergent, c6) makes this criterion unavailable instead of
// charging it, the same carve-out ADDRESS_FIELDS applies.
func penaltyApplicantNameRule(ev trainingintake.EvidenceBody, intake *content.Intake112, snapshot training.IntakeCard, c assessment.RubricCriterion) assessment.CriterionResult {
	ref := intake.Reference
	points := paramFloat(c.Params, "points", 5)
	noReference := paramString(c.Params, "no_reference_explanation", "эталон не задан")
	if ref.ExpectedCard == nil || ref.ExpectedCard.ApplicantName == "" {
		return penaltyResult(c, 0, noReference, assessment.CriterionMet)
	}
	if !factRevealed(dialogueFactsOf(intake), ev.IntakeState.Transcript, "/applicant_name") {
		return penaltyResult(c, 0, "заявитель не сообщил ФИО", assessment.CriterionMet)
	}
	actual := knownValue(snapshot.ApplicantName)
	if actual != "" && normalize.Matches(actual, ref.ExpectedCard.ApplicantName, alternativesFor(ref, "applicant_name")) {
		return penaltyResult(c, 0, "ФИО указано верно", assessment.CriterionMet)
	}
	if aiDivergent(ev, intake, actual) {
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionUnavailable, Weight: c.Weight, Critical: c.Critical,
			Explanation: "заявитель мог назвать имя иначе, чем в эталоне — требуется проверка преподавателя",
		}
	}
	return penaltyResult(c, points, "ФИО не совпадает с эталоном", assessment.CriterionNotMet)
}

// factRevealed reports whether any dialogue fact at cardPath was ever
// revealed to the trainee, by checking intake_state.transcript's own
// accumulated IntakeLine.Reveals — the same signal for a prepared
// scripted dialogue (Answer.Reveals) and a free-text one (a caller
// turn's own Reveals, 112-5a/ADR-024), so this needs no mode-specific
// branch. An empty facts list (card_only, no dialogue at all) always
// returns false.
func factRevealed(facts []content.Intake112Fact, transcript []training.IntakeLine, cardPath string) bool {
	revealed := make(map[string]bool)
	for _, line := range transcript {
		for _, r := range line.Reveals {
			revealed[r] = true
		}
	}
	for _, f := range facts {
		if f.CardPath == cardPath && revealed[f.ID] {
			return true
		}
	}
	return false
}

// penaltyServicesRule is P_SERVICES (ADR-026 §2.6: −points_per for each
// extra AND each missing service relative to reference.expected_services
// — interpretation §10.4). It needs the notify snapshot specifically
// (not final_card, which has no service list at all) — an item closed
// before notify_services gets not_applicable, not a penalty: there is
// no service selection to judge yet, distinct from "the reference is
// absent."
func penaltyServicesRule(ev trainingintake.EvidenceBody, ref content.Intake112Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	pointsPer := paramFloat(c.Params, "points_per", 5)
	noReference := paramString(c.Params, "no_reference_explanation", "эталон не задан")
	if len(ref.ExpectedServices) == 0 {
		return penaltyResult(c, 0, noReference, assessment.CriterionMet)
	}
	if ev.Notification == nil {
		return notApplicablePenalty(c, "оповещение служб не выполнено")
	}
	expected := toSet(ref.ExpectedServices)
	actualCodes := make([]string, 0, len(ev.Notification.Services))
	for _, s := range ev.Notification.Services {
		actualCodes = append(actualCodes, s.ServiceCode)
	}
	actual := toSet(actualCodes)

	var details []assessment.CriterionDetail
	for _, code := range sortedKeys(actual) {
		if !expected[code] {
			details = append(details, assessment.CriterionDetail{Key: code, Label: "лишняя служба", Points: pointsPer, MaxPoints: pointsPer, Status: assessment.CriterionNotMet})
		}
	}
	for _, code := range sortedKeys(expected) {
		if !actual[code] {
			details = append(details, assessment.CriterionDetail{Key: code, Label: "пропущенная служба", Points: pointsPer, MaxPoints: pointsPer, Status: assessment.CriterionNotMet})
		}
	}
	points := pointsPer * float64(len(details))
	status, explanation := assessment.CriterionMet, "список служб совпадает с эталоном"
	if len(details) > 0 {
		status, explanation = assessment.CriterionNotMet, "список служб отличается от эталона"
	}
	return assessment.CriterionResult{
		ID: c.ID, Status: status, Weight: c.Weight, Critical: c.Critical, PenaltyPoints: &points,
		EvidenceRefs: evidenceRefs(ev), Details: details, Explanation: explanation,
	}
}

// penaltyExtraProfileRule is P_EXTRA_PROFILE (ADR-026 §2.6: −points_per
// per profile card created beyond reference.expected_types' own
// catalog-resolved set).
func penaltyExtraProfileRule(ev trainingintake.EvidenceBody, ref content.Intake112Reference, snapshot training.IntakeCard, c assessment.RubricCriterion) assessment.CriterionResult {
	pointsPer := paramFloat(c.Params, "points_per", 5)
	noReference := paramString(c.Params, "no_reference_explanation", "эталон не задан")
	if len(ref.ExpectedTypes) == 0 {
		return penaltyResult(c, 0, noReference, assessment.CriterionMet)
	}
	expected := toSet(expectedProfileIDs(ev.IntakeState.Catalog, ref.ExpectedTypes))
	var extra []string
	for id := range snapshot.Profiles {
		if !expected[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	details := make([]assessment.CriterionDetail, 0, len(extra))
	for _, id := range extra {
		details = append(details, assessment.CriterionDetail{Key: id, Label: profileNameByID(ev.IntakeState.Catalog, id), Points: pointsPer, MaxPoints: pointsPer, Status: assessment.CriterionNotMet})
	}
	points := pointsPer * float64(len(extra))
	status, explanation := assessment.CriterionMet, "лишних карт не создано"
	if len(extra) > 0 {
		status, explanation = assessment.CriterionNotMet, "созданы карты, не входящие в эталон"
	}
	return assessment.CriterionResult{
		ID: c.ID, Status: status, Weight: c.Weight, Critical: c.Critical, PenaltyPoints: &points,
		EvidenceRefs: evidenceRefs(ev), Details: details, Explanation: explanation,
	}
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
