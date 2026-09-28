package dds

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/assessment/dds/commentjudge"
	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// commentStep is one accepted set_status the trainee made, with the
// comment written together with it ("" for none).
type commentStep struct {
	at      time.Duration
	status  content.Reaction
	comment string
}

// commentCase is one D_COMMENT_CONTENT/G_GRAMMAR fixture over the same
// three-report scenario progress_test.go uses (e1 responding notice, e2
// arrived phone_incoming, e3 working notice), with comment_facts added
// to e1/e3 and, optionally, comment_must_mention on the primary decision.
type commentCase struct {
	mustMention []string
	facts       map[string][]string
	evEvents    []training.EvidenceEvent
	evCalls     []training.EvidenceCall
	steps       []commentStep
	// addComments are add_comment texts, each written right after the
	// step with the same index (the status in effect at that moment).
	addComments map[int]string
}

func (tc commentCase) body() content.Body {
	events := threeReportEvents()
	for i := range events {
		if facts, ok := tc.facts[events[i].Key]; ok {
			exp := *events[i].Expects
			exp.CommentFacts = facts
			events[i].Expects = &exp
		}
	}
	return content.Body{
		ExerciseType: content.ExerciseTypeDDSProcessing,
		Contacts:     []content.Contact{{Key: "crew_leader", Label: "Руководитель бригады", Role: content.ContactRoleCrew}},
		Events:       events,
		Reference: content.Reference{
			PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted, CommentMustMention: tc.mustMention},
			ExpectedChain:   fullExpectedChain(),
		},
	}
}

func (tc commentCase) evidence(t *testing.T) training.EvidenceBody {
	t.Helper()
	ev := baseEvidence()
	ev.Events = tc.evEvents
	ev.Calls = tc.evCalls
	var seq int64
	for i, s := range tc.steps {
		seq++
		payload, err := json.Marshal(map[string]any{"status": s.status, "comment": s.comment})
		if err != nil {
			t.Fatal(err)
		}
		ev.Actions = append(ev.Actions, training.EvidenceAction{
			Seq: seq, Type: training.CommandSetStatus, Accepted: true, ServerAt: t0().Add(s.at), ActionID: uuid.New(), Payload: payload,
		})
		if strings.TrimSpace(s.comment) != "" {
			status := s.status
			ev.Comments = append(ev.Comments, training.EvidenceComment{Seq: seq, Text: s.comment, WithStatus: &status})
		}
		if extra, ok := tc.addComments[i]; ok {
			seq++
			payload, _ := json.Marshal(map[string]any{"text": extra})
			ev.Actions = append(ev.Actions, training.EvidenceAction{
				Seq: seq, Type: training.CommandAddComment, Accepted: true, ServerAt: t0().Add(s.at), ActionID: uuid.New(), Payload: payload,
			})
			ev.Comments = append(ev.Comments, training.EvidenceComment{Seq: seq, Text: extra})
		}
	}
	return ev
}

func rubricV3(t *testing.T) assessment.Rubric {
	t.Helper()
	base, err := assessment.LoadRubric(content.ExerciseTypeDDSProcessing, "dds/rubric-v3")
	if err != nil {
		t.Fatal(err)
	}
	return assessment.Merge(base, nil)
}

