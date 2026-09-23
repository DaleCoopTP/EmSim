package operator112

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"emsim/internal/training"
)

type exercise struct{}

func New() training.Exercise { return exercise{} }

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

func (exercise) Decide(item training.Item, cmd training.Command, now time.Time) (training.Decision, error) {
	if item.IntakeCard == nil || item.IntakeState == nil {
		return reject(item, training.RejectTransitionNotAllowed), nil
	}
	card, state := *item.IntakeCard, *item.IntakeState
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
		state.Transcript = make([]training.IntakeLine, 0, len(item.IntakeScript))
		for _, line := range item.IntakeScript {
			state.Transcript = append(state.Transcript, training.IntakeLine{Text: line, ServerAt: now})
		}
		d.State = training.ItemInProgress
	case training.CommandEndIncoming:
		if state.CallStatus != "connected" {
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
		if state.CallStatus != "connected" || state.Dispatched {
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
