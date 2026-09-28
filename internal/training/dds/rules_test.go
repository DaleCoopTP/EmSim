package dds

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// pilotWorkflow is seed/services.json's dds_district workflow — the one
// every pilot scenario in slice 2/3 uses: added -> received ->
// {accepted, not_accepted}, not_accepted -> accepted, comment required
// for not_accepted, nothing terminal listed (close is decided by
// decideClose's own rules, not workflow.Terminal, in this slice).
func pilotWorkflow() content.Workflow {
	return content.Workflow{
		Transitions: map[content.Reaction][]content.Reaction{
			content.ReactionAdded:       {content.ReactionReceived},
			content.ReactionReceived:    {content.ReactionAccepted, content.ReactionNotAccepted},
			content.ReactionNotAccepted: {content.ReactionAccepted},
		},
		CommentRequired: []content.Reaction{content.ReactionNotAccepted},
	}
}

// baseItem builds an offered item for one of the two pilot scenarios
// (seed/scenarios/pilot-tree-01.json okrug=ЮАО already correct,
// pilot-tree-02.json okrug=ЮАР needing correction) with pilotGoal
// "accept_card" as both pilots set (ADR-017).
func baseItem(t *testing.T, okrug string) training.Item {
	t.Helper()
	offeredAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	digest := make([]byte, 32)
	address := content.Address{
		Country: "Россия", City: "Москва", Okrug: okrug, District: "Чертаново Южное",
		Street: "Чертановская улица", House: "58", Building: "2", Entrance: "2",
	}
	address.Text = formatPilotAddressText(address)
	return training.Item{
		ID:                uuid.New(),
		RunID:             uuid.New(),
		LessonID:          uuid.New(),
		UserID:            uuid.New(),
		WorkstationNo:     5,
		ScenarioVersionID: uuid.New(),
		ScenarioDigest:    hex.EncodeToString(digest),
		TargetService:     "dds_district",
		Ordinal:           1,
		State:             training.ItemOffered,
		Reaction:          content.ReactionAdded,
		Card: content.CardPreview{
			Number:              "881412",
			RegisteredAtOffsetS: -60,
			Address:             address,
			Incident:            content.IncidentPreview{TypeCode: "14080106", TypeName: "Дерево упало во дворе"},
			NotificationList: []content.NotificationPreview{
				{Service: "dds_district", Status: content.ReactionAdded, Mine: true},
			},
		},
		Workflow:        pilotWorkflow(),
		PilotGoal:       "accept_card",
		Mode:            training.ModeTraining,
		Seq:             0,
		LogSeq:          0,
		TimingEffective: training.Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180},
		Deadlines: training.Deadlines{
			OpenAt:    offeredAt.Add(30 * time.Second),
			PrimaryAt: offeredAt.Add(30 * time.Second),
		},
		OfferedAt: offeredAt,
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func applyAccepted(t *testing.T, item training.Item, decision training.Decision) training.Item {
	t.Helper()
	if !decision.Accepted {
		t.Fatalf("applyAccepted called on a rejected decision: %+v", decision)
	}
	item.Reaction = decision.Reaction
	item.State = decision.State
	item.Card = decision.Card
	if decision.OpenedAt != nil {
		item.OpenedAt = decision.OpenedAt
	}
	if decision.PrimaryAt != nil {
		item.PrimaryAt = decision.PrimaryAt
		item.Deadlines.CompleteAt = decision.CompleteAt
	}
	if decision.Close != nil {
		item.CloseReason = decision.Close
	}
	return item
}

func TestPilotOneAcceptWithoutCorrection(t *testing.T) {
	item := baseItem(t, "ЮАО") // pilot-tree-01: already correct
	now := item.OfferedAt.Add(5 * time.Second)

	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandOpen}, now)
	if err != nil {
		t.Fatalf("Decide(open): %v", err)
	}
	if !decision.Accepted || decision.State != training.ItemOpened || decision.Reaction != content.ReactionReceived {
		t.Fatalf("open decision = %+v", decision)
	}
	item = applyAccepted(t, item, decision)

	now = now.Add(10 * time.Second)
	decision, err = Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "accepted"}),
	}, now)
	if err != nil {
		t.Fatalf("Decide(set_status accepted): %v", err)
	}
	if !decision.Accepted || decision.Reaction != content.ReactionAccepted || decision.State != training.ItemInProgress {
		t.Fatalf("accept decision = %+v", decision)
	}
	if decision.PrimaryAt == nil || !decision.PrimaryAt.Equal(now) {
		t.Fatalf("primary_at = %v, want %v", decision.PrimaryAt, now)
	}
	wantComplete := now.Add(180 * time.Second)
	if decision.CompleteAt == nil || !decision.CompleteAt.Equal(wantComplete) {
		t.Fatalf("complete_at = %v, want %v", decision.CompleteAt, wantComplete)
	}
	item = applyAccepted(t, item, decision)

	now = now.Add(3 * time.Second)
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandClose}, now)
	if err != nil {
		t.Fatalf("Decide(close): %v", err)
	}
	if !decision.Accepted || decision.Close == nil || *decision.Close != training.ClosePilotCompleted {
		t.Fatalf("close decision = %+v", decision)
	}
	if decision.Reaction != content.ReactionAccepted {
		t.Fatalf("close must not change reaction, got %v", decision.Reaction)
	}
}