// prepared runs PrepareSemantic exactly as sealInputForItem does and
// returns the request payloads keyed by criterion id.
func (tc commentCase) prepared(t *testing.T) map[string]assessment.SemanticRequest {
	t.Helper()
	raw, err := json.Marshal(tc.evidence(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := evaluator{}.PrepareSemantic(raw, tc.body(), rubricV3(t))
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func (tc commentCase) evaluate(t *testing.T, semantic map[string]any) map[string]assessment.CriterionResult {
	t.Helper()
	raw, err := json.Marshal(tc.evidence(t))
	if err != nil {
		t.Fatal(err)
	}
	answers := assessment.SemanticAnswers{}
	for id, v := range semantic {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		answers[id] = b
	}
	if semantic == nil {
		answers = nil
	}
	results, err := Evaluator.Evaluate(raw, tc.body(), rubricV3(t), answers)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]assessment.CriterionResult, len(results))
	for _, r := range results {
		out[r.ID] = r
	}
	return out
}

func allHeard() []training.EvidenceEvent {
	return []training.EvidenceEvent{deliveredNotice("e1", 20*time.Second), deliveredIncoming("e2", 40*time.Second), deliveredNotice("e3", 60*time.Second)}
}

func heardCalls() []training.EvidenceCall {
	return []training.EvidenceCall{answeredIncoming("e2", 41*time.Second)}
}

// goodCase is a full cycle: every reaction has a comment, facts on e1
// (one) and e3 (two).
func goodCase() commentCase {
	return commentCase{
		facts:    map[string][]string{"e1": {"выехала бригада 12"}, "e3": {"вызвана автовышка", "работы идут с 14:00"}},
		evEvents: allHeard(), evCalls: heardCalls(),
		steps: []commentStep{
			{10 * time.Second, content.ReactionAccepted, "Принято в работу"},
			{25 * time.Second, content.ReactionResponding, "Бригада 12 выехала"},
			{45 * time.Second, content.ReactionArrived, ""},
			{65 * time.Second, content.ReactionWorking, "Вызвана автовышка, работы идут с 14:00"},
		},
	}
}

func factsPayload(t *testing.T, prepared map[string]assessment.SemanticRequest) commentjudge.FactsRequest {
	t.Helper()
	req, ok := prepared["D_COMMENT_CONTENT"]
	if !ok {
		t.Fatal("no D_COMMENT_CONTENT request was prepared")
	}
	if req.PromptVersion != commentjudge.FactsPromptVersion {
		t.Fatalf("prompt version = %q, want %q", req.PromptVersion, commentjudge.FactsPromptVersion)
	}
	payload, ok := req.Payload.(commentjudge.FactsRequest)
	if !ok {
		t.Fatalf("payload is %T, want commentjudge.FactsRequest", req.Payload)
	}
	return payload
}

func questionByID(req commentjudge.FactsRequest, id string) (commentjudge.Question, bool) {
	for _, q := range req.Questions {
		if q.ID == id {
			return q, true
		}
	}
	return commentjudge.Question{}, false
}

func commentText(req commentjudge.FactsRequest, id string) string {
	for _, c := range req.Comments {
		if c.ID == id {
			return c.Text
		}
	}
	return ""
}

func yesFor(req commentjudge.FactsRequest) map[string]commentjudge.Answer {
	out := map[string]commentjudge.Answer{}
	for _, q := range req.Questions {
		out[q.ID] = commentjudge.AnswerYes
	}
	return out
}

// TestFactQuestionsAreBoundToTheReactionComment: a comment_facts entry
// is asked about the comment of the status set in reaction to its own
// report, never about another status' comment — the working-status
// facts about the comment "…автовышка…", the responding fact about
// "Бригада 12 выехала".
func TestFactQuestionsAreBoundToTheReactionComment(t *testing.T) {
	req := factsPayload(t, goodCase().prepared(t))
	for id, want := range map[string]string{
		"event:e1:0": "Бригада 12 выехала",
		"event:e3:0": "Вызвана автовышка, работы идут с 14:00",
		"event:e3:1": "Вызвана автовышка, работы идут с 14:00",
	} {
		q, ok := questionByID(req, id)
		if !ok {
			t.Fatalf("question %s missing among %+v", id, req.Questions)
		}
		if got := commentText(req, q.CommentID); got != want {
			t.Fatalf("question %s is bound to %q, want %q", id, got, want)
		}
		if !strings.HasPrefix(q.Question, "Комментарий сообщает: ") {
			t.Fatalf("question %s text = %q", id, q.Question)
		}
	}
	// One "does not contradict the status" question per status comment
	// (accepted, responding, working — arrived has none).
	contradictions := 0
	for _, q := range req.Questions {
		if strings.HasPrefix(q.ID, "status:") {
			contradictions++
		}
	}
	if contradictions != 3 {
		t.Fatalf("contradiction questions = %d, want 3", contradictions)
	}
}

func TestCommentContentAllYesIsMet(t *testing.T) {
	tc := goodCase()
	results := tc.evaluate(t, map[string]any{"D_COMMENT_CONTENT": yesFor(factsPayload(t, tc.prepared(t)))})
	r := results["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionMet || r.Score == nil || *r.Score != 1 {
		t.Fatalf("D_COMMENT_CONTENT = %+v, want met with score 1", r)
	}
	// 3 fact questions + 1 aggregate contradiction question share weight 15.
	var points float64
	for _, d := range r.Details {
		points += d.Points
		if d.MaxPoints != 15.0/4 {
			t.Fatalf("detail %s max_points = %v, want 3.75", d.Key, d.MaxPoints)
		}
	}
	if points != 15 || len(r.Details) != 4 {
		t.Fatalf("details points = %v over %d rows, want 15 over 4", points, len(r.Details))
	}
}

// TestFactInTheWrongCommentScoresPartial: the judge answers per bound
// comment — here the autovышka fact was "found" only in another status'
// comment, which the judge cannot see for that question, so it says no.
func TestCommentContentJudgeNoIsPartial(t *testing.T) {
	tc := goodCase()
	answers := yesFor(factsPayload(t, tc.prepared(t)))
	answers["event:e3:0"] = commentjudge.AnswerNo
	r := tc.evaluate(t, map[string]any{"D_COMMENT_CONTENT": answers})["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionPartial || r.Score == nil || *r.Score != 0.75 {
		t.Fatalf("D_COMMENT_CONTENT = %+v, want partial 0.75", r)
	}
	if got := detailStatus(r, "event:e3:0"); got != assessment.CriterionNotMet {
		t.Fatalf("event:e3:0 status = %q, want not_met", got)
	}
	// The question text is the answer key and lives in Expected only,
	// which StripExpected clears for a trainee; Label stays generic.
	for _, d := range r.Details {
		if strings.Contains(d.Label, "автовышка") {
			t.Fatalf("label %q leaks a reference fact", d.Label)
		}
	}
	stripped := assessment.StripExpected([]assessment.CriterionResult{r})
	for _, d := range stripped[0].Details {
		if d.Expected != nil {
			t.Fatalf("detail %s still has Expected after StripExpected", d.Key)
		}
	}
}

func detailStatus(r assessment.CriterionResult, key string) assessment.CriterionStatus {
	for _, d := range r.Details {
		if d.Key == key {
			return d.Status
		}
	}
	return ""
}

// TestNoReactionIsNoWithoutAskingTheJudge: a heard report whose status
// was never set has no comment — its facts are a plain "no", and no
// question about them ever reaches the model.
func TestNoReactionIsNoWithoutAskingTheJudge(t *testing.T) {
	tc := goodCase()
	tc.steps = tc.steps[:3] // no "working" status at all
	req := factsPayload(t, tc.prepared(t))
	for _, id := range []string{"event:e3:0", "event:e3:1"} {
		if _, ok := questionByID(req, id); ok {
			t.Fatalf("question %s was sent to the judge although there is no comment for it", id)
		}
	}
	r := tc.evaluate(t, map[string]any{"D_COMMENT_CONTENT": yesFor(req)})["D_COMMENT_CONTENT"]
	// e1 fact yes, e3 facts no, contradiction yes: 2 of 4.
	if r.Status != assessment.CriterionPartial || *r.Score != 0.5 {
		t.Fatalf("D_COMMENT_CONTENT = %+v, want partial 0.5", r)
	}
	if got := detailStatus(r, "event:e3:1"); got != assessment.CriterionNotMet {
		t.Fatalf("event:e3:1 status = %q, want not_met", got)
	}
}

// TestUnheardReportsAddNoQuestions: stop before the third report (its
// event skipped) and a never-called crew produce no questions for facts
// the trainee could not have known — C_CALLS/T_PROGRESS already cover them.
func TestUnheardReportsAddNoQuestions(t *testing.T) {
	stopped := goodCase()
	stopped.evEvents = []training.EvidenceEvent{deliveredNotice("e1", 20*time.Second), skippedEvent("e2"), skippedEvent("e3")}
	stopped.evCalls = nil
	stopped.steps = stopped.steps[:2]
	req := factsPayload(t, stopped.prepared(t))
	for _, q := range req.Questions {
		if strings.HasPrefix(q.ID, "event:e3") {
			t.Fatalf("question %s asked for a report skipped by stop", q.ID)
		}
	}
	if _, ok := questionByID(req, "event:e1:0"); !ok {
		t.Fatal("the heard report's fact question is missing")
	}

	neverCalled := goodCase()
	neverCalled.evEvents, neverCalled.evCalls = nil, nil
	neverCalled.steps = neverCalled.steps[:1]
	if _, ok := neverCalled.prepared(t)["D_COMMENT_CONTENT"]; ok {
		t.Fatal("no report was ever heard, yet a fact request was prepared")
	}
	if r := neverCalled.evaluate(t, nil)["D_COMMENT_CONTENT"]; r.Status != assessment.CriterionNotApplicable {
		t.Fatalf("D_COMMENT_CONTENT with no heard reports and no must_mention = %q, want not_applicable", r.Status)
	}
}

// TestMustMentionIsBoundToThePrimaryComment: comment_must_mention items
// become questions about the comment of the first status set.
func TestMustMentionIsBoundToThePrimaryComment(t *testing.T) {
	tc := commentCase{
		mustMention: []string{"не наша территория", "передано в ДДС Северного"},
		steps:       []commentStep{{10 * time.Second, content.ReactionNotAccepted, "Не наша территория, передано в ДДС Северного"}},
	}
	req := factsPayload(t, tc.prepared(t))
	for _, id := range []string{"primary:0", "primary:1"} {
		q, ok := questionByID(req, id)
		if !ok {
			t.Fatalf("question %s missing among %+v", id, req.Questions)
		}
		if got := commentText(req, q.CommentID); got != "Не наша территория, передано в ДДС Северного" {
			t.Fatalf("question %s is bound to %q", id, got)
		}
	}
	r := tc.evaluate(t, map[string]any{"D_COMMENT_CONTENT": yesFor(req)})["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionMet {
		t.Fatalf("D_COMMENT_CONTENT = %+v, want met", r)
	}

	// An empty primary comment: both mentions are "no", nothing to judge.
	empty := tc
	empty.steps = []commentStep{{10 * time.Second, content.ReactionNotAccepted, ""}}
	if _, ok := empty.prepared(t)["D_COMMENT_CONTENT"]; ok {
		t.Fatal("a request was prepared although there is no comment to judge")
	}
	r = empty.evaluate(t, nil)["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionNotMet || r.Score == nil || *r.Score != 0 {
		t.Fatalf("D_COMMENT_CONTENT with an empty primary comment = %+v, want not_met 0", r)
	}
}

func TestAddCommentBelongsToTheStatusInEffect(t *testing.T) {
	tc := goodCase()
	tc.steps[2].comment = "" // arrived has no comment of its own
	tc.addComments = map[int]string{2: "Бригада на месте"}
	req := factsPayload(t, tc.prepared(t))
	var arrived string
	for _, c := range req.Comments {
		if c.Status == "Прибытие" {
			arrived = c.Text
		}
	}
	if arrived != "Бригада на месте" {
		t.Fatalf("arrived comment = %q, want the add_comment text under status Прибытие", arrived)
	}
}

func TestCommentContentNoRequirementsIsNotApplicable(t *testing.T) {
	tc := goodCase()
	tc.facts = nil
	if _, ok := tc.prepared(t)["D_COMMENT_CONTENT"]; ok {
		t.Fatal("a request was prepared for a scenario that asks nothing of comments")
	}
	if r := tc.evaluate(t, nil)["D_COMMENT_CONTENT"]; r.Status != assessment.CriterionNotApplicable {
		t.Fatalf("D_COMMENT_CONTENT = %q, want not_applicable", r.Status)
	}
}

func TestCommentContentNeedsReviewMakesCriterionUnavailable(t *testing.T) {
	tc := goodCase()
	answers := yesFor(factsPayload(t, tc.prepared(t)))
	answers["event:e1:0"] = commentjudge.AnswerNeedsReview
	r := tc.evaluate(t, map[string]any{"D_COMMENT_CONTENT": answers})["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionUnavailable || r.Score != nil {
		t.Fatalf("D_COMMENT_CONTENT = %+v, want unavailable without a score", r)
	}

	delete(answers, "event:e1:0") // a question the judge never answered
	r = tc.evaluate(t, map[string]any{"D_COMMENT_CONTENT": answers})["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionUnavailable {
		t.Fatalf("a missing answer gave %q, want unavailable", r.Status)
	}

	// Sealing (semantic == nil) and "judge not configured" look alike.
	r = tc.evaluate(t, nil)["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionUnavailable {
		t.Fatalf("no semantic answers gave %q, want unavailable", r.Status)
	}
}

func TestContradictionAnswerNoFailsOnlyItsQuestion(t *testing.T) {
	tc := goodCase()
	answers := yesFor(factsPayload(t, tc.prepared(t)))
	answers["status:2"] = commentjudge.AnswerNo // the responding comment contradicts its status
	r := tc.evaluate(t, map[string]any{"D_COMMENT_CONTENT": answers})["D_COMMENT_CONTENT"]
	if r.Status != assessment.CriterionPartial || *r.Score != 0.75 {
		t.Fatalf("D_COMMENT_CONTENT = %+v, want partial 0.75", r)
	}
	if got := detailStatus(r, contradictionID); got != assessment.CriterionNotMet {
		t.Fatalf("contradiction status = %q, want not_met", got)
	}
}

func grammarPayload(t *testing.T, prepared map[string]assessment.SemanticRequest) commentjudge.GrammarRequest {
	t.Helper()
	req, ok := prepared["G_GRAMMAR"]
	if !ok {
		t.Fatal("no G_GRAMMAR request was prepared")
	}
	if req.PromptVersion != commentjudge.GrammarPromptVersion {
		t.Fatalf("prompt version = %q, want %q", req.PromptVersion, commentjudge.GrammarPromptVersion)
	}
	return req.Payload.(commentjudge.GrammarRequest)
}

func grammarAnswers(req commentjudge.GrammarRequest, errors int) map[string][]commentjudge.GrammarError {
	out := map[string][]commentjudge.GrammarError{}
	for _, c := range req.Comments {
		out[c.ID] = []commentjudge.GrammarError{}
	}
	if errors > 0 {
		id := req.Comments[0].ID
		for i := 0; i < errors; i++ {
			out[id] = append(out[id], commentjudge.GrammarError{Fragment: "х", Correction: "у", Kind: commentjudge.KindSpelling})
		}
	}
	return out
}

func TestGrammarThresholds(t *testing.T) {
	tc := goodCase()
	req := grammarPayload(t, tc.prepared(t))
	if len(req.Comments) != 3 {
		t.Fatalf("grammar request has %d comments, want 3 (accepted, responding, working)", len(req.Comments))
	}
	for errs, want := range map[int]assessment.CriterionStatus{0: assessment.CriterionMet, 1: assessment.CriterionPartial, 2: assessment.CriterionPartial, 3: assessment.CriterionNotMet} {
		r := tc.evaluate(t, map[string]any{"G_GRAMMAR": grammarAnswers(req, errs)})["G_GRAMMAR"]
		if r.Status != want {
			t.Fatalf("%d errors: G_GRAMMAR = %q, want %q", errs, r.Status, want)
		}
		if errs == 2 && (r.Score == nil || *r.Score != 0.5) {
			t.Fatalf("2 errors: score = %v, want 0.5", r.Score)
		}
		total := 0
		for _, d := range r.Details {
			total += len(d.Errors)
		}
		if total != errs {
			t.Fatalf("%d errors: details carry %d errors", errs, total)
		}
	}
}

func TestGrammarWithoutCommentsIsNotApplicable(t *testing.T) {
	tc := goodCase()
	for i := range tc.steps {
		tc.steps[i].comment = ""
	}
	if _, ok := tc.prepared(t)["G_GRAMMAR"]; ok {
		t.Fatal("a grammar request was prepared for a card without comments")
	}
	if r := tc.evaluate(t, nil)["G_GRAMMAR"]; r.Status != assessment.CriterionNotApplicable {
		t.Fatalf("G_GRAMMAR = %q, want not_applicable", r.Status)
	}
}

func TestGrammarIncompleteAnswerIsUnavailable(t *testing.T) {
	tc := goodCase()
	req := grammarPayload(t, tc.prepared(t))
	answers := grammarAnswers(req, 0)
	delete(answers, req.Comments[1].ID)
	if r := tc.evaluate(t, map[string]any{"G_GRAMMAR": answers})["G_GRAMMAR"]; r.Status != assessment.CriterionUnavailable {
		t.Fatalf("G_GRAMMAR with an uncovered comment = %q, want unavailable", r.Status)
	}
	if r := tc.evaluate(t, nil)["G_GRAMMAR"]; r.Status != assessment.CriterionUnavailable {
		t.Fatalf("G_GRAMMAR without a judge = %q, want unavailable", r.Status)
	}
}

// TestRubricV1LlmCriteriaStayUnavailable: a lesson frozen on dds/rubric-v1
// keeps its never-implemented comment-v2/grammar-v1 prompts, which are
// not v3's — no request is prepared and the criteria stay unavailable,
// exactly as before ДДС-4.
func TestRubricV1LlmCriteriaStayUnavailable(t *testing.T) {
	tc := goodCase()
	base, err := assessment.LoadRubric(content.ExerciseTypeDDSProcessing, "dds/rubric-v1")
	if err != nil {
		t.Fatal(err)
	}
	effective := assessment.Merge(base, nil)
	raw, err := json.Marshal(tc.evidence(t))
	if err != nil {
		t.Fatal(err)
	}
	body := tc.body()
	body.Reference.PrimaryDecision.CommentMustMention = []string{"причина"}
	prepared, err := evaluator{}.PrepareSemantic(raw, body, effective)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 0 {
		t.Fatalf("a v1 lesson prepared judge requests: %+v", prepared)
	}
	results, err := Evaluator.Evaluate(raw, body, effective, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.ID == "D_COMMENT_CONTENT" && r.Status != assessment.CriterionUnavailable {
			t.Fatalf("v1 D_COMMENT_CONTENT = %q, want unavailable", r.Status)
		}
		if r.ID == "G_GRAMMAR" && r.Status != assessment.CriterionUnavailable {
			t.Fatalf("v1 G_GRAMMAR = %q, want unavailable", r.Status)
		}
	}
}
