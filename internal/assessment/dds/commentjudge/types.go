// Package commentjudge holds the wire types and (from the adapters
// commit onward) the model adapters of dds/rubric-v3's two LLM criteria
// (ДДС-4, ADR-034): D_COMMENT_CONTENT — did the trainee's comments to
// their statuses carry the facts the scenario expects — and G_GRAMMAR —
// how many spelling/grammar/punctuation mistakes do those comments have.
//
// The types live apart from internal/assessment/dds so the rules there
// and the adapters here share one request/answer vocabulary without an
// import cycle, the same split internal/assessment/operator112 and
// descjudge already use (ADR-028).
package commentjudge

// FactsPromptVersion and GrammarPromptVersion identify the two prompt
// layouts — sealed into assessment_inputs.judge.prompt_versions
// [criterion_id] and matched against rubric.dds.v3.json's own `prompt`,
// so a future prompt revision cannot silently change how an already
// sealed input would be read without a version bump. They differ from
// dds/rubric-v1's never-implemented comment-v2/grammar-v1 on purpose:
// only a v3 lesson ever prepares a judge request.
const (
	FactsPromptVersion   = "dds-comment-facts-v1"
	GrammarPromptVersion = "dds-grammar-v1"
)

// Answer is one closed question's own verdict — the model's response
// vocabulary for FactsPromptVersion and the only one the rules decode.
type Answer string

const (
	AnswerYes         Answer = "yes"
	AnswerNo          Answer = "no"
	AnswerNeedsReview Answer = "needs_review"
)

// Valid reports whether a is one of the three closed answers.
func (a Answer) Valid() bool {
	return a == AnswerYes || a == AnswerNo || a == AnswerNeedsReview
}

// Comment is one trainee comment as the judge sees it: everything the
// trainee wrote for one status (comments to the same status are joined),
// the status' name for the "does not contradict its status" question,
// and nothing else — never the crew report, the scenario's reference,
// scores or weights.
type Comment struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
	Text   string `json:"text"`
}

// Question is one closed control question about one comment — built by
// internal/assessment/dds from the scenario reference, never authored
// per scenario (ADR-034).
type Question struct {
	ID        string `json:"id"`
	CommentID string `json:"comment_id"`
	Question  string `json:"question"`
}

// FactsRequest is D_COMMENT_CONTENT's SemanticRequest.Payload — exactly
// what is sealed into assessment_inputs.semantic_input[id] and decoded
// back by the adapter. The answer is a JSON object of exactly the
// request's question ids to Answer values.
type FactsRequest struct {
	Comments  []Comment  `json:"comments"`
	Questions []Question `json:"questions"`
}

// GrammarRequest is G_GRAMMAR's SemanticRequest.Payload. The answer is a
// JSON object of exactly the request's comment ids to lists of
// GrammarError (empty list: no mistakes).
type GrammarRequest struct {
	Comments []Comment `json:"comments"`
}

// GrammarError is one mistake found in a comment. Fragment must be a
// substring of that comment's own text — the adapter rejects an answer
// whose fragment is not, instead of silently dropping it.
type GrammarError struct {
	Fragment   string `json:"fragment"`
	Correction string `json:"correction"`
	Kind       string `json:"kind"`
}

// Grammar error kinds.
const (
	KindSpelling    = "spelling"
	KindGrammar     = "grammar"
	KindPunctuation = "punctuation"
)
