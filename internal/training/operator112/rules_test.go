package operator112

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
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
	card.OnSitePhone = training.IntakeField{State: "known", Value: "+79161313131"}
	card.Address.Descriptive = training.IntakeField{State: "known", Value: "рядом с метро ВДНХ"}
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": card}); !d.Accepted {
		t.Fatalf("save complaint: %+v", d)
	}
	if d := run(training.CommandDispatchIntake, map[string]string{"service_code": "other"}); d.Accepted {
		t.Fatal("unlisted recipient")
	}
	d := run(training.CommandDispatchIntake, map[string]string{"service_code": "pilot_ambulance"})
	if !d.Accepted || d.IntakeDispatch == nil || d.IntakeDispatch.CardSnapshot.Complaint.Value != card.Complaint.Value ||
		d.IntakeDispatch.CardSnapshot.OnSitePhone.Value != card.OnSitePhone.Value ||
		d.IntakeDispatch.CardSnapshot.Address.Descriptive.Value != card.Address.Descriptive.Value {
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

func TestPreparedDialogueOrdersHoldAndRepeat(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 3, 0, 0, time.UTC)
	dialogue := &content.Intake112Dialogue{
		Initial: content.Intake112Utterance{ID: "initial", Text: "Нужна помощь", Reveals: []string{"complaint"}},
		Questions: []content.Intake112Question{
			{ID: "address", Text: "Где вы?", TopicID: "address", Answer: content.Intake112Utterance{ID: "address_reply", Text: "Москва, дом 2", Reveals: []string{"address"}}},
			{ID: "victims", Text: "Сколько пострадавших?", TopicID: "victims", Answer: content.Intake112Utterance{ID: "victims_partial", Text: "Несколько, сейчас уточню", Reveals: []string{}}},
			{ID: "clarify", Text: "Уточните число", TopicID: "victims", AvailableAfter: []string{"victims"}, Answer: content.Intake112Utterance{ID: "victims_reply", Text: "Двое", Reveals: []string{"victims_count"}}},
		},
	}
	orders := [][]string{{"address", "victims", "clarify"}, {"victims", "clarify", "address"}}
	for _, order := range orders {
		card := training.UnansweredIntakeCard("112-1", "+79161313131", "02:03", "Europe/Moscow")
		state := training.IntakeState{CallStatus: "ringing", Transcript: []training.IntakeLine{}}
		item := training.Item{ID: uuid.New(), State: training.ItemOpened, IntakeCard: &card, IntakeState: &state, IntakeDialogue: dialogue}
		ex := New().(exercise)
		run := func(typ training.CommandType, p any) training.Decision {
			t.Helper()
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			decision, err := ex.Decide(item, training.Command{CommandID: uuid.New(), Type: typ, Payload: raw}, now)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Accepted {
				item.State, item.IntakeCard, item.IntakeState = decision.State, decision.IntakeCard, decision.IntakeState
			}
			return decision
		}
		if d := run(training.CommandAnswerIncoming, map[string]any{}); !d.Accepted || len(item.IntakeState.Transcript) != 1 ||
			item.IntakeState.Transcript[0].Speaker != "caller" {
			t.Fatalf("initial turn: %+v", d)
		}
		if d := run(training.CommandAskIntakeQuestion, map[string]any{"question_id": "clarify"}); d.Accepted {
			t.Fatal("clarification before prerequisite accepted")
		}
		if d := run(training.CommandHoldIncoming, map[string]any{}); !d.Accepted {
			t.Fatal("hold rejected")
		}
		if len(ex.AvailableQuestions(item)) != 0 {
			t.Fatal("questions exposed while held")
		}
		if d := run(training.CommandAskIntakeQuestion, map[string]any{"question_id": order[0]}); d.Accepted {
			t.Fatal("question accepted while held")
		}
		if d := run(training.CommandResumeIncoming, map[string]any{}); !d.Accepted {
			t.Fatal("resume rejected")
		}
		for _, id := range order {
			if d := run(training.CommandAskIntakeQuestion, map[string]any{"question_id": id}); !d.Accepted {
				t.Fatalf("question %s: %+v", id, d)
			}
		}
		lines := item.IntakeState.Transcript
		if len(lines) != 7 || lines[1].Speaker != "operator" || lines[2].Speaker != "caller" ||
			lines[2].CallID != item.ID.String() || lines[2].ID == "" || len(item.IntakeState.AskedQuestionIDs) != 3 {
			t.Fatalf("dialogue: %+v", item.IntakeState)
		}
		if d := run(training.CommandAskIntakeQuestion, map[string]any{"question_id": "address"}); !d.Accepted ||
			item.IntakeState.Transcript[len(item.IntakeState.Transcript)-1].Text != "Москва, дом 2" || len(item.IntakeState.AskedQuestionIDs) != 3 {
			t.Fatalf("intentional repeat: %+v", d)
		}
		if d := run(training.CommandEndIncoming, map[string]any{}); !d.Accepted {
			t.Fatal("end rejected")
		}
		if d := run(training.CommandAskIntakeQuestion, map[string]any{"question_id": "address"}); d.Accepted {
			t.Fatal("question after end accepted")
		}
	}
}