func TestPilotTwoAcceptWithCorrection(t *testing.T) {
	item := baseItem(t, "ЮАР") // pilot-tree-02: needs correction
	now := item.OfferedAt.Add(5 * time.Second)

	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandOpen}, now)
	if err != nil || !decision.Accepted {
		t.Fatalf("Decide(open) = %+v, %v", decision, err)
	}
	item = applyAccepted(t, item, decision)

	now = now.Add(5 * time.Second)
	decision, err = Exercise.Decide(item, training.Command{
		Type:    training.CommandSetCardField,
		Payload: mustJSON(t, map[string]string{"path": allowedFieldCorrectionPath, "value": "ЮАО"}),
	}, now)
	if err != nil {
		t.Fatalf("Decide(set_card_field): %v", err)
	}
	if !decision.Accepted {
		t.Fatalf("set_card_field rejected: %+v", decision)
	}
	if decision.Card.Address.Okrug != "ЮАО" {
		t.Fatalf("okrug = %q, want ЮАО", decision.Card.Address.Okrug)
	}
	wantText := "Россия, Москва, (ЮАО, Чертаново Южное), Чертановская улица, 58, к. 2, под. 2"
	if decision.Card.Address.Text != wantText {
		t.Fatalf("address text = %q, want %q", decision.Card.Address.Text, wantText)
	}
	if decision.Effect["path"] != allowedFieldCorrectionPath || decision.Effect["old"] != "ЮАР" || decision.Effect["new"] != "ЮАО" {
		t.Fatalf("effect = %+v", decision.Effect)
	}
	// Reaction/state must not change: this is a correction, not a
	// decision about the card.
	if decision.Reaction != content.ReactionReceived || decision.State != training.ItemOpened {
		t.Fatalf("set_card_field must not change reaction/state, got %v/%v", decision.Reaction, decision.State)
	}
	item = applyAccepted(t, item, decision)

	now = now.Add(10 * time.Second)
	decision, err = Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "accepted"}),
	}, now)
	if err != nil || !decision.Accepted {
		t.Fatalf("Decide(set_status accepted) = %+v, %v", decision, err)
	}
	item = applyAccepted(t, item, decision)

	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandClose}, now.Add(2*time.Second))
	if err != nil || !decision.Accepted || decision.Close == nil || *decision.Close != training.ClosePilotCompleted {
		t.Fatalf("Decide(close) = %+v, %v", decision, err)
	}
	if item.Card.Address.Okrug != "ЮАО" {
		t.Fatalf("final card okrug = %q, want ЮАО (uncorrected value must not leak into the closed item)", item.Card.Address.Okrug)
	}
}

