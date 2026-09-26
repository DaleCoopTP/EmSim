package operator112

import (
	"encoding/json"
	"testing"

	"emsim/internal/assessment"
	"emsim/internal/assessment/operator112/descjudge"
	"emsim/internal/content"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"
)

// descriptionRubric is a minimal operator112/rubric-v3-shaped Rubric
// with just DESCRIPTION_CONTENT — the same "keep it local to this test
// file" convention testRubric() uses, so these tests exercise the real
// evaluateCriterion/llmCriterionResult dispatch (Kind=="llm", Prompt
// matching descjudge.PromptVersion) rather than calling
// descriptionContentRule directly.
func descriptionRubric() assessment.Rubric {
	return assessment.Rubric{
		Schema: "emsim/rubric/v1", ID: "test", Version: "operator112/rubric-v3",
		PassThreshold: 70, CriticalCap: 100, ExerciseType: content.ExerciseTypeOperator112Intake,
		Criteria: []assessment.RubricCriterion{
			{ID: "DESCRIPTION_CONTENT", Kind: "llm", Weight: 10, Prompt: descjudge.PromptVersion, Sources: []string{"card_description"}, Params: map[string]any{}},
		},
	}
}

func refWithQuestions(questions ...content.Intake112DescriptionQuestion) content.Intake112Reference {
	ref := fullReference()
	ref.DescriptionQuestions = questions
	return ref
}

func mustEvaluateWithSemantic(t *testing.T, ev trainingintake.EvidenceBody, body content.Body, rubric assessment.Rubric, semantic assessment.SemanticAnswers) []assessment.CriterionResult {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	results, err := Evaluator.Evaluate(raw, body, rubric, semantic)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return results
}

func answersJSON(t *testing.T, answers map[string]descjudge.Answer) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(answers)
	if err != nil {
		t.Fatalf("marshal answers: %v", err)
	}
	return raw
}

func TestDescriptionContentNoQuestionsIsZero(t *testing.T) {
	body := baseBody(fullReference()) // no DescriptionQuestions
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), nil)
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionNotMet || r.Score == nil || *r.Score != 0 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want not_met/0 (no reference)", r)
	}
	if r.Explanation != "эталон не задан" {
		t.Fatalf("unexpected explanation: %q", r.Explanation)
	}
}

func TestDescriptionContentEmptyDescriptionIsZero(t *testing.T) {
	ref := refWithQuestions(content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"})
	body := baseBody(ref)
	card := correctCard()
	card.Complaint = training.IntakeField{} // zero value: state != known
	ev := baseEvidence(testCatalog(), card, true, true)
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), nil)
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionNotMet || r.Score == nil || *r.Score != 0 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want not_met/0 (empty description)", r)
	}
	if len(r.Details) != 1 || r.Details[0].Status != assessment.CriterionNotMet || r.Details[0].MaxPoints != 10 {
		t.Fatalf("unexpected details: %+v", r.Details)
	}
}

func TestDescriptionContentNoJudgeAnswerIsUnavailable(t *testing.T) {
	ref := refWithQuestions(content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"})
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), nil) // no semantic answer at all
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionUnavailable || r.Score != nil {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want unavailable/nil (no judge answer)", r)
	}
}

func TestDescriptionContentAllYesIsMet(t *testing.T) {
	ref := refWithQuestions(
		content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"},
		content.Intake112DescriptionQuestion{ID: "victims", Question: "Указано ли число пострадавших?"},
	)
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	semantic := assessment.SemanticAnswers{"DESCRIPTION_CONTENT": answersJSON(t, map[string]descjudge.Answer{"smell": descjudge.AnswerYes, "victims": descjudge.AnswerYes})}
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), semantic)
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionMet || r.Score == nil || *r.Score != 1 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want met/1", r)
	}
	if len(r.Details) != 2 {
		t.Fatalf("unexpected details: %+v", r.Details)
	}
	for _, d := range r.Details {
		if d.Status != assessment.CriterionMet || d.Points != 5 || d.MaxPoints != 5 {
			t.Fatalf("unexpected detail %+v, want met/5/5 (10 baллов / 2 questions)", d)
		}
	}
}

func TestDescriptionContentPartialSplitsWeightEqually(t *testing.T) {
	ref := refWithQuestions(
		content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"},
		content.Intake112DescriptionQuestion{ID: "victims", Question: "Указано ли число пострадавших?"},
	)
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	semantic := assessment.SemanticAnswers{"DESCRIPTION_CONTENT": answersJSON(t, map[string]descjudge.Answer{"smell": descjudge.AnswerYes, "victims": descjudge.AnswerNo})}
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), semantic)
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionPartial || r.Score == nil || *r.Score != 0.5 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want partial/0.5", r)
	}
}

func TestDescriptionContentAllNoIsNotMet(t *testing.T) {
	ref := refWithQuestions(content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"})
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	semantic := assessment.SemanticAnswers{"DESCRIPTION_CONTENT": answersJSON(t, map[string]descjudge.Answer{"smell": descjudge.AnswerNo})}
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), semantic)
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionNotMet || r.Score == nil || *r.Score != 0 {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want not_met/0", r)
	}
}

// TestDescriptionContentAnyNeedsReviewIsUnavailable is 112-6's own
// decision 2: any single needs_review (even among otherwise-correct
// answers) forces the whole criterion to needs_review, with points
// withheld pending manual review — not a partial credit average.
func TestDescriptionContentAnyNeedsReviewIsUnavailable(t *testing.T) {
	ref := refWithQuestions(
		content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"},
		content.Intake112DescriptionQuestion{ID: "victims", Question: "Указано ли число пострадавших?"},
	)
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	semantic := assessment.SemanticAnswers{"DESCRIPTION_CONTENT": answersJSON(t, map[string]descjudge.Answer{"smell": descjudge.AnswerYes, "victims": descjudge.AnswerNeedsReview})}
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), semantic)
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionUnavailable || r.Score != nil {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want unavailable/nil", r)
	}
	if len(r.Details) != 2 {
		t.Fatalf("unexpected details: %+v", r.Details)
	}
}

