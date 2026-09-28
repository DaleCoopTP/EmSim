package dds

import (
	"encoding/json"
	"fmt"
	"strings"

	"emsim/internal/assessment"
	"emsim/internal/assessment/dds/commentjudge"
	"emsim/internal/content"
	"emsim/internal/training"
)

// ДДС-4/ADR-034: D_COMMENT_CONTENT and G_GRAMMAR (dds/rubric-v3). The
// scenario carries no judge questions of its own — they are built here
// from the reference the scenario already has (reference.primary_
// decision.comment_must_mention, events[].expects.comment_facts), each
// tied to the comment of one status change.

// reactionTitles are the DDS guide's own status names («Памятка для
// ДДС», стр. 21–23) — what the judge sees in a "does not contradict its
// status" question and what the review screen shows; the same table
// web/src/labels.ts keeps for the UI.
var reactionTitles = map[content.Reaction]string{
	content.ReactionAccepted:             "Принята",
	content.ReactionNotAccepted:          "Не принята",
	content.ReactionResponding:           "Начало реагирования",
	content.ReactionArrived:              "Прибытие",
	content.ReactionWorking:              "Проведение работ",
	content.ReactionCompleted:            "Работы завершены",
	content.ReactionRefused:              "Отказ от выполнения работ",
	content.ReactionCompletedWithoutTeam: "Завершение работ без бригады",
}

func reactionTitle(r content.Reaction) string {
	if title, ok := reactionTitles[r]; ok {
		return title
	}
	return string(r)
}

// commentEntry is everything the trainee wrote for one status change —
// its own comment plus any add_comment written while that status was the
// current one, joined in input order. The status change is the unit both
// criteria judge (ADR-030: a comment is part of its status). A comment
// journalled before any status was set has status "" and only feeds
// G_GRAMMAR.
type commentEntry struct {
	id     string
	seq    int64 // the owning set_status action's seq; the first comment's own seq for a pre-status entry
	status content.Reaction
	text   string
	ref    string // action:<uuid> of the owning action, "" when unknown
}

// commentEntries groups ev.Comments by owning status change, in order.
func commentEntries(ev training.EvidenceBody) []commentEntry {
	type statusAt struct {
		seq    int64
		status content.Reaction
	}
	var setStatuses []statusAt
	refs := make(map[int64]string, len(ev.Actions))
	for _, a := range ev.Actions {
		refs[a.Seq] = "action:" + a.ActionID.String()
		if !a.Accepted || a.Type != training.CommandSetStatus {
			continue
		}
		var payload struct {
			Status content.Reaction `json:"status"`
		}
		if err := json.Unmarshal(a.Payload, &payload); err != nil {
			continue
		}
		setStatuses = append(setStatuses, statusAt{seq: a.Seq, status: payload.Status})
	}
	var entries []commentEntry
	index := make(map[int64]int)
	for _, c := range ev.Comments {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		owner := statusAt{seq: c.Seq}
		if c.WithStatus != nil {
			owner.status = *c.WithStatus
		} else {
			for _, s := range setStatuses {
				if s.seq >= c.Seq {
					break
				}
				owner = s
			}
			if owner.status == "" {
				owner.seq = c.Seq
			}
		}
		i, ok := index[owner.seq]
		if !ok {
			i = len(entries)
			index[owner.seq] = i
			entries = append(entries, commentEntry{
				id: fmt.Sprintf("comment:%d", owner.seq), seq: owner.seq, status: owner.status, ref: refs[owner.seq],
			})
		}
		if entries[i].text != "" {
			entries[i].text += "\n"
		}
		entries[i].text += strings.TrimSpace(c.Text)
	}
	return entries
}

func entryByStatusSeq(entries []commentEntry, seq int64) *commentEntry {
	for i := range entries {
		if entries[i].seq == seq && entries[i].status != "" {
			return &entries[i]
		}
	}
	return nil
}

// firstStatusSeq returns the seq of the first accepted set_status action
// whose status is want ("" matches any) and whether one exists.
func firstStatusSeq(ev training.EvidenceBody, want content.Reaction) (int64, bool) {
	for _, a := range ev.Actions {
		if !a.Accepted || a.Type != training.CommandSetStatus {
			continue
		}
		var payload struct {
			Status content.Reaction `json:"status"`
		}
		if err := json.Unmarshal(a.Payload, &payload); err != nil {
			continue
		}
		if want == "" || payload.Status == want {
			return a.Seq, true
		}
	}
	return 0, false
}

