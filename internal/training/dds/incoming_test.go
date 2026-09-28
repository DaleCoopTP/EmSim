package dds

import (
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

func answer(t *testing.T, item training.Item, key string, now time.Time) training.Decision {
	t.Helper()
	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandAnswerIncoming, Payload: mustJSON(t, map[string]string{"event_key": key})}, now)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

// ADR-031: answer_incoming opens an incoming call for a delivered
// phone_incoming event within its 30 s ring window; call_end closes it
// without a call log or a recording.
func TestAnswerIncomingCall(t *testing.T) {
	item := terminalItem(t, districtWorkflow())
	item.State, item.Reaction = training.ItemInProgress, content.ReactionWorking
	delivered := item.OfferedAt.Add(60 * time.Second)
	item.IncomingRings = []training.IncomingRing{{EventKey: "e2", From: "crew_leader", DeliveredAt: delivered}}

	if d := answer(t, item, "e9", delivered); d.Rejection != training.RejectInvalidPayload {
		t.Fatalf("unknown event = %+v", d)
	}
	if d := answer(t, item, "", delivered); d.Rejection != training.RejectInvalidPayload {
		t.Fatalf("empty event_key = %+v", d)
	}
	if d := answer(t, item, "e2", delivered.Add(31*time.Second)); d.Rejection != training.RejectCallMissed {
		t.Fatalf("answer after the ring = %+v, want call_missed", d)
	}
	offered := item
	offered.State = training.ItemOffered
	if d := answer(t, offered, "e2", delivered); d.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("answer before open = %+v", d)
	}
	busy := item
	busy.Calls = []training.Call{{ID: uuid.New(), ContactKey: "control", Direction: training.CallOutgoing, StartedAt: delivered}}
	if d := answer(t, busy, "e2", delivered.Add(5*time.Second)); d.Rejection != training.RejectCallInProgress {
		t.Fatalf("answer during an active call = %+v", d)
	}

	d := answer(t, item, "e2", delivered.Add(30*time.Second))
	if !d.Accepted || d.StartCall == nil || d.StartCall.Direction != training.CallIncoming || d.StartCall.EventKey != "e2" || d.StartCall.ContactKey != "crew_leader" {
		t.Fatalf("answer within the ring = %+v", d)
	}
	call := *d.StartCall
	call.ID = uuid.New()
	item.Calls = []training.Call{call}

	if ringing := RingingCall(item, delivered.Add(10*time.Second)); ringing != nil {
		t.Fatalf("answered call still rings: %+v", ringing)
	}
	// A terminal status is refused while the incoming conversation lasts
	// (ADR-030's close precondition).
	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandSetStatus, Payload: mustJSON(t, map[string]string{"status": "completed"})}, delivered)
	if err != nil || decision.Rejection != training.RejectCallInProgress {
		t.Fatalf("terminal status during a call = %+v, %v", decision, err)
	}
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandCallEnd, Payload: mustJSON(t, map[string]any{"call_id": call.ID, "accepted_by": "", "summary": "", "recording": map[string]any{"sha256": "00", "size": 1, "mime": "audio/webm"}})}, delivered)
	if err != nil || decision.Rejection != training.RejectInvalidPayload {
		t.Fatalf("incoming call_end with a recording = %+v, %v", decision, err)
	}
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandCallEnd, Payload: mustJSON(t, map[string]any{"call_id": call.ID, "accepted_by": "", "summary": "", "recording": nil})}, delivered)
	if err != nil || !decision.Accepted || decision.EndCall == nil {
		t.Fatalf("incoming call_end without a call log = %+v, %v", decision, err)
	}
	now := delivered.Add(20 * time.Second)
	item.Calls[0].EndedAt = &now
	if d := answer(t, item, "e2", now); d.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("second answer = %+v", d)
	}
}

// An outgoing call still needs «Кто принял»/«Суть сообщения».
func TestOutgoingCallEndStillNeedsCallLog(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State, item.Reaction = training.ItemInProgress, content.ReactionAccepted
	call := training.Call{ID: uuid.New(), ContactKey: "crew_leader", Direction: training.CallOutgoing, StartedAt: item.OfferedAt}
	item.Calls = []training.Call{call}
	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandCallEnd, Payload: mustJSON(t, map[string]any{"call_id": call.ID, "accepted_by": "", "summary": "", "recording": nil})}, item.OfferedAt)
	if err != nil || decision.Rejection != training.RejectInvalidPayload {
		t.Fatalf("outgoing call_end without a call log = %+v, %v", decision, err)
	}
}

func TestRingingCallPicksNewestUnansweredWithinWindow(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State = training.ItemInProgress
	t0 := item.OfferedAt
	item.IncomingRings = []training.IncomingRing{
		{EventKey: "e1", From: "control", DeliveredAt: t0},
		{EventKey: "e2", From: "crew_leader", DeliveredAt: t0.Add(10 * time.Second)},
	}
	if r := RingingCall(item, t0.Add(15*time.Second)); r == nil || r.EventKey != "e2" {
		t.Fatalf("ringing = %+v, want e2", r)
	}
	if r := RingingCall(item, t0.Add(35*time.Second)); r == nil || r.EventKey != "e2" {
		t.Fatalf("ringing = %+v, want e2 (e1 missed)", r)
	}
	if r := RingingCall(item, t0.Add(41*time.Second)); r != nil {
		t.Fatalf("ringing = %+v, want none", r)
	}
	if !Missed(item.IncomingRings[0], nil, t0.Add(31*time.Second)) || Missed(item.IncomingRings[0], nil, t0.Add(30*time.Second)) {
		t.Fatal("missed window boundary")
	}
	item.State = training.ItemClosed
	if r := RingingCall(item, t0.Add(15*time.Second)); r != nil {
		t.Fatalf("closed item rings: %+v", r)
	}
}
