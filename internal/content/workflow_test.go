package content

import (
	"errors"
	"strings"
	"testing"
)

func districtWorkflow() Workflow {
	return Workflow{
		Transitions: map[Reaction][]Reaction{
			ReactionAdded:       {ReactionReceived},
			ReactionReceived:    {ReactionAccepted, ReactionNotAccepted},
			ReactionNotAccepted: {ReactionAccepted},
			ReactionAccepted:    {ReactionResponding, ReactionCompleted, ReactionRefused},
			ReactionResponding:  {ReactionCompleted, ReactionRefused},
		},
		CommentRequired: []Reaction{ReactionNotAccepted, ReactionRefused},
		Terminal:        []Reaction{ReactionCompleted, ReactionRefused},
	}
}

func TestWorkflowValidate(t *testing.T) {
	pilot := Workflow{
		Transitions:     map[Reaction][]Reaction{ReactionAdded: {ReactionReceived}, ReactionReceived: {ReactionAccepted, ReactionNotAccepted}, ReactionNotAccepted: {ReactionAccepted}},
		CommentRequired: []Reaction{ReactionNotAccepted},
	}
	if err := pilot.Validate(); err != nil {
		t.Fatalf("pilot workflow without terminal statuses: %v", err)
	}
	if err := districtWorkflow().Validate(); err != nil {
		t.Fatalf("district workflow: %v", err)
	}

	cases := map[string]struct {
		mutate func(*Workflow)
		reason string
	}{
		"terminal with outgoing transition": {
			mutate: func(w *Workflow) { w.Transitions[ReactionCompleted] = []Reaction{ReactionWorking} },
			reason: "has_outgoing_transitions:completed",
		},
		"terminal unreachable from received": {
			mutate: func(w *Workflow) { w.Terminal = append(w.Terminal, ReactionCompletedWithoutTeam) },
			reason: "unreachable_from_received:completed_without_team",
		},
		"unknown status": {
			mutate: func(w *Workflow) { w.CommentRequired = append(w.CommentRequired, Reaction("closed")) },
			reason: "unknown_status:closed",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := districtWorkflow()
			tc.mutate(&w)
			err := w.Validate()
			var ve *ValidationError
			if !errors.As(err, &ve) || !strings.Contains(ve.Reason, tc.reason) {
				t.Fatalf("Validate() = %v, want reason %q", err, tc.reason)
			}
		})
	}
}

func TestWorkflowIsTerminal(t *testing.T) {
	w := districtWorkflow()
	if !w.IsTerminal(ReactionCompleted) || !w.IsTerminal(ReactionRefused) {
		t.Fatal("completed and refused must be terminal")
	}
	if w.IsTerminal(ReactionAccepted) || w.IsTerminal(ReactionNotAccepted) {
		t.Fatal("accepted and not_accepted must not be terminal")
	}
	if (Workflow{}).IsTerminal(ReactionCompleted) {
		t.Fatal("a workflow without terminal statuses has none")
	}
}

func TestDecodeServiceDefsRejectsInvalidWorkflow(t *testing.T) {
	file := `[{"code":"x","name":"X","workflow":{"transitions":{"received":["completed"],"completed":["working"]},"comment_required":[],"terminal":["completed"]}}]`
	_, err := DecodeServiceDefs(strings.NewReader(file))
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "[0].workflow.terminal" {
		t.Fatalf("DecodeServiceDefs() = %v, want [0].workflow.terminal", err)
	}
}