// factQuestion is one reference fact the comment to one status should
// carry: id is primary:<n> (comment_must_mention) or event:<key>:<n>
// (comment_facts). comment is nil when the trainee wrote no comment for
// that status — the answer is then a plain "no" with no judge call.
type factQuestion struct {
	id      string
	status  content.Reaction // the status whose comment must carry the fact
	fact    string
	comment *commentEntry
}

func (q factQuestion) text() string { return "Комментарий сообщает: " + q.fact }

// commentQuestions builds D_COMMENT_CONTENT's fact questions from the
// scenario reference and the item's own evidence:
//
//   - every comment_must_mention entry becomes a question about the
//     comment of the first status set (the primary decision);
//   - every comment_facts entry of a crew report the trainee actually
//     heard becomes a question about the comment of the first status
//     equal to that report's expects.status.
//
// A report that was never heard — the item closed before it (stop), an
// unanswered incoming call, or the crew was never called — yields no
// question: the trainee could not have known its facts, and the missed
// call or reaction is already scored by C_CALLS/T_PROGRESS. A heard
// report whose status was never set (or set without a comment) does
// yield a question, answered "no" — "did not react" and "did not pass
// the facts on" are different violations, scored by different criteria.
func commentQuestions(ev training.EvidenceBody, body content.Body, entries []commentEntry) []factQuestion {
	var out []factQuestion
	primary := body.Reference.PrimaryDecision
	if len(primary.CommentMustMention) > 0 {
		var entry *commentEntry
		var status content.Reaction
		if seq, ok := firstStatusSeq(ev, ""); ok {
			entry = entryByStatusSeq(entries, seq)
			status = statusOfSeq(ev, seq)
		}
		if status == "" {
			status = primary.Status
		}
		for n, fact := range primary.CommentMustMention {
			out = append(out, factQuestion{id: fmt.Sprintf("primary:%d", n), status: status, fact: fact, comment: entry})
		}
	}
	for _, r := range crewReports(ev, body) {
		if len(r.expects.CommentFacts) == 0 || r.heardAt == nil {
			continue
		}
		var entry *commentEntry
		if seq, ok := firstStatusSeq(ev, r.expects.Status); ok {
			entry = entryByStatusSeq(entries, seq)
		}
		for n, fact := range r.expects.CommentFacts {
			out = append(out, factQuestion{id: fmt.Sprintf("event:%s:%d", r.key, n), status: r.expects.Status, fact: fact, comment: entry})
		}
	}
	return out
}

func statusOfSeq(ev training.EvidenceBody, seq int64) content.Reaction {
	for _, a := range ev.Actions {
		if a.Seq != seq || !a.Accepted || a.Type != training.CommandSetStatus {
			continue
		}
		var payload struct {
			Status content.Reaction `json:"status"`
		}
		if err := json.Unmarshal(a.Payload, &payload); err == nil {
			return payload.Status
		}
	}
	return ""
}

// contradictionID is the aggregate "no comment contradicts its status"
// question's own detail key — one question in the criterion's share
// however many comments there are, so a card full of harmless comments
// cannot dilute the fact questions (ADR-034).
const contradictionID = "contradiction"

func contradictionQuestionID(e commentEntry) string { return fmt.Sprintf("status:%d", e.seq) }

func judgeComments(entries []commentEntry) []commentjudge.Comment {
	out := make([]commentjudge.Comment, 0, len(entries))
	for _, e := range entries {
		c := commentjudge.Comment{ID: e.id, Text: e.text}
		if e.status != "" {
			c.Status = reactionTitle(e.status)
		}
		out = append(out, c)
	}
	return out
}

// factsRequest builds D_COMMENT_CONTENT's judge request: every fact
// question that has a comment, plus one "does not contradict the status"
// question per status comment — but only when there is at least one fact
// question at all (the criterion is not applicable otherwise). ok is
// false when there is nothing for the judge to answer.
func factsRequest(entries []commentEntry, facts []factQuestion) (commentjudge.FactsRequest, bool) {
	if len(facts) == 0 {
		return commentjudge.FactsRequest{}, false
	}
	var req commentjudge.FactsRequest
	used := make(map[string]bool)
	for _, q := range facts {
		if q.comment == nil {
			continue
		}
		used[q.comment.id] = true
		req.Questions = append(req.Questions, commentjudge.Question{ID: q.id, CommentID: q.comment.id, Question: q.text()})
	}
	for _, e := range entries {
		if e.status == "" {
			continue
		}
		used[e.id] = true
		req.Questions = append(req.Questions, commentjudge.Question{
			ID: contradictionQuestionID(e), CommentID: e.id,
			Question: fmt.Sprintf("Комментарий не противоречит статусу «%s»", reactionTitle(e.status)),
		})
	}
	if len(req.Questions) == 0 {
		return commentjudge.FactsRequest{}, false
	}
	for _, c := range judgeComments(entries) {
		if used[c.ID] {
			req.Comments = append(req.Comments, c)
		}
	}
	return req, true
}

