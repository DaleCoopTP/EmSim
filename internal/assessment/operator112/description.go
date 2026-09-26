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

// descriptionPresentRule is DESCRIPTION_PRESENT (ADR-026 §2.5, rubric-v2
// only): binary presence of the caller's own complaint, in their own
// words — no reference needed. operator112/rubric-v3 (ADR-028) replaces
// this criterion with DESCRIPTION_CONTENT (descriptionContentRule,
// below); this function itself is unchanged and keeps scoring any
// lesson still frozen on rubric-v2 exactly as before.
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

// descriptionContentCriterionID is rubric.operator112.v3.json's own
// DESCRIPTION_CONTENT id — PrepareSemantic below looks it up by this
// exact id (effective.ByID) rather than assuming it is the only
// llm-kind criterion, matching addressFieldsRule/etc.'s own convention
// of never assuming rubric shape beyond what params/kind actually say.
const descriptionContentCriterionID = "DESCRIPTION_CONTENT"

// descriptionContentRule is DESCRIPTION_CONTENT (ADR-028, operator112/
// rubric-v3): the LLM judge's own yes/no/needs_review verdicts, one per
// intake112.reference.description_questions entry, scored against equal
// shares of the criterion's weight (112-6's decision 1: "10 баллов
// делятся поровну"). No questions in the reference, or an unfilled
// complaint, are the same ADR-026 "эталон отсутствует" zero every other
// block already gives for a missing reference — PrepareSemantic never
// even prepares a request for either case, so a judge call never
// happens for them. Once a request was prepared, semantic[c.ID]'s own
// absence here can only mean "no judge configured" or "the judge has
// not answered yet" (Service.Handle's own two call sites into Evaluate)
// — both get the same explanation, since neither is the trainee's fault
// and both resolve to needs_review, never a zero (112-6's decision 2).
func descriptionContentRule(ev trainingintake.EvidenceBody, intake *content.Intake112, snapshot training.IntakeCard, c assessment.RubricCriterion, semantic assessment.SemanticAnswers) assessment.CriterionResult {
	questions := intake.Reference.DescriptionQuestions
	if len(questions) == 0 {
		zero := 0.0
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionNotMet, Score: &zero, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Explanation: paramString(c.Params, "no_reference_explanation", "эталон не задан"),
		}
	}
	share := c.Weight / float64(len(questions))
	description := knownValue(snapshot.Complaint)
	if description == "" {
		zero := 0.0
		details := make([]assessment.CriterionDetail, 0, len(questions))
		for _, q := range questions {
			details = append(details, assessment.CriterionDetail{Key: q.ID, Label: q.Question, MaxPoints: share, Status: assessment.CriterionNotMet})
		}
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionNotMet, Score: &zero, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Details: details,
			Explanation: paramString(c.Params, "empty_description_explanation", "описание со слов заявителя не заполнено"),
		}
	}
	raw, ok := semantic[c.ID]
	if !ok {
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionUnavailable, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Explanation: paramString(c.Params, "no_judge_explanation", "судья не настроен"),
		}
	}
	var answers map[string]descjudge.Answer
	if err := json.Unmarshal(raw, &answers); err != nil {
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionUnavailable, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Explanation: "ошибка формата ответа судьи",
		}
	}
	details := make([]assessment.CriterionDetail, 0, len(questions))
	var yesCount int
	var anyReview bool
	for _, q := range questions {
		answer, ok := answers[q.ID]
		status := assessment.CriterionNotMet
		switch {
		case !ok, answer == descjudge.AnswerNeedsReview:
			status = assessment.CriterionUnavailable
			anyReview = true
		case answer == descjudge.AnswerYes:
			status = assessment.CriterionMet
			yesCount++
		case answer == descjudge.AnswerNo:
			status = assessment.CriterionNotMet
		default:
			status = assessment.CriterionUnavailable
			anyReview = true
		}
		points := 0.0
		if status == assessment.CriterionMet {
			points = share
		}
		details = append(details, assessment.CriterionDetail{Key: q.ID, Label: q.Question, Points: points, MaxPoints: share, Status: status})
	}
	if anyReview {
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionUnavailable, Weight: c.Weight, Critical: c.Critical,
			EvidenceRefs: evidenceRefs(ev), Details: details,
			Explanation: "судья не уверен в ответе на один или несколько вопросов — требуется ручная проверка",
		}
	}
	score := float64(yesCount) / float64(len(questions))
	return assessment.CriterionResult{
		ID: c.ID, Status: statusFromScore(score), Score: &score, Weight: c.Weight, Critical: c.Critical,
		EvidenceRefs: evidenceRefs(ev), Details: details,
		Explanation: fmt.Sprintf("%d из %d вопросов получили «да»", yesCount, len(questions)),
	}
}

// PrepareSemantic implements assessment.SemanticPreparer (ADR-028) —
// called by sealInputForItem only when a judge is configured at all
// (Service.judge != nil). It returns an empty, non-nil map (not an
// error) whenever there is nothing for a judge to answer: no
// DESCRIPTION_CONTENT criterion in effective at all (a rubric-v2 lesson
// — never happens today since lessons.rubric_version freezes v2 unless
// the judge was on at creation, but this stays robust to that anyway),
// no description_questions in the reference, or an unfilled complaint —
// descriptionContentRule's own "эталон отсутствует"/empty-description
// zero applies to all three without ever calling a model.
func (evaluator) PrepareSemantic(raw json.RawMessage, body content.Body, effective assessment.Rubric) (map[string]assessment.SemanticRequest, error) {
	c, ok := effective.ByID(descriptionContentCriterionID)
	if !ok {
		return map[string]assessment.SemanticRequest{}, nil
	}
	if body.Intake112 == nil {
		return nil, fmt.Errorf("assessment/operator112: scenario body has no intake112 section")
	}
	questions := body.Intake112.Reference.DescriptionQuestions
	if len(questions) == 0 {
		return map[string]assessment.SemanticRequest{}, nil
	}
	var ev trainingintake.EvidenceBody
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("assessment/operator112: decode evidence for semantic prepare: %w", err)
	}
	description := knownValue(scoredCard(ev).Complaint)
	if description == "" {
		return map[string]assessment.SemanticRequest{}, nil
	}
	judgeQuestions := make([]descjudge.Question, len(questions))
	for i, q := range questions {
		judgeQuestions[i] = descjudge.Question{ID: q.ID, Question: q.Question}
	}
	return map[string]assessment.SemanticRequest{
		c.ID: {PromptVersion: descjudge.PromptVersion, Payload: descjudge.Request{Description: description, Questions: judgeQuestions}},
	}, nil
}
