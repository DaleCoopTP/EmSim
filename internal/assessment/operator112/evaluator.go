// Package operator112 implements assessment.RuleEvaluator for
// exercise_type "operator112_intake" against operator112/rubric-v2
// (112-6/ADR-026) and, for a judge-enabled lesson, rubric-v3 (ADR-028's
// DESCRIPTION_CONTENT) — the deterministic half (plus one llm criterion)
// of the same RFC-001 §7.4 pipeline internal/assessment/dds already
// implements for dds_processing. It has the same name as
// internal/training/operator112 (a different package, a different
// import path) for the same reason dds/dds does: each is "the
// operator112 rules" for its own module.
package operator112

import (
	"encoding/json"
	"fmt"

	"emsim/internal/assessment"
	"emsim/internal/assessment/operator112/descjudge"
	"emsim/internal/content"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"
)

// Evaluator is the assessment.RuleEvaluator value for
// "operator112_intake". Like dds.Evaluator, it has no fields — every
// rule is a pure function of (evidence, reference, criterion). It also
// implements assessment.SemanticPreparer (description.go's
// PrepareSemantic, ADR-028) — the assertion below keeps that true at
// compile time.
var Evaluator assessment.RuleEvaluator = evaluator{}

var _ assessment.SemanticPreparer = evaluator{}

type evaluator struct{}

// Evaluate decodes raw into internal/training/operator112.EvidenceBody
// (ADR-026's c3 generalization: assessment hands every RuleEvaluator raw
// evidence + the whole scenario content.Body, decoded here rather than
// by the caller). A legacy-route item (intake_state.finale == "" —
// every incoming_call item, and any card_only/full_case item offered
// before ADR-023 shipped) aborts the whole evaluation with
// *assessment.TerminalEvaluationError{Code: "operator112_legacy_route"}
// rather than scoring individual criteria unavailable: rubric-v2's
// blocks are built around the ADR-023 notify snapshot, and there is
// nothing meaningful to compare for an item that never had one.
func (evaluator) Evaluate(raw json.RawMessage, body content.Body, effective assessment.Rubric, semantic assessment.SemanticAnswers) ([]assessment.CriterionResult, error) {
	var ev trainingintake.EvidenceBody
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("assessment/operator112: decode evidence: %w", err)
	}
	if body.Intake112 == nil {
		return nil, fmt.Errorf("assessment/operator112: scenario body has no intake112 section")
	}
	if ev.IntakeState.Finale == "" {
		return nil, &assessment.TerminalEvaluationError{Code: "operator112_legacy_route"}
	}
	snapshot := scoredCard(ev)

	results := make([]assessment.CriterionResult, 0, len(effective.Criteria))
	for _, c := range effective.Criteria {
		results = append(results, evaluateCriterion(ev, body.Intake112, snapshot, c, semantic))
	}
	return results, nil
}

// scoredCard is ADR-026's own "что проверяется" rule (slice-112-6-plan.md
// §2.7): the notified snapshot if "оповестить и сохранить" happened,
// else the last known draft. Scoring the notification snapshot rather
// than the live/final draft matters because the draft can keep changing
// after notify (the item stays open until complete_intake) — the
// notification is the immutable record of what actually reached the
// services, which is what RFC-001 §10 says to compare against.
func scoredCard(ev trainingintake.EvidenceBody) training.IntakeCard {
	if ev.Notification != nil {
		return ev.Notification.CardSnapshot
	}
	return ev.FinalCard
}

// llmCriterionResult dispatches a kind=llm criterion by its own Prompt
// (rubric.schema.json requires prompt+sources, forbids rule, for this
// kind) — the operator112 evaluator's own counterpart to the rule-string
// switch above, one level down. descjudge.PromptVersion
// ("description-questions-v1") is the same string
// Service.sealInputForItem seals into assessment_inputs.judge.
// prompt_versions (via PrepareSemantic's own SemanticRequest), so this
// dispatch and the actual model-calling Handler always agree on which
// prompt version a given SemanticAnswers entry means. An unrecognized
// prompt version (a rubric file this evaluator does not yet know how to
// score) is unavailable, same as an unrecognized rule.
func llmCriterionResult(ev trainingintake.EvidenceBody, intake *content.Intake112, snapshot training.IntakeCard, c assessment.RubricCriterion, semantic assessment.SemanticAnswers) assessment.CriterionResult {
	switch c.Prompt {
	case descjudge.PromptVersion:
		return descriptionContentRule(ev, intake, snapshot, c, semantic)
	default:
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionUnavailable, Weight: c.Weight, Critical: c.Critical,
			Explanation: fmt.Sprintf("неизвестный промпт %q", c.Prompt),
		}
	}
}

// dialogueFactsOf returns the scenario's dialogue facts regardless of
// mode — card_only has none (Dialogue is nil), matching
// penaltyApplicantNameRule's own "no conversation, so nothing was ever
// disclosed" logic.
func dialogueFactsOf(intake *content.Intake112) []content.Intake112Fact {
	if intake.Dialogue == nil {
		return nil
	}
	return intake.Dialogue.Facts
}

// evidenceRefs is every rule's own evidence_refs (openapi.yaml's
// CriterionResult.evidence_refs convention): the notify action if one
// exists, so an instructor can jump straight to the moment the snapshot
// was taken. A stop-before-notify item (scored from final_card) has
// none — its own actions are already in the item's ordinary evidence.
func evidenceRefs(ev trainingintake.EvidenceBody) []string {
	if ev.Notification != nil {
		return []string{"action:" + ev.Notification.ActionID.String()}
	}
	return nil
}

func evaluateCriterion(ev trainingintake.EvidenceBody, intake *content.Intake112, snapshot training.IntakeCard, c assessment.RubricCriterion, semantic assessment.SemanticAnswers) assessment.CriterionResult {
	ref := intake.Reference
	if c.Kind == "llm" {
		return llmCriterionResult(ev, intake, snapshot, c, semantic)
	}
	switch c.Rule {
	case "operator112_address_fields":
		return addressFieldsRule(ev, intake, snapshot, c)
	case "operator112_profile_cards":
		return profileCardsRule(ev, ref, snapshot, c)
	case "operator112_caller_topics":
		return callerTopicsRule(ev, intake, c)
	case "operator112_answer_timing":
		return answerTimingRule(ev, c)
	case "operator112_fill_timing":
		return fillTimingRule(ev, c)
	case "operator112_description_present":
		return descriptionPresentRule(ev, snapshot, c)
	case "operator112_penalty_address_region":
		return penaltyAddressRegionRule(ref, snapshot, c)
	case "operator112_penalty_applicant_name":
		return penaltyApplicantNameRule(ev, intake, snapshot, c)
	case "operator112_penalty_services":
		return penaltyServicesRule(ev, ref, c)
	case "operator112_penalty_extra_profile":
		return penaltyExtraProfileRule(ev, ref, snapshot, c)
	default:
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionUnavailable, Weight: c.Weight, Critical: c.Critical,
			Explanation: fmt.Sprintf("неизвестное правило %q", c.Rule),
		}
	}
}