// TestPilotTwoAcceptWithoutCorrection: ADR-017 does not require the
// trainee to fix the error to finish the exercise — the pilot's whole
// point is that the mistake is still visible afterwards, not that the
// server silently blocks completion until it is fixed.
func TestPilotTwoAcceptWithoutCorrection(t *testing.T) {
	item := baseItem(t, "ЮАР")
	now := item.OfferedAt.Add(5 * time.Second)

	decision, _ := Exercise.Decide(item, training.Command{Type: training.CommandOpen}, now)
	item = applyAccepted(t, item, decision)

	decision, err := Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "accepted"}),
	}, now.Add(5*time.Second))
	if err != nil || !decision.Accepted {
		t.Fatalf("Decide(set_status accepted) = %+v, %v", decision, err)
	}
	item = applyAccepted(t, item, decision)

	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandClose}, now.Add(8*time.Second))
	if err != nil || !decision.Accepted || decision.Close == nil || *decision.Close != training.ClosePilotCompleted {
		t.Fatalf("Decide(close) = %+v, %v, want accepted pilot_completed", decision, err)
	}
	if item.Card.Address.Okrug != "ЮАР" {
		t.Fatalf("uncorrected okrug must remain ЮАР in the item, got %q", item.Card.Address.Okrug)
	}
}

func TestSetCardFieldRejectedAfterAccepted(t *testing.T) {
	item := baseItem(t, "ЮАР")
	item.Reaction = content.ReactionAccepted
	item.State = training.ItemInProgress

	decision, err := Exercise.Decide(item, training.Command{
		Type:    training.CommandSetCardField,
		Payload: mustJSON(t, map[string]string{"path": allowedFieldCorrectionPath, "value": "ЮАО"}),
	}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("decision = %+v, want transition_not_allowed", decision)
	}
}

func TestSetCardFieldRejectsUnknownPathAndEmptyValue(t *testing.T) {
	item := baseItem(t, "ЮАР")
	item.Reaction = content.ReactionReceived
	item.State = training.ItemOpened

	cases := []struct {
		name    string
		payload map[string]string
	}{
		{"unknown path", map[string]string{"path": "/card/incident/description", "value": "x"}},
		{"empty value", map[string]string{"path": allowedFieldCorrectionPath, "value": "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := Exercise.Decide(item, training.Command{
				Type:    training.CommandSetCardField,
				Payload: mustJSON(t, tc.payload),
			}, item.OfferedAt)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if decision.Accepted || decision.Rejection != training.RejectInvalidPayload {
				t.Fatalf("decision = %+v, want invalid_payload", decision)
			}
		})
	}
}

func TestCloseFromReceivedRejected(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.Reaction = content.ReactionReceived
	item.State = training.ItemOpened

	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandClose}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("decision = %+v, want transition_not_allowed", decision)
	}
}

func TestCloseFromAcceptedWithoutPilotGoalRejected(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.PilotGoal = "" // not a pilot scenario: ordinary ADR-011 rules apply
	item.Reaction = content.ReactionAccepted
	item.State = training.ItemInProgress

	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandClose}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("decision = %+v, want transition_not_allowed", decision)
	}
}

func TestNotAcceptedRequiresComment(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.Reaction = content.ReactionReceived
	item.State = training.ItemOpened

	decision, err := Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "not_accepted"}),
	}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectCommentRequired {
		t.Fatalf("decision = %+v, want comment_required", decision)
	}

	decision, err = Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "not_accepted", "comment": "не наша территория, передано в ДДС Северного"}),
	}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !decision.Accepted || decision.Reaction != content.ReactionNotAccepted {
		t.Fatalf("decision = %+v, want accepted not_accepted", decision)
	}
}