func TestDescriptionContentMalformedJudgeAnswerIsUnavailable(t *testing.T) {
	ref := refWithQuestions(content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"})
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	semantic := assessment.SemanticAnswers{"DESCRIPTION_CONTENT": json.RawMessage("not json")}
	results := mustEvaluateWithSemantic(t, ev, body, descriptionRubric(), semantic)
	r := findResult(t, results, "DESCRIPTION_CONTENT")
	if r.Status != assessment.CriterionUnavailable || r.Score != nil {
		t.Fatalf("DESCRIPTION_CONTENT = %+v, want unavailable/nil (malformed answer)", r)
	}
}

// ---------------------------------------------------------------- PrepareSemantic

func mustPrepareSemantic(t *testing.T, ev trainingintake.EvidenceBody, body content.Body, rubric assessment.Rubric) map[string]assessment.SemanticRequest {
	t.Helper()
	preparer, ok := Evaluator.(assessment.SemanticPreparer)
	if !ok {
		t.Fatal("Evaluator does not implement assessment.SemanticPreparer")
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	prepared, err := preparer.PrepareSemantic(raw, body, rubric)
	if err != nil {
		t.Fatalf("PrepareSemantic: %v", err)
	}
	return prepared
}

func TestPrepareSemanticEmptyWhenRubricHasNoDescriptionContent(t *testing.T) {
	ref := refWithQuestions(content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"})
	body := baseBody(ref)
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	prepared := mustPrepareSemantic(t, ev, body, testRubric()) // rubric-v2, no DESCRIPTION_CONTENT
	if len(prepared) != 0 {
		t.Fatalf("prepared = %+v, want empty for a rubric-v2 lesson", prepared)
	}
}

func TestPrepareSemanticEmptyWhenNoQuestions(t *testing.T) {
	body := baseBody(fullReference()) // no DescriptionQuestions
	ev := baseEvidence(testCatalog(), correctCard(), true, true)
	prepared := mustPrepareSemantic(t, ev, body, descriptionRubric())
	if len(prepared) != 0 {
		t.Fatalf("prepared = %+v, want empty when the reference has no questions", prepared)
	}
}

func TestPrepareSemanticEmptyWhenDescriptionUnfilled(t *testing.T) {
	ref := refWithQuestions(content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"})
	body := baseBody(ref)
	card := correctCard()
	card.Complaint = training.IntakeField{}
	ev := baseEvidence(testCatalog(), card, true, true)
	prepared := mustPrepareSemantic(t, ev, body, descriptionRubric())
	if len(prepared) != 0 {
		t.Fatalf("prepared = %+v, want empty when the description is unfilled", prepared)
	}
}

func TestPrepareSemanticBuildsRequestFromReferenceQuestionsAndScoredCard(t *testing.T) {
	ref := refWithQuestions(
		content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"},
		content.Intake112DescriptionQuestion{ID: "victims", Question: "Указано ли число пострадавших?"},
	)
	body := baseBody(ref)
	card := correctCard()
	card.Complaint = knownField("Сильно пахнет газом, пострадавших двое")
	ev := baseEvidence(testCatalog(), card, true, true)
	prepared := mustPrepareSemantic(t, ev, body, descriptionRubric())
	req, ok := prepared["DESCRIPTION_CONTENT"]
	if !ok {
		t.Fatalf("prepared = %+v, want an entry for DESCRIPTION_CONTENT", prepared)
	}
	if req.PromptVersion != descjudge.PromptVersion {
		t.Fatalf("PromptVersion = %q, want %q", req.PromptVersion, descjudge.PromptVersion)
	}
	payload, ok := req.Payload.(descjudge.Request)
	if !ok {
		t.Fatalf("Payload = %+v (%T), want descjudge.Request", req.Payload, req.Payload)
	}
	if payload.Description != "Сильно пахнет газом, пострадавших двое" {
		t.Fatalf("Description = %q", payload.Description)
	}
	if len(payload.Questions) != 2 || payload.Questions[0].ID != "smell" || payload.Questions[1].ID != "victims" {
		t.Fatalf("unexpected questions: %+v", payload.Questions)
	}
}

// TestPrepareSemanticScoresNotifiedSnapshotNotLiveDraft is ADR-026's own
// "что проверяется" rule (scoredCard), exercised for the judge's own
// input too: if the item was notified, PrepareSemantic must build its
// request from the notified snapshot's complaint, never a later draft
// edit — the same rule descriptionContentRule's own Evaluate call
// applies when scoring.
func TestPrepareSemanticScoresNotifiedSnapshotNotLiveDraft(t *testing.T) {
	ref := refWithQuestions(content.Intake112DescriptionQuestion{ID: "smell", Question: "Указано ли, что пахнет газом?"})
	body := baseBody(ref)
	notifiedCard := correctCard()
	notifiedCard.Complaint = knownField("Снимок на момент оповещения")
	ev := baseEvidence(testCatalog(), notifiedCard, true, true)
	ev.FinalCard.Complaint = knownField("Более поздний черновик, после оповещения")
	prepared := mustPrepareSemantic(t, ev, body, descriptionRubric())
	req := prepared["DESCRIPTION_CONTENT"]
	payload := req.Payload.(descjudge.Request)
	if payload.Description != "Снимок на момент оповещения" {
		t.Fatalf("Description = %q, want the notified snapshot's own value", payload.Description)
	}
}
