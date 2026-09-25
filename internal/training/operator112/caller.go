package operator112

import (
	"context"

	"emsim/internal/content"
	"emsim/internal/training"
)

// CallerRequest and CallerTurn are the content-only boundary of a caller
// simulator. The telephone state is deliberately absent: training rules
// decide whether a turn may be requested before calling this port. An AI
// adapter can later produce the same result outside a command transaction.
type CallerRequest struct {
	Dialogue         content.Intake112Dialogue
	AskedQuestionIDs []string
	Transcript       []training.IntakeLine
	QuestionID       string // empty means the initial caller utterance
}

type CallerTurn struct {
	Question *content.Intake112Question
	Answer   content.Intake112Utterance
}

type CallerSimulator interface {
	Available(CallerRequest) []training.IntakeQuestionOption
	Turn(CallerRequest) (CallerTurn, bool)
}

type preparedCaller struct{}

func (preparedCaller) Available(req CallerRequest) []training.IntakeQuestionOption {
	asked := make(map[string]bool, len(req.AskedQuestionIDs))
	for _, id := range req.AskedQuestionIDs {
		asked[id] = true
	}
	available := make([]training.IntakeQuestionOption, 0, len(req.Dialogue.Questions))
	for _, question := range req.Dialogue.Questions {
		allowed := true
		for _, dependency := range question.AvailableAfter {
			if !asked[dependency] {
				allowed = false
				break
			}
		}
		if allowed {
			available = append(available, training.IntakeQuestionOption{
				ID: question.ID, Text: question.Text, TopicID: question.TopicID, Asked: asked[question.ID],
			})
		}
	}
	return available
}

func (p preparedCaller) Turn(req CallerRequest) (CallerTurn, bool) {
	if req.QuestionID == "" {
		return CallerTurn{Answer: req.Dialogue.Initial}, true
	}
	for _, available := range p.Available(req) {
		if available.ID != req.QuestionID {
			continue
		}
		for i := range req.Dialogue.Questions {
			question := &req.Dialogue.Questions[i]
			if question.ID == req.QuestionID {
				return CallerTurn{Question: question, Answer: question.Answer}, true
			}
		}
	}
	return CallerTurn{}, false
}

// CallerReplyRequest is one pending free-text caller-chat turn's input
// (112-5a/ADR-024, 112-5b/ADR-025) — what a CallerReplier answers. Facts,
// Caller and Transcript come from training.CallerReplyContext (the
// worker's own read, taken without any lock before calling Reply); Turn
// identifies which pending IntakeCallerTurn this reply is for, so
// ApplyCallerReply can no-op a reply for a turn that is no longer
// pending by the time it is applied. Unlike CallerRequest/CallerTurn
// above, there is no AskedQuestionIDs or QuestionID — a free-text turn
// has no scripted question to select.
//
// Caller is nil for a scenario written before 112-5b or one that omits
// the profile on purpose — a CallerReplier must fall back to
// StubCallerReplier's behavior in that case (slice-112-5b-plan.md's
// decision 7), never invent a persona.
type CallerReplyRequest struct {
	Facts      []content.Intake112Fact
	Caller     *content.Intake112CallerProfile
	Transcript []training.IntakeLine
	Turn       int
}

// CallerReply is a CallerReplier's own result. Adapter/Source/Generation
// carry the implementation's own identity and provenance
// (training.IntakeCallerTurn's own fields of the same names — see
// training.CallerReplyOutcome, which the worker builds from this struct);
// unlike CallerTurn's scripted Answer, this is not a closed-book field
// list, so Reveals is computed by the CallerReplier itself (via
// disclosure_patterns, 112-5b) rather than by the caller of Reply.
// 112-6's LLM-based assessment is still responsible for judging what a
// free-text conversation actually established — Reveals only drives
// which facts training/operator112 considers "the applicant has said
// this", not scoring.
type CallerReply struct {
	Text    string
	Adapter string
	Source  string
	Reveals []string
	// Generation is set only when Source is "model" or "fallback" — an
	// opening/scripted/stub reply never calls a model
	// (training.IntakeCallerTurn's own doc comment).
	Generation *training.IntakeCallerGeneration
}

// CallerReplier answers one free-text caller-chat turn (112-5a/ADR-024,
// 112-5b/ADR-025). Reply is called entirely outside any command
// transaction, from a worker's caller.reply task handler (ADR-003/
// ADR-024) — an implementation may take real wall-clock time
// (StubCallerReplier deliberately does, so 112-5a exercises the exact
// asynchronous protocol 112-5b's model adapter uses) and must honor ctx
// cancellation (the handler's own lease-bound timeout) rather than
// touching PostgreSQL or blocking indefinitely. Unlike 112-5a, the
// implementation itself now names each reply's own adapter/source
// through CallerReply rather than a fixed Adapter() method — a single
// aicaller.Replier can answer some turns as "model" and others (no
// caller profile in the scenario) as "stub", both from the same Reply
// call (slice-112-5b-plan.md's decision 7).
type CallerReplier interface {
	Reply(ctx context.Context, req CallerReplyRequest) (CallerReply, error)
}