// TestRepeatDecisionDoesNotResetPrimaryAt: RFC-001 §7.2 "Повтор статуса
// не перезапускает таймер" — once primary_at is set by the first applied
// decision (here, not_accepted), a later transition (not_accepted ->
// accepted) leaves it alone.
func TestRepeatDecisionDoesNotResetPrimaryAt(t *testing.T) {
	item := baseItem(t, "ЮАО")
	now := item.OfferedAt.Add(5 * time.Second)

	decision, _ := Exercise.Decide(item, training.Command{Type: training.CommandOpen}, now)
	item = applyAccepted(t, item, decision)

	now = now.Add(10 * time.Second)
	decision, err := Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "not_accepted", "comment": "не наша территория"}),
	}, now)
	if err != nil || !decision.Accepted || decision.PrimaryAt == nil {
		t.Fatalf("first decision = %+v, %v", decision, err)
	}
	firstPrimaryAt := *decision.PrimaryAt
	item = applyAccepted(t, item, decision)
	if item.PrimaryAt == nil || !item.PrimaryAt.Equal(firstPrimaryAt) {
		t.Fatalf("item.PrimaryAt = %v, want %v", item.PrimaryAt, firstPrimaryAt)
	}

	now = now.Add(20 * time.Second)
	decision, err = Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "accepted"}),
	}, now)
	if err != nil || !decision.Accepted {
		t.Fatalf("second decision = %+v, %v", decision, err)
	}
	if decision.PrimaryAt != nil || decision.CompleteAt != nil {
		t.Fatalf("repeat decision must not report a new primary_at/complete_at: %+v", decision)
	}
}

func TestRepeatOpenRejected(t *testing.T) {
	item := baseItem(t, "ЮАО")
	decision, _ := Exercise.Decide(item, training.Command{Type: training.CommandOpen}, item.OfferedAt)
	item = applyAccepted(t, item, decision)

	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandOpen}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("second open = %+v, want transition_not_allowed", decision)
	}
}

