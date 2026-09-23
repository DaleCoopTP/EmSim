package operator112

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"emsim/internal/training"
)

type exercise struct{ caller CallerSimulator }

func New() training.Exercise { return exercise{caller: preparedCaller{}} }

// AvailableQuestions is an optional exercise read projection; the
// application service loads the immutable scenario before calling it.
func (e exercise) AvailableQuestions(item training.Item) []training.IntakeQuestionOption {
	if item.IntakeDialogue == nil || item.IntakeState == nil || item.IntakeState.CallStatus != "connected" ||
		item.State == training.ItemClosed || item.State == training.ItemInterrupted {
		return []training.IntakeQuestionOption{}
	}
	return e.caller.Available(callerRequest(item, ""))
}

func callerRequest(item training.Item, questionID string) CallerRequest {
	return CallerRequest{Dialogue: *item.IntakeDialogue, AskedQuestionIDs: item.IntakeState.AskedQuestionIDs,
		Transcript: item.IntakeState.Transcript, QuestionID: questionID}
}

func appendLine(state *training.IntakeState, item training.Item, cmd training.Command, now time.Time,
	sourceID, speaker, text, questionID, topicID string, reveals []string) {
	state.Transcript = append(state.Transcript, training.IntakeLine{
		ID:       cmd.CommandID.String() + ":" + strconv.Itoa(len(state.Transcript)+1),
		SourceID: sourceID, Speaker: speaker, CallID: item.ID.String(), CommandID: cmd.CommandID.String(),
		QuestionID: questionID, TopicID: topicID, Reveals: append([]string(nil), reveals...),
		Text: text, ServerAt: now,
	})
}

func payload(raw json.RawMessage, dst any) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}

func reject(item training.Item, why training.Rejection) training.Decision {
	return training.Decision{Rejection: why, State: item.State, Reaction: item.Reaction,
		Card: item.Card, IntakeCard: item.IntakeCard, IntakeState: item.IntakeState}
}

func (e exercise) Decide(item training.Item, cmd training.Command, now time.Time) (training.Decision, error) {
	if item.IntakeCard == nil || item.IntakeState == nil {
		return reject(item, training.RejectTransitionNotAllowed), nil
	}
	card, state := *item.IntakeCard, *item.IntakeState
	state.Transcript = append([]training.IntakeLine(nil), item.IntakeState.Transcript...)
	state.AskedQuestionIDs = append([]string(nil), item.IntakeState.AskedQuestionIDs...)
	d := training.Decision{Accepted: true, State: item.State, Reaction: item.Reaction,
		Card: item.Card, IntakeCard: &card, IntakeState: &state}
	switch cmd.Type {
	case training.CommandOpen:
		if item.State != training.ItemOffered {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		d.State, d.OpenedAt = training.ItemOpened, &now
	case training.CommandAnswerIncoming:
		if item.State != training.ItemOpened || state.CallStatus != "ringing" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.AnsweredAt = "connected", &now
		state.Transcript = []training.IntakeLine{}
		if item.IntakeDialogue != nil {
			turn, ok := e.caller.Turn(callerRequest(item, ""))
			if !ok {
				return reject(item, training.RejectTransitionNotAllowed), nil
			}
			appendLine(&state, item, cmd, now, turn.Answer.ID, "caller", turn.Answer.Text, "", "", turn.Answer.Reveals)
		} else {
			for i, line := range item.IntakeScript {
				appendLine(&state, item, cmd, now, "legacy_"+strconv.Itoa(i+1), "caller", line, "", "", nil)
			}
		}
		d.State = training.ItemInProgress
	case training.CommandAskIntakeQuestion:
		if state.CallStatus != "connected" || item.IntakeDialogue == nil {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			QuestionID string `json:"question_id"`
		}
		if !payload(cmd.Payload, &p) || p.QuestionID == "" {
			return reject(item, training.RejectInvalidPayload), nil
		}
		turn, ok := e.caller.Turn(callerRequest(item, p.QuestionID))
		if !ok || turn.Question == nil {
			return reject(item, training.RejectInvalidPayload), nil
		}
		question := turn.Question
		appendLine(&state, item, cmd, now, question.ID, "operator", question.Text, question.ID, question.TopicID, nil)
		appendLine(&state, item, cmd, now, turn.Answer.ID, "caller", turn.Answer.Text, question.ID, question.TopicID, turn.Answer.Reveals)
		asked := false
		for _, id := range state.AskedQuestionIDs {
			asked = asked || id == question.ID
		}
		if !asked {
			state.AskedQuestionIDs = append(state.AskedQuestionIDs, question.ID)
		}
	case training.CommandHoldIncoming:
		if state.CallStatus != "connected" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus = "held"
	case training.CommandResumeIncoming:
		if state.CallStatus != "held" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus = "connected"
	case training.CommandEndIncoming:
		if state.CallStatus != "connected" && state.CallStatus != "held" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.EndedAt = "ended", &now
	case training.CommandMarkNoContact:
		if item.State != training.ItemOpened || state.CallStatus != "ringing" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.EndedAt = "ended", &now
		reason := training.CloseNoContact
		d.Close, d.State = &reason, training.ItemClosed
	case training.CommandMarkCallDropped:
		if (state.CallStatus != "connected" && state.CallStatus != "held") || state.Dispatched {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.EndedAt = "ended", &now
		reason := training.CloseCallDropped
		d.Close, d.State = &reason, training.ItemClosed
	case training.CommandSaveIntakeDraft:
		if state.CallStatus == "ringing" || state.Dispatched {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			Draft training.IntakeCard `json:"draft"`
		}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		if p.Draft.Number != card.Number || p.Draft.AON != card.AON ||
			p.Draft.CallLocalTime != card.CallLocalTime || p.Draft.CallTimeZone != card.CallTimeZone ||
			!training.ValidIntakeCard(p.Draft) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		card, state.HasSavedDraft = p.Draft, true
	case training.CommandDispatchIntake:
		if !state.HasSavedDraft || state.Dispatched {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			ServiceCode string `json:"service_code"`
		}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		allowed := false
		for _, code := range item.IntakeRecipients {
			if code == p.ServiceCode {
				allowed = true
				break
			}
		}
		if !allowed {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.Dispatched, state.SelectedService = true, p.ServiceCode
		d.IntakeDispatch = &training.IntakeDispatch{ServiceCode: p.ServiceCode, CardSnapshot: card, SentAt: now}
	case training.CommandCompleteIntake:
		if state.CallStatus != "ended" || !state.Dispatched {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		reason := training.CloseCompleted
		d.Close, d.State = &reason, training.ItemClosed
	default:
		return reject(item, training.RejectTransitionNotAllowed), nil
	}
	d.IntakeCard, d.IntakeState = &card, &state
	return d, nil
}