func TestIntakeFieldStates(t *testing.T) {
	c := training.UnansweredIntakeCard("n", "a", "02:03", "Europe/Moscow")
	if !training.ValidIntakeCard(c) {
		t.Fatal("unanswered card should be valid")
	}
	if c.Channel.State != "unanswered" {
		t.Fatal("new card must not invent a telecom provider")
	}
	c.Channel = training.IntakeField{State: "known", Value: "МТС"}
	if !training.ValidIntakeCard(c) {
		t.Fatal("telecom provider should be accepted")
	}
	c.Channel = training.IntakeField{State: "known", Value: "phone"}
	if !training.ValidIntakeCard(c) {
		t.Fatal("legacy phone channel should remain valid")
	}
	c.Channel = training.IntakeField{State: "known", Value: " МТС"}
	if training.ValidIntakeCard(c) {
		t.Fatal("channel with surrounding whitespace should be rejected")
	}
	c.Channel = training.IntakeField{State: "unanswered"}
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
	c.Address.Descriptive = training.IntakeField{State: "known", Value: "рядом с метро ВДНХ"}
	c.OnSitePhone = training.IntakeField{State: "known", Value: "+79161313131"}
	if !training.ValidIntakeCard(c) {
		t.Fatal("new address and phone fields should be valid")
	}
	c.NoAccess = training.IntakeField{State: "negative"}
	if training.ValidIntakeCard(c) {
		t.Fatal("negative remains exclusive to victims_present")
	}
	c.NoAccess = training.IntakeField{State: "known", Value: "not-yes"}
	if training.ValidIntakeCard(c) {
		t.Fatal("flag values must be allowlisted")
	}
	c.NoAccess = training.IntakeField{State: "unanswered"}
	c.Complaint = training.IntakeField{State: "known", Value: strings.Repeat("x", 1999)}
	if !training.ValidIntakeCard(c) {
		t.Fatal("description accepts the reference limit")
	}
	c.Complaint.Value += "x"
	if training.ValidIntakeCard(c) {
		t.Fatal("description over the reference limit must fail")
	}
}

func TestExceptionalCallOutcomes(t *testing.T) {
	now := time.Date(2026, 9, 23, 2, 3, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		status  string
		state   training.ItemState
		command training.CommandType
		want    training.CloseReason
	}{
		{"no contact", "ringing", training.ItemOpened, training.CommandMarkNoContact, training.CloseNoContact},
		{"dropped call", "connected", training.ItemInProgress, training.CommandMarkCallDropped, training.CloseCallDropped},
	} {
		t.Run(test.name, func(t *testing.T) {
			card := training.UnansweredIntakeCard("112-1", "+79161313131", "02:03", "Europe/Moscow")
			state := training.IntakeState{CallStatus: test.status}
			item := training.Item{State: test.state, IntakeCard: &card, IntakeState: &state}
			decision, err := New().Decide(item, training.Command{Type: test.command, Payload: []byte(`{}`)}, now)
			if err != nil || !decision.Accepted || decision.Close == nil || *decision.Close != test.want ||
				decision.State != training.ItemClosed || decision.IntakeState.CallStatus != "ended" || decision.IntakeDispatch != nil ||
				decision.IntakeState.Transcript == nil {
				t.Fatalf("exceptional close: %+v, %v", decision, err)
			}
			item.State, item.IntakeCard, item.IntakeState = decision.State, decision.IntakeCard, decision.IntakeState
			if replay, err := New().Decide(item, training.Command{Type: test.command, Payload: []byte(`{}`)}, now); err != nil || replay.Accepted {
				t.Fatalf("cannot close twice: %+v, %v", replay, err)
			}
		})
	}
}