func TestAddCommentBeforeOpenRejected(t *testing.T) {
	item := baseItem(t, "ЮАО")
	decision, err := Exercise.Decide(item, training.Command{
		Type:    training.CommandAddComment,
		Payload: mustJSON(t, map[string]string{"text": "тест"}),
	}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectTransitionNotAllowed {
		t.Fatalf("decision = %+v, want transition_not_allowed", decision)
	}
}

func TestAddCommentRejectsEmptyText(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State = training.ItemOpened
	decision, err := Exercise.Decide(item, training.Command{
		Type:    training.CommandAddComment,
		Payload: mustJSON(t, map[string]string{"text": "   "}),
	}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectInvalidPayload {
		t.Fatalf("decision = %+v, want invalid_payload", decision)
	}
}

func TestSetStatusRejectsUnknownStatusValue(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State = training.ItemOpened
	decision, err := Exercise.Decide(item, training.Command{
		Type:    training.CommandSetStatus,
		Payload: mustJSON(t, map[string]string{"status": "not_a_real_status"}),
	}, item.OfferedAt)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Accepted || decision.Rejection != training.RejectInvalidPayload {
		t.Fatalf("decision = %+v, want invalid_payload", decision)
	}
}

func TestUnsupportedCommandTypesRejected(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State = training.ItemInProgress
	item.Reaction = content.ReactionAccepted
	for _, ct := range []training.CommandType{training.CommandCallStart, training.CommandCallEnd, training.CommandControlReport} {
		decision, err := Exercise.Decide(item, training.Command{Type: ct}, item.OfferedAt)
		if err != nil {
			t.Fatalf("Decide(%s): %v", ct, err)
		}
		want := training.RejectTransitionNotAllowed
		if ct == training.CommandCallStart || ct == training.CommandCallEnd {
			want = training.RejectInvalidPayload
		}
		if decision.Accepted || decision.Rejection != want {
			t.Fatalf("Decide(%s) = %+v, want %s", ct, decision, want)
		}
	}
}

func TestPhoneCallRulesRequireFinishedTargetCallAndAllowNullRecording(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State, item.Reaction = training.ItemOpened, content.ReactionReceived
	item.PilotGoal = "accept_card"
	item.Contacts = []content.Contact{{Key: "crew_leader", Label: "Бригада", Number: "1234"}}
	item.CallPolicy = content.Call{Required: true, To: "crew_leader", BeforeStatus: content.ReactionAccepted}

	decision, err := Exercise.Decide(item, training.Command{Type: training.CommandSetStatus, Payload: mustJSON(t, map[string]string{"status": "accepted"})}, item.OfferedAt)
	if err != nil || decision.Rejection != training.RejectCallRequired {
		t.Fatalf("accept before required call = %+v, %v", decision, err)
	}
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandCallStart, Payload: mustJSON(t, map[string]string{"contact": "unknown"})}, item.OfferedAt)
	if err != nil || decision.Rejection != training.RejectInvalidPayload {
		t.Fatalf("unknown contact = %+v, %v", decision, err)
	}
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandCallStart, Payload: mustJSON(t, map[string]string{"contact": "crew_leader"})}, item.OfferedAt)
	if err != nil || !decision.Accepted || decision.StartCall == nil {
		t.Fatalf("call start = %+v, %v", decision, err)
	}
	call := *decision.StartCall
	call.ID = uuid.New()
	// ADR-031: a finished incoming call from the same contact does not
	// satisfy the required call.
	answered := item.OfferedAt
	item.Calls = []training.Call{{ID: uuid.New(), ContactKey: "crew_leader", Direction: training.CallIncoming, EventKey: "e1", StartedAt: answered, EndedAt: &answered}}
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandSetStatus, Payload: mustJSON(t, map[string]string{"status": "accepted"})}, item.OfferedAt)
	if err != nil || decision.Rejection != training.RejectCallRequired {
		t.Fatalf("accept after only an incoming call = %+v, %v", decision, err)
	}
	item.Calls = []training.Call{call}
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandClose, Payload: []byte(`{}`)}, item.OfferedAt)
	if err != nil || decision.Rejection != training.RejectCallInProgress {
		t.Fatalf("close active call = %+v, %v", decision, err)
	}
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandCallEnd, Payload: mustJSON(t, map[string]any{"call_id": call.ID, "accepted_by": "Иванов", "summary": "Доклад", "recording": nil})}, item.OfferedAt)
	if err != nil || !decision.Accepted || decision.EndCall == nil || decision.EndCall.Recording != nil {
		t.Fatalf("call end null recording = %+v, %v", decision, err)
	}
	now := item.OfferedAt
	item.Calls[0].EndedAt = &now
	decision, err = Exercise.Decide(item, training.Command{Type: training.CommandSetStatus, Payload: mustJSON(t, map[string]string{"status": "accepted"})}, item.OfferedAt)
	if err != nil || !decision.Accepted {
		t.Fatalf("accept after required call = %+v, %v", decision, err)
	}
}

func TestFormatPilotAddressText(t *testing.T) {
	got := formatPilotAddressText(content.Address{
		Country: "Россия", City: "Москва", Okrug: "ЮАО", District: "Чертаново Южное",
		Street: "Чертановская улица", House: "58", Building: "2", Entrance: "2",
	})
	want := "Россия, Москва, (ЮАО, Чертаново Южное), Чертановская улица, 58, к. 2, под. 2"
	if got != want {
		t.Fatalf("formatPilotAddressText = %q, want %q", got, want)
	}
}

func TestFormatPilotAddressTextOmitsEmptyParts(t *testing.T) {
	got := formatPilotAddressText(content.Address{City: "Москва", Street: "ул. Ленина", House: "1"})
	if strings.Contains(got, "()") || strings.HasPrefix(got, ", ") {
		t.Fatalf("formatPilotAddressText produced malformed text: %q", got)
	}
	want := "Москва, ул. Ленина, 1"
	if got != want {
		t.Fatalf("formatPilotAddressText = %q, want %q", got, want)
	}
}