// grammarRequest builds G_GRAMMAR's judge request: every non-empty
// comment. ok is false when the trainee wrote none.
func grammarRequest(entries []commentEntry) (commentjudge.GrammarRequest, bool) {
	if len(entries) == 0 {
		return commentjudge.GrammarRequest{}, false
	}
	return commentjudge.GrammarRequest{Comments: judgeComments(entries)}, true
}

var _ assessment.SemanticPreparer = evaluator{}

// PrepareSemantic implements assessment.SemanticPreparer (ADR-028,
// ADR-034): a request for each dds/rubric-v3 llm criterion in effective
// that has something for the judge to answer. Criteria of a v1 lesson
// (comment-v2/grammar-v1 prompts, never implemented) and criteria with
// nothing to ask — no facts, no comments, or every fact already a
// deterministic "no" — get no request and are scored by Evaluate alone.
func (evaluator) PrepareSemantic(raw json.RawMessage, body content.Body, effective assessment.Rubric) (map[string]assessment.SemanticRequest, error) {
	prepared := map[string]assessment.SemanticRequest{}
	var ev training.EvidenceBody
	decoded := false
	for _, c := range effective.Criteria {
		if c.Kind != "llm" || (c.Prompt != commentjudge.FactsPromptVersion && c.Prompt != commentjudge.GrammarPromptVersion) {
			continue
		}
		if !decoded {
			if err := json.Unmarshal(raw, &ev); err != nil {
				return nil, fmt.Errorf("assessment/dds: decode evidence for semantic prepare: %w", err)
			}
			decoded = true
		}
		entries := commentEntries(ev)
		switch c.Prompt {
		case commentjudge.FactsPromptVersion:
			if req, ok := factsRequest(entries, commentQuestions(ev, body, entries)); ok {
				prepared[c.ID] = assessment.SemanticRequest{PromptVersion: c.Prompt, Payload: req}
			}
		case commentjudge.GrammarPromptVersion:
			if req, ok := grammarRequest(entries); ok {
				prepared[c.ID] = assessment.SemanticRequest{PromptVersion: c.Prompt, Payload: req}
			}
		}
	}
	return prepared, nil
}

func paramString(params map[string]any, key, fallback string) string {
	if s, ok := params[key].(string); ok && s != "" {
		return s
	}
	return fallback
}

func noJudge(c assessment.RubricCriterion) assessment.CriterionResult {
	return unavailable(c, paramString(c.Params, "no_judge_explanation", "судья не настроен"))
}

func statusFromShare(score float64) assessment.CriterionStatus {
	switch {
	case score >= 1:
		return assessment.CriterionMet
	case score <= 0:
		return assessment.CriterionNotMet
	default:
		return assessment.CriterionPartial
	}
}

func textPtr(s string) *string { return &s }

