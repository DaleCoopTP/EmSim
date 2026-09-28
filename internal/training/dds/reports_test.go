package dds

import (
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// ADR-031: the monitor shows each delivered crew message and the first
// applied status after it reached the trainee — after delivery for a
// notice, after the answer for an incoming call; a missed call has none.
func TestReportReactions(t *testing.T) {
	item := baseItem(t, "ЮАО")
	t0 := item.OfferedAt
	item.Contacts = []content.Contact{{Key: "crew_leader", Label: "Руководитель бригады"}}
	answered := t0.Add(62 * time.Second)
	item.Calls = []training.Call{{ID: uuid.New(), ContactKey: "crew_leader", Direction: training.CallIncoming, EventKey: "e2", StartedAt: answered}}
	events := []training.DeliveredEvent{
		{Key: "e1", Delivery: "notice", From: "crew_leader", DeliveredAt: t0.Add(30 * time.Second)},
		{Key: "e2", Delivery: "phone_incoming", From: "crew_leader", DeliveredAt: t0.Add(60 * time.Second)},
		{Key: "e3", Delivery: "phone_incoming", From: "control", DeliveredAt: t0.Add(90 * time.Second)},
		{Key: "e4", Delivery: "spawn_card", DeliveredAt: t0.Add(95 * time.Second)},
	}
	status := func(at time.Duration, accepted bool) training.Action {
		return training.Action{Type: training.CommandSetStatus, Accepted: accepted, ServerAt: t0.Add(at)}
	}
	actions := []training.Action{
		status(10*time.Second, true), // accepted — before any report
		status(40*time.Second, false),
		{Type: training.CommandAddComment, Accepted: true, ServerAt: t0.Add(42 * time.Second)},
		status(45*time.Second, true), // reaction to e1
		status(75*time.Second, true), // reaction to e2 (after the answer)
	}

	got := ReportReactions(item, events, actions, t0.Add(125*time.Second))
	if len(got) != 3 {
		t.Fatalf("reports = %+v, want 3 (spawn_card excluded)", got)
	}
	if r := got[0]; r.FromLabel != "Руководитель бригады" || r.AnsweredAt != nil || r.Missed || r.ReactionAt == nil || !r.ReactionAt.Equal(t0.Add(45*time.Second)) {
		t.Fatalf("notice report = %+v", r)
	}
	if r := got[1]; r.AnsweredAt == nil || !r.AnsweredAt.Equal(answered) || r.ReactionAt == nil || !r.ReactionAt.Equal(t0.Add(75*time.Second)) {
		t.Fatalf("answered call report = %+v", r)
	}
	if r := got[2]; r.FromLabel != "control" || !r.Missed || r.ReactionAt != nil || r.AnsweredAt != nil {
		t.Fatalf("missed call report = %+v", r)
	}
	if r := ReportReactions(item, events, actions, t0.Add(100*time.Second))[2]; r.Missed {
		t.Fatalf("still ringing call reported missed: %+v", r)
	}
}
