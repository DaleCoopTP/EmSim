package operator112

import (
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
