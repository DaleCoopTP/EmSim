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
// (112-5a/ADR-024) — what a CallerReplier answers. Facts and Transcript
// come from training.CallerReplyContext (the worker's own read, taken
// without any lock before calling Reply); Turn identifies which pending
// IntakeCallerTurn this reply is for, so ApplyCallerReply can no-op a
// reply for a turn that is no longer pending by the time it is applied.
// Unlike CallerRequest/CallerTurn above, there is no AskedQuestionIDs or
// QuestionID — a free-text turn has no scripted question to select.
type CallerReplyRequest struct {
	Facts      []content.Intake112Fact
	Transcript []training.IntakeLine
	Turn       int
}

// CallerReply is a CallerReplier's own result — just the applicant's
// reply text. It carries no reveals/reasons the way CallerTurn's
// scripted Answer does, since a free-text turn has no fixed fact list
// to track: 112-6's LLM-based assessment, not this port, will be
// responsible for judging what a free-text conversation actually
// established.
type CallerReply struct {
	Text string
}

// CallerReplier answers one free-text caller-chat turn (112-5a/ADR-024).
// Reply is called entirely outside any command transaction, from a
// worker's caller.reply task handler (ADR-003/ADR-024) — an
// implementation may take real wall-clock time (StubCallerReplier
// deliberately does, so 112-5a exercises the exact asynchronous
// protocol 112-5b's model adapter will use) and must honor ctx
// cancellation (the handler's own lease-bound timeout) rather than
// touching PostgreSQL or blocking indefinitely.
type CallerReplier interface {
	// Adapter names this implementation for IntakeCallerTurn.Adapter —
	// "stub/v1" for StubCallerReplier; 112-5b's model adapter names
	// itself and its model version, so the instructor's review can tell
	// a stub-generated reply from a model's without a separate
	// server-side setting (ADR-024).
	Adapter() string
	Reply(ctx context.Context, req CallerReplyRequest) (CallerReply, error)
}
