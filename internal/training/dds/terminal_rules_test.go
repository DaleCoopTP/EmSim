package dds

import (
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"
)

// districtWorkflow and ambulanceWorkflow mirror seed/services.json's
// dds_district_chertanovo and dds_ambulance_03 (ADR-030).
func districtWorkflow() content.Workflow {
	return content.Workflow{
		Transitions: map[content.Reaction][]content.Reaction{
			content.ReactionAdded:       {content.ReactionReceived},
			content.ReactionReceived:    {content.ReactionAccepted, content.ReactionNotAccepted},
			content.ReactionNotAccepted: {content.ReactionAccepted},
			content.ReactionAccepted:    {content.ReactionResponding, content.ReactionArrived, content.ReactionWorking, content.ReactionCompleted, content.ReactionRefused},
			content.ReactionResponding:  {content.ReactionArrived, content.ReactionWorking, content.ReactionCompleted, content.ReactionRefused},
			content.ReactionArrived:     {content.ReactionWorking, content.ReactionCompleted, content.ReactionRefused},
			content.ReactionWorking:     {content.ReactionCompleted, content.ReactionRefused},
		},
		CommentRequired: []content.Reaction{content.ReactionNotAccepted, content.ReactionRefused},
		Terminal:        []content.Reaction{content.ReactionCompleted, content.ReactionRefused},
	}
}

func ambulanceWorkflow() content.Workflow {
	return content.Workflow{
		Transitions: map[content.Reaction][]content.Reaction{
			content.ReactionAdded:      {content.ReactionReceived},
			content.ReactionReceived:   {content.ReactionAccepted, content.ReactionCompletedWithoutTeam},
			content.ReactionAccepted:   {content.ReactionResponding, content.ReactionArrived, content.ReactionWorking, content.ReactionCompleted, content.ReactionCompletedWithoutTeam},
			content.ReactionWorking:    {content.ReactionCompleted, content.ReactionCompletedWithoutTeam},
			content.ReactionArrived:    {content.ReactionWorking, content.ReactionCompleted, content.ReactionCompletedWithoutTeam},
			content.ReactionResponding: {content.ReactionArrived, content.ReactionWorking, content.ReactionCompleted, content.ReactionCompletedWithoutTeam},
		},
		CommentRequired: []content.Reaction{content.ReactionCompletedWithoutTeam},
		Terminal:        []content.Reaction{content.ReactionCompleted, content.ReactionCompletedWithoutTeam},
	}
}

// terminalItem is an opened card (reaction received) under a workflow
// with terminal statuses, without pilot_goal.
func terminalItem(t *testing.T, workflow content.Workflow) training.Item {
	t.Helper()
	item := baseItem(t, "ЮАО")
	item.TargetService = "dds_district_chertanovo"
	item.Workflow = workflow
	item.PilotGoal = ""
	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandOpen}, item.OfferedAt.Add(5*time.Second))
	if err != nil {
		t.Fatalf("Decide(open): %v", err)
	}
	return applyAccepted(t, item, decision)
}

func setStatus(t *testing.T, item training.Item, status, comment string, now time.Time) training.Decision {
	t.Helper()
	payload := map[string]string{"status": status}
	if comment != "" {
		payload["comment"] = comment
	}
	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandSetStatus, Payload: mustJSON(t, payload)}, now)
	if err != nil {
		t.Fatalf("Decide(set_status %s): %v", status, err)
	}
	return decision
}

func TestTerminalStatusClosesFullCycle(t *testing.T) {
	item := terminalItem(t, districtWorkflow())
	now := item.OfferedAt.Add(10 * time.Second)
	for _, status := range []string{"accepted", "responding", "arrived", "working"} {
		decision := setStatus(t, item, status, "по докладу бригады", now)
		if !decision.Accepted || decision.Close != nil || decision.State != training.ItemInProgress {
			t.Fatalf("set_status %s = %+v, want accepted, not closed", status, decision)
		}
		item = applyAccepted(t, item, decision)
		now = now.Add(20 * time.Second)
	}
	decision := setStatus(t, item, "completed", "работы завершены, проезд свободен", now)
	if !decision.Accepted || decision.State != training.ItemClosed || decision.Reaction != content.ReactionCompleted {
		t.Fatalf("set_status completed = %+v, want closed completed", decision)
	}
	if decision.Close == nil || *decision.Close != training.CloseCompleted {
		t.Fatalf("close reason = %v, want completed", decision.Close)
	}
}

