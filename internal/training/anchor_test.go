package training

import (
	"testing"

	"emsim/internal/content"

	"github.com/google/uuid"
)

// ADR-031: the end of an outgoing call anchors that contact's crew
// reports; an incoming call from the same contact, a call to another
// contact and a call that is merely starting do not.
func TestDDSEventAnchor(t *testing.T) {
	outgoing := Call{ID: uuid.New(), ContactKey: "crew_leader", Direction: CallOutgoing}
	legacy := Call{ID: uuid.New(), ContactKey: "crew_leader"}
	incoming := Call{ID: uuid.New(), ContactKey: "crew_leader", Direction: CallIncoming, EventKey: "e2"}
	calls := []Call{outgoing, legacy, incoming}
	ended := func(id uuid.UUID) Decision {
		return Decision{Accepted: true, Reaction: content.ReactionAccepted, EndCall: &CallEnd{CallID: id}}
	}
	cases := []struct {
		name     string
		cmd      CommandType
		decision Decision
		want     eventAnchor
		ok       bool
	}{
		{"open", CommandOpen, Decision{Reaction: content.ReactionReceived}, eventAnchor{name: "opened"}, true},
		{"accepted", CommandSetStatus, Decision{Reaction: content.ReactionAccepted}, eventAnchor{name: "accepted"}, true},
		{"working", CommandSetStatus, Decision{Reaction: content.ReactionWorking}, eventAnchor{name: "working"}, true},
		{"not_accepted", CommandSetStatus, Decision{Reaction: content.ReactionNotAccepted}, eventAnchor{}, false},
		{"outgoing call ended", CommandCallEnd, ended(outgoing.ID), eventAnchor{name: content.EventSinceCallEnded, contact: "crew_leader"}, true},
		{"pre-ДДС-2 call ended", CommandCallEnd, ended(legacy.ID), eventAnchor{name: content.EventSinceCallEnded, contact: "crew_leader"}, true},
		{"incoming call ended", CommandCallEnd, ended(incoming.ID), eventAnchor{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ddsEventAnchor(tc.cmd, tc.decision, calls)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ddsEventAnchor = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestEventAnchorMatchesCallContact(t *testing.T) {
	crewReport := content.Event{Key: "e1", Since: content.EventSinceCallEnded, SinceContact: "crew_leader"}
	accepted := content.Event{Key: "e2", Since: "accepted"}
	crew := eventAnchor{name: content.EventSinceCallEnded, contact: "crew_leader"}
	control := eventAnchor{name: content.EventSinceCallEnded, contact: "control"}
	if !crew.matches(crewReport) || control.matches(crewReport) || crew.matches(accepted) {
		t.Fatal("call_ended must match only its own contact's events")
	}
	if !(eventAnchor{name: "accepted"}).matches(accepted) || (eventAnchor{name: "accepted"}).matches(crewReport) {
		t.Fatal("status anchors must keep matching by since alone")
	}
}
