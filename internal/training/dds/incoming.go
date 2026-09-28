package dds

import (
	"encoding/json"
	"strings"
	"time"

	"emsim/internal/training"
)

// IncomingRingS is how long a delivered phone_incoming event rings before
// it is missed (ADR-031). Ringing and missed are derived from server time,
// so no scheduler transition is needed.
const IncomingRingS = 30

// RingUntil is the end of ring's ringing window.
func RingUntil(ring training.IncomingRing) time.Time {
	return ring.DeliveredAt.Add(IncomingRingS * time.Second)
}

// AnsweredEvents is the set of phone_incoming event keys the trainee has
// answered — each has its incoming call.
func AnsweredEvents(calls []training.Call) map[string]bool {
	out := map[string]bool{}
	for _, c := range calls {
		if !c.Outgoing() && c.EventKey != "" {
			out[c.EventKey] = true
		}
	}
	return out
}

// Missed reports whether ring went unanswered past its window at now.
func Missed(ring training.IncomingRing, calls []training.Call, now time.Time) bool {
	return !AnsweredEvents(calls)[ring.EventKey] && now.After(RingUntil(ring))
}

// RingingCall is the incoming call ringing on the item at now: the most
// recently delivered phone_incoming that is neither answered nor past
// its window, or nil. A closed or interrupted item has no ringing call.
func RingingCall(item training.Item, now time.Time) *training.IncomingRing {
	if item.State == training.ItemClosed || item.State == training.ItemInterrupted {
		return nil
	}
	answered := AnsweredEvents(item.Calls)
	var ringing *training.IncomingRing
	for i := range item.IncomingRings {
		ring := item.IncomingRings[i]
		if answered[ring.EventKey] || now.After(RingUntil(ring)) {
			continue
		}
		if ringing == nil || ring.DeliveredAt.After(ringing.DeliveredAt) {
			ringing = &ring
		}
	}
	return ringing
}

type answerIncomingPayload struct {
	EventKey string `json:"event_key"`
}

// decideAnswerIncoming is answer_incoming {event_key} (ADR-031): it opens
// an incoming call for a delivered, still ringing phone_incoming event.
func decideAnswerIncoming(item training.Item, cmd training.Command, now time.Time) training.Decision {
	if item.State == training.ItemOffered {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	var payload answerIncomingPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil || strings.TrimSpace(payload.EventKey) == "" {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	var ring *training.IncomingRing
	for i := range item.IncomingRings {
		if item.IncomingRings[i].EventKey == payload.EventKey {
			ring = &item.IncomingRings[i]
			break
		}
	}
	if ring == nil {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	if AnsweredEvents(item.Calls)[ring.EventKey] {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	if now.After(RingUntil(*ring)) {
		return rejectDecision(item, training.RejectCallMissed)
	}
	if activeCall(item) != nil {
		return rejectDecision(item, training.RejectCallInProgress)
	}
	return training.Decision{Accepted: true, Reaction: item.Reaction, State: item.State, Card: item.Card,
		StartCall: &training.Call{ContactKey: ring.From, Direction: training.CallIncoming, EventKey: ring.EventKey,
			StartedAt: now, ReactionAtCall: item.Reaction, RecordingState: training.RecordingAbsent}}
}