func TestTerminalRefusedNeedsCommentAndClosesAsRefused(t *testing.T) {
	item := terminalItem(t, districtWorkflow())
	now := item.OfferedAt.Add(10 * time.Second)
	item = applyAccepted(t, item, setStatus(t, item, "accepted", "", now))

	if d := setStatus(t, item, "refused", "", now.Add(time.Second)); d.Accepted || d.Rejection != training.RejectCommentRequired {
		t.Fatalf("refused without comment = %+v, want comment_required", d)
	}
	d := setStatus(t, item, "refused", "объект Ростелеком, информация передана", now.Add(2*time.Second))
	if !d.Accepted || d.State != training.ItemClosed || d.Close == nil || *d.Close != training.CloseRefused {
		t.Fatalf("refused with comment = %+v, want closed as refused", d)
	}
}

func TestNotAcceptedDoesNotCloseUnderTerminalWorkflow(t *testing.T) {
	item := terminalItem(t, districtWorkflow())
	now := item.OfferedAt.Add(10 * time.Second)
	d := setStatus(t, item, "not_accepted", "не обслуживаем, передано в УК", now)
	if !d.Accepted || d.Close != nil {
		t.Fatalf("not_accepted = %+v, want accepted and open", d)
	}
	item = applyAccepted(t, item, d)
	if d := setStatus(t, item, "accepted", "", now.Add(time.Second)); !d.Accepted {
		t.Fatalf("not_accepted -> accepted = %+v", d)
	}
}

func TestTerminalStatusRejectedWhileCallActiveOrRequiredCallMissing(t *testing.T) {
	item := terminalItem(t, districtWorkflow())
	now := item.OfferedAt.Add(10 * time.Second)
	item = applyAccepted(t, item, setStatus(t, item, "accepted", "", now))

	item.Calls = []training.Call{{ContactKey: "crew_leader", StartedAt: now}}
	if d := setStatus(t, item, "completed", "итог", now.Add(time.Second)); d.Accepted || d.Rejection != training.RejectCallInProgress {
		t.Fatalf("completed during a call = %+v, want call_in_progress", d)
	}

	item.Calls = nil
	item.CallPolicy = content.Call{Required: true, To: "crew_leader"}
	if d := setStatus(t, item, "completed", "итог", now.Add(time.Second)); d.Accepted || d.Rejection != training.RejectCallRequired {
		t.Fatalf("completed without the required call = %+v, want call_required", d)
	}
}

func TestLegacyCommandsRejectedUnderTerminalWorkflow(t *testing.T) {
	item := terminalItem(t, districtWorkflow())
	now := item.OfferedAt.Add(10 * time.Second)
	commands := []training.Command{
		{Type: training.CommandClose},
		{Type: training.CommandAddComment, Payload: mustJSON(t, map[string]string{"text": "комментарий"})},
		{Type: training.CommandSetCardField, Payload: mustJSON(t, map[string]string{"path": "/card/address/okrug", "value": "ЮАО"})},
	}
	for _, cmd := range commands {
		d, err := Exercise.Decide(item, cmd, now)
		if err != nil {
			t.Fatalf("Decide(%s): %v", cmd.Type, err)
		}
		if d.Accepted || d.Rejection != training.RejectTransitionNotAllowed {
			t.Fatalf("%s = %+v, want transition_not_allowed", cmd.Type, d)
		}
	}
}

func TestAmbulanceHasNoNotAcceptedAndClosesWithoutTeam(t *testing.T) {
	item := terminalItem(t, ambulanceWorkflow())
	now := item.OfferedAt.Add(10 * time.Second)
	if d := setStatus(t, item, "not_accepted", "не наш вызов", now); d.Accepted || d.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("03 not_accepted = %+v, want transition_not_allowed", d)
	}
	if d := setStatus(t, item, "completed_without_team", "", now); d.Accepted || d.Rejection != training.RejectCommentRequired {
		t.Fatalf("completed_without_team without comment = %+v, want comment_required", d)
	}
	d := setStatus(t, item, "completed_without_team", "консультация по телефону, бригада не требуется", now)
	if !d.Accepted || d.State != training.ItemClosed || d.Close == nil || *d.Close != training.CloseCompleted {
		t.Fatalf("completed_without_team = %+v, want closed as completed", d)
	}
	if d.PrimaryAt == nil {
		t.Fatal("a closing first decision still fixes primary_at")
	}
}