// commentContentRule is D_COMMENT_CONTENT (dds/rubric-v3, ADR-034): the
// criterion's weight is split equally among the fact questions plus one
// aggregate "no comment contradicts its status" question (present only
// when the trainee wrote a status comment). No fact questions at all —
// the scenario asks nothing of the comments, or every crew report went
// unheard — is not_applicable. Any needs_review, or a question the judge
// never answered, makes the whole criterion unavailable and so the whole
// assessment needs_review, never a zero (ADR-028 §2).
func commentContentRule(ev training.EvidenceBody, body content.Body, c assessment.RubricCriterion, semantic assessment.SemanticAnswers) assessment.CriterionResult {
	entries := commentEntries(ev)
	facts := commentQuestions(ev, body, entries)
	if len(facts) == 0 {
		return na(c)
	}
	var answers map[string]commentjudge.Answer
	_, judged := factsRequest(entries, facts)
	if judged {
		raw, ok := semantic[c.ID]
		if !ok {
			return noJudge(c)
		}
		if err := json.Unmarshal(raw, &answers); err != nil {
			return unavailable(c, "ошибка формата ответа судьи")
		}
	}
	hasContradiction := false
	for _, e := range entries {
		if e.status != "" {
			hasContradiction = true
		}
	}
	total := len(facts)
	if hasContradiction {
		total++
	}
	share := c.Weight / float64(total)

	var details []assessment.CriterionDetail
	var refs []string
	yes, anyReview := 0, false
	for _, q := range facts {
		detail := assessment.CriterionDetail{
			Key: q.id, Label: "Комментарий к статусу «" + reactionTitle(q.status) + "»",
			MaxPoints: share, Expected: textPtr(q.text()), Status: assessment.CriterionNotMet,
		}
		if q.comment != nil {
			detail.Actual = textPtr(q.comment.text)
			if q.comment.ref != "" {
				refs = append(refs, q.comment.ref)
			}
			switch answers[q.id] {
			case commentjudge.AnswerYes:
				detail.Status, detail.Points = assessment.CriterionMet, share
				yes++
			case commentjudge.AnswerNo:
			default: // needs_review, or the judge did not answer this question
				detail.Status = assessment.CriterionUnavailable
				anyReview = true
			}
		}
		details = append(details, detail)
	}
	if hasContradiction {
		detail := assessment.CriterionDetail{
			Key: contradictionID, Label: "Комментарии не противоречат своим статусам",
			MaxPoints: share, Status: assessment.CriterionMet, Points: share,
		}
		contradicts, review := false, false
		for _, e := range entries {
			if e.status == "" {
				continue
			}
			switch answers[contradictionQuestionID(e)] {
			case commentjudge.AnswerYes:
			case commentjudge.AnswerNo:
				contradicts = true
			default:
				review = true
			}
		}
		switch {
		case contradicts:
			detail.Status, detail.Points = assessment.CriterionNotMet, 0
		case review:
			detail.Status, detail.Points = assessment.CriterionUnavailable, 0
			anyReview = true
		default:
			yes++
		}
		details = append(details, detail)
	}
	if anyReview {
		r := result(c, assessment.CriterionUnavailable, c.Critical, "судья не уверен в ответе на один или несколько вопросов — требуется ручная проверка", refs)
		r.Details = details
		return r
	}
	score := float64(yes) / float64(total)
	r := result(c, statusFromShare(score), c.Critical, fmt.Sprintf("%d из %d проверок комментариев пройдены", yes, total), refs)
	r.Score = &score
	r.Details = details
	return r
}

// grammarRule is G_GRAMMAR (dds/rubric-v3, ADR-034): the number of
// mistakes the judge found across all the trainee's comments — none is
// met, up to params.partial_max_errors (default 2) is half credit, more
// is not met. No comments at all is not_applicable; a judge answer that
// does not cover every comment is unavailable.
func grammarRule(ev training.EvidenceBody, c assessment.RubricCriterion, semantic assessment.SemanticAnswers) assessment.CriterionResult {
	entries := commentEntries(ev)
	if len(entries) == 0 {
		return na(c)
	}
	raw, ok := semantic[c.ID]
	if !ok {
		return noJudge(c)
	}
	var answers map[string][]commentjudge.GrammarError
	if err := json.Unmarshal(raw, &answers); err != nil {
		return unavailable(c, "ошибка формата ответа судьи")
	}
	var details []assessment.CriterionDetail
	var refs []string
	total := 0
	for _, e := range entries {
		errs, ok := answers[e.id]
		if !ok {
			return unavailable(c, "судья не проверил один из комментариев")
		}
		if e.ref != "" {
			refs = append(refs, e.ref)
		}
		label := "Комментарий"
		if e.status != "" {
			label = "Комментарий к статусу «" + reactionTitle(e.status) + "»"
		}
		detail := assessment.CriterionDetail{Key: e.id, Label: label, Actual: textPtr(e.text), Status: assessment.CriterionMet}
		for _, ge := range errs {
			detail.Errors = append(detail.Errors, assessment.GrammarError{Fragment: ge.Fragment, Correction: ge.Correction, Kind: ge.Kind})
		}
		if len(errs) > 0 {
			detail.Status = assessment.CriterionNotMet
		}
		total += len(errs)
		details = append(details, detail)
	}
	limit := paramInt(c.Params, "partial_max_errors", 2)
	var r assessment.CriterionResult
	switch {
	case total == 0:
		r = met(c, "ошибок в комментариях не найдено", refs...)
	case total <= limit:
		r = partial(c, 0.5, fmt.Sprintf("ошибок в комментариях: %d", total), refs...)
	default:
		r = notMet(c, fmt.Sprintf("ошибок в комментариях: %d", total), refs...)
	}
	r.Details = details
	return r
}
