package operator112

import (
	"encoding/json"
	"testing"
	"time"

	"emsim/internal/training"
)

func TestIntakeFlowAndImmutableDispatch(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 3, 0, 0, time.UTC)
	card := training.UnansweredIntakeCard("112-1", "+79161313131", "02:03", "Europe/Moscow")
	state := training.IntakeState{CallStatus: "ringing", Transcript: []training.IntakeLine{}}
	item := training.Item{State: training.ItemOffered, IntakeCard: &card, IntakeState: &state,
		IntakeScript: []string{"Мне 19 лет", "Сильная диарея и обильная рвота"}, IntakeRecipients: []string{"pilot_ambulance"}}
	run := func(typ training.CommandType, p any) training.Decision {
		t.Helper()
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		d, err := New().Decide(item, training.Command{Type: typ, Payload: raw}, now)
		if err != nil {
			t.Fatal(err)
		}
		if d.Accepted {
			item.State, item.IntakeCard, item.IntakeState = d.State, d.IntakeCard, d.IntakeState
		}
		return d
	}
	if d := run(training.CommandDispatchIntake, map[string]string{"service_code": "pilot_ambulance"}); d.Accepted {
		t.Fatal("dispatch before save")
	}
	if d := run(training.CommandOpen, map[string]any{}); !d.Accepted || d.State != training.ItemOpened {
		t.Fatalf("open: %+v", d)
	}
	if d := run(training.CommandAnswerIncoming, map[string]any{}); !d.Accepted || len(d.IntakeState.Transcript) != 2 {
		t.Fatalf("answer: %+v", d)
	}
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": card}); !d.Accepted {
		t.Fatalf("save incomplete draft: %+v", d)
	}
	card.Complaint = training.IntakeField{State: "known", Value: "Сильная диарея и обильная рвота"}
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": card}); !d.Accepted {
		t.Fatalf("save complaint: %+v", d)
	}
	if d := run(training.CommandDispatchIntake, map[string]string{"service_code": "other"}); d.Accepted {
		t.Fatal("unlisted recipient")
	}
	d := run(training.CommandDispatchIntake, map[string]string{"service_code": "pilot_ambulance"})
	if !d.Accepted || d.IntakeDispatch == nil || d.IntakeDispatch.CardSnapshot.Complaint.Value != card.Complaint.Value {
		t.Fatalf("dispatch: %+v", d)
	}
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": card}); d.Accepted {
		t.Fatal("edit after dispatch")
	}
	if d := run(training.CommandCompleteIntake, map[string]any{}); d.Accepted {
		t.Fatal("complete while call connected")
	}
	if d := run(training.CommandEndIncoming, map[string]any{}); !d.Accepted {
		t.Fatalf("end: %+v", d)
	}
	if d := run(training.CommandCompleteIntake, map[string]any{}); !d.Accepted || d.Close == nil || d.State != training.ItemClosed {
		t.Fatalf("complete: %+v", d)
	}
}

func TestIntakeFieldStates(t *testing.T) {
	c := training.UnansweredIntakeCard("n", "a", "02:03", "Europe/Moscow")
	if !training.ValidIntakeCard(c) {
		t.Fatal("unanswered card should be valid")
	}
	c.VictimsPresent = training.IntakeField{State: "negative"}
	if !training.ValidIntakeCard(c) {
		t.Fatal("negative presence should be valid")
	}
	c.Age = training.IntakeField{State: "negative"}
	if training.ValidIntakeCard(c) {
		t.Fatal("negative age should be invalid")
	}
	c.Age = training.IntakeField{State: "unknown"}
	if !training.ValidIntakeCard(c) {
		t.Fatal("explicit unknown should be valid")
	}
}
