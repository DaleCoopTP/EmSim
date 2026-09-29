package dds

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// evidenceValidator compiles design-docs/contracts/evidence.schema.json
// directly from disk — a test-only dependency (this package's own
// binary never validates evidence against the schema at runtime; the
// Go struct tags in internal/training/evidence.go are what keep the
// shape correct, and this test is what catches them drifting apart).
// jsonschema.UnmarshalJSON decodes with json.Number preserved, the same
// requirement content.Canonical has, so reusing the schema library's own
// decoder keeps this consistent with how SealEvidence itself builds its
// document.
func evidenceValidator(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// this file is internal/training/dds/evidence_test.go — four levels
	// under the repo root.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "design-docs", "contracts", "evidence.schema.json"))
	if err != nil {
		t.Fatalf("read evidence.schema.json: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse evidence.schema.json: %v", err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	const id = "https://emsim.local/schemas/evidence/v1"
	if err := c.AddResource(id, doc); err != nil {
		t.Fatalf("register evidence.schema.json: %v", err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatalf("compile evidence.schema.json: %v", err)
	}
	return sch
}

// asCanonicalTree round-trips v through JSON with UseNumber, matching
// what jsonschema.Validate expects (and what content.Canonical itself
// requires) — the same conversion SealEvidence performs internally.
func asCanonicalTree(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		t.Fatalf("decode evidence body: %v", err)
	}
	return tree
}

// closedItemAction is a small helper to build a training.Action for the
// evidence tests below without repeating every field each time.
func closedItemAction(seq, logSeq int64, actorID uuid.UUID, cmdType training.CommandType, payload json.RawMessage, effect map[string]any, accepted bool, rejection training.Rejection, reactionAfter content.Reaction, serverAt time.Time) training.Action {
	return training.Action{
		ID:            uuid.New(),
		Seq:           seq,
		LogSeq:        logSeq,
		ActorID:       actorID,
		Type:          cmdType,
		Payload:       payload,
		Effect:        effect,
		Accepted:      accepted,
		Rejection:     rejection,
		ServerAt:      serverAt,
		ReactionAfter: reactionAfter,
	}
}

// TestEvidenceValidatesAgainstSchema runs a full pilot-2-shaped item
// (open, a rejected transition, a comment-bearing not_accepted, the
// accepted decision, close) through Exercise.Evidence and validates the
// result against evidence.schema.json end to end, then spot-checks the
// derived numbers and the action log's shape.
func TestEvidenceValidatesAgainstSchema(t *testing.T) {
	item := baseItem(t, "ЮАР")
	actorID := item.UserID
	offeredAt := item.OfferedAt

	openedAt := offeredAt.Add(5 * time.Second)
	item.State = training.ItemOpened
	item.OpenedAt = &openedAt
	item.Reaction = content.ReactionReceived

	notAcceptedAt := offeredAt.Add(10 * time.Second)
	primaryAt := notAcceptedAt
	completeAt := primaryAt.Add(180 * time.Second)
	item.State = training.ItemInProgress
	item.Reaction = content.ReactionNotAccepted
	item.PrimaryAt = &primaryAt
	item.Deadlines.CompleteAt = &completeAt

	acceptedAt := offeredAt.Add(20 * time.Second)
	item.Reaction = content.ReactionAccepted

	closedAt := offeredAt.Add(25 * time.Second)
	pilotCompleted := training.ClosePilotCompleted
	item.State = training.ItemClosed
	item.CloseReason = &pilotCompleted

	actions := []training.Action{
		closedItemAction(1, 1, actorID, training.CommandOpen, mustJSON(t, map[string]any{}), nil, true, "", content.ReactionReceived, openedAt),
		// A rejected transition attempt before the trainee settles on
		// not_accepted — must count toward rejected_transitions and be
		// excluded from comments/chain.
		closedItemAction(1, 2, actorID, training.CommandSetStatus, mustJSON(t, map[string]any{"status": "arrived"}), nil, false, training.RejectTransitionNotAllowed, "", notAcceptedAt.Add(-2*time.Second)),
		closedItemAction(2, 3, actorID, training.CommandSetStatus, mustJSON(t, map[string]any{"status": "not_accepted", "comment": "не наша территория, передано в ДДС Северного"}), nil, true, "", content.ReactionNotAccepted, notAcceptedAt),
		closedItemAction(3, 4, actorID, training.CommandSetStatus, mustJSON(t, map[string]any{"status": "accepted"}), nil, true, "", content.ReactionAccepted, acceptedAt),
		closedItemAction(4, 5, actorID, training.CommandClose, mustJSON(t, map[string]any{}), nil, true, "", content.ReactionAccepted, closedAt),
	}

	evidence, err := Exercise.Evidence(item, actions, nil, 5, closedAt)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if evidence.Digest == ([32]byte{}) {
		t.Fatal("evidence digest is zero")
	}

	tree := asCanonicalTree(t, evidence.Body)
	if err := evidenceValidator(t).Validate(tree); err != nil {
		t.Fatalf("evidence does not validate against evidence.schema.json: %v\nbody: %s", err, evidence.Body)
	}

	var body training.EvidenceBody
	if err := json.Unmarshal(evidence.Body, &body); err != nil {
		t.Fatalf("unmarshal sealed evidence body: %v", err)
	}
	if body.CloseReason != training.ClosePilotCompleted {
		t.Fatalf("close_reason = %q, want pilot_completed", body.CloseReason)
	}
	if body.FinalReaction != content.ReactionAccepted {
		t.Fatalf("final_reaction = %q, want accepted", body.FinalReaction)
	}
	if len(body.Actions) != 5 {
		t.Fatalf("actions = %d, want 5", len(body.Actions))
	}
	if body.Derived.RejectedTransitions != 1 {
		t.Fatalf("rejected_transitions = %d, want 1", body.Derived.RejectedTransitions)
	}
	if len(body.Comments) != 1 || body.Comments[0].Text != "не наша территория, передано в ДДС Северного" {
		t.Fatalf("comments = %+v", body.Comments)
	}
	if body.Comments[0].WithStatus == nil || *body.Comments[0].WithStatus != content.ReactionNotAccepted {
		t.Fatalf("comments[0].with_status = %v, want not_accepted", body.Comments[0].WithStatus)
	}
	wantChain := []content.Reaction{content.ReactionNotAccepted, content.ReactionAccepted}
	if len(body.Derived.Chain) != len(wantChain) || body.Derived.Chain[0] != wantChain[0] || body.Derived.Chain[1] != wantChain[1] {
		t.Fatalf("chain = %v, want %v", body.Derived.Chain, wantChain)
	}
	if body.Derived.PrimaryStatus == nil || *body.Derived.PrimaryStatus != content.ReactionNotAccepted {
		t.Fatalf("primary_status = %v, want not_accepted (the first applied decision)", body.Derived.PrimaryStatus)
	}
	if body.Derived.OpenSeconds == nil || *body.Derived.OpenSeconds != 5 {
		t.Fatalf("open_seconds = %v, want 5", body.Derived.OpenSeconds)
	}
	if body.Derived.PrimarySeconds == nil || *body.Derived.PrimarySeconds != 5 {
		t.Fatalf("primary_seconds = %v, want 5 (primary_at - opened_at)", body.Derived.PrimarySeconds)
	}
	if body.Derived.WorkSeconds == nil || *body.Derived.WorkSeconds != 15 {
		t.Fatalf("work_seconds = %v, want 15 (closed_at - primary_at)", body.Derived.WorkSeconds)
	}
	if body.Derived.TotalSeconds != 25 {
		t.Fatalf("total_seconds = %v, want 25", body.Derived.TotalSeconds)
	}
	if body.FinalCard.Address.Okrug != "ЮАР" {
		t.Fatalf("final_card must reflect the item's actual (uncorrected) card, got okrug=%q", body.FinalCard.Address.Okrug)
	}
	if len(body.Events) != 0 || len(body.Calls) != 0 || len(body.Interruptions) != 0 || body.Interruption != nil {
		t.Fatalf("events/calls/interruptions must be empty in slice 3: %+v", body)
	}
}

func TestEvidencePreservesStartedActiveCallOnStop(t *testing.T) {
	item := baseItem(t, "ЮАО")
	closedAt := item.OfferedAt.Add(20 * time.Second)
	interrupted := training.CloseInterrupted
	item.State = training.ItemInterrupted
	item.CloseReason = &interrupted
	item.Calls = []training.Call{{
		ID: uuid.New(), ItemID: item.ID, ContactKey: "crew_leader",
		StartedAt: item.OfferedAt.Add(5 * time.Second), ReactionAtCall: content.ReactionReceived,
		RecordingState: training.RecordingAbsent,
	}}

	evidence, err := Exercise.Evidence(item, nil, nil, 0, closedAt)
	if err != nil {
		t.Fatal(err)
	}
	var body training.EvidenceBody
	if err := json.Unmarshal(evidence.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Calls) != 1 || body.Calls[0].CallID != item.Calls[0].ID || body.Calls[0].EndedAt != nil || body.Calls[0].AcceptedBy != nil || body.Calls[0].Summary != nil {
		t.Fatalf("active call projection = %+v", body.Calls)
	}
}

// ADR-031: a sealed call names its direction; an incoming call keeps the
// phone_incoming event it answered. A call value from before ДДС-2 (no
// direction) is sealed as outgoing.
func TestEvidenceCarriesCallDirection(t *testing.T) {
	item := baseItem(t, "ЮАО")
	closedAt := item.OfferedAt.Add(60 * time.Second)
	interrupted := training.CloseInterrupted
	item.State = training.ItemInterrupted
	item.CloseReason = &interrupted
	outEnd, inEnd := item.OfferedAt.Add(15*time.Second), item.OfferedAt.Add(40*time.Second)
	item.Calls = []training.Call{
		{ID: uuid.New(), ItemID: item.ID, ContactKey: "crew_leader", StartedAt: item.OfferedAt.Add(5 * time.Second), EndedAt: &outEnd,
			ReactionAtCall: content.ReactionAccepted, RecordingState: training.RecordingAbsent},
		{ID: uuid.New(), ItemID: item.ID, ContactKey: "crew_leader", Direction: training.CallIncoming, EventKey: "e2",
			StartedAt: item.OfferedAt.Add(30 * time.Second), EndedAt: &inEnd, ReactionAtCall: content.ReactionResponding, RecordingState: training.RecordingAbsent},
	}

	evidence, err := Exercise.Evidence(item, nil, nil, 0, closedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := evidenceValidator(t).Validate(asCanonicalTree(t, evidence.Body)); err != nil {
		t.Fatalf("evidence does not validate against evidence.schema.json: %v\nbody: %s", err, evidence.Body)
	}
	var body training.EvidenceBody
	if err := json.Unmarshal(evidence.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Calls) != 2 ||
		body.Calls[0].Direction != training.CallOutgoing || body.Calls[0].EventKey != "" ||
		body.Calls[1].Direction != training.CallIncoming || body.Calls[1].EventKey != "e2" {
		t.Fatalf("calls = %+v, want outgoing then incoming e2", body.Calls)
	}
}

// TestEvidenceProjectsEventsAndInterruptions is C6's own coverage: a
// closed item that accumulated a delivered event, a skipped one and a
// restart-recovery marker must carry all three into the sealed evidence,
// validating against evidence.schema.json — not the empty placeholders
// slice 3 shipped.
func TestEvidenceProjectsEventsAndInterruptions(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State = training.ItemClosed
	item.Reaction = content.ReactionAccepted
	pilotCompleted := training.ClosePilotCompleted
	item.CloseReason = &pilotCompleted
	closedAt := item.OfferedAt.Add(10 * time.Second)
	recoveryID := uuid.New()
	detectedAt := item.OfferedAt.Add(3 * time.Second)
	item.Interruptions = []training.Interruption{{RecoveryID: recoveryID, Cause: "server_restart", DetectedAt: detectedAt}}

	deliveredAt := item.OfferedAt.Add(2 * time.Second)
	skipReason := training.SkipReasonItemClosed
	events := []training.ItemEvent{
		{ID: uuid.New(), ItemID: item.ID, EventKey: "e1", AnchorAt: item.OfferedAt, DueAt: deliveredAt, State: training.EventDelivered, DeliveredAt: &deliveredAt, Late: false},
		{ID: uuid.New(), ItemID: item.ID, EventKey: "e2", AnchorAt: item.OfferedAt, DueAt: closedAt.Add(time.Second), State: training.EventSkipped, SkipReason: &skipReason},
	}

	actions := []training.Action{
		closedItemAction(1, 1, item.UserID, training.CommandOpen, mustJSON(t, map[string]any{}), nil, true, "", content.ReactionReceived, item.OfferedAt),
		closedItemAction(2, 2, item.UserID, training.CommandClose, mustJSON(t, map[string]any{}), nil, true, "", content.ReactionAccepted, closedAt),
	}

	evidence, err := Exercise.Evidence(item, actions, events, 2, closedAt)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	tree := asCanonicalTree(t, evidence.Body)
	if err := evidenceValidator(t).Validate(tree); err != nil {
		t.Fatalf("evidence does not validate against evidence.schema.json: %v\nbody: %s", err, evidence.Body)
	}

	var body training.EvidenceBody
	if err := json.Unmarshal(evidence.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Events) != 2 || body.Events[0].Key != "e1" || body.Events[0].State != training.EventDelivered {
		t.Fatalf("events = %+v", body.Events)
	}
	if body.Events[1].Key != "e2" || body.Events[1].State != training.EventSkipped || body.Events[1].SkipReason == nil || *body.Events[1].SkipReason != training.SkipReasonItemClosed {
		t.Fatalf("events[1] = %+v", body.Events[1])
	}
	if len(body.Interruptions) != 1 || body.Interruptions[0].RecoveryID != recoveryID || body.Interruptions[0].Cause != "server_restart" {
		t.Fatalf("interruptions = %+v", body.Interruptions)
	}
}

// TestEvidenceRequiresClosedItem guards Evidence's own precondition: it
// must never be called on an item the application service has not
// already applied a close Decision to.
func TestEvidenceRequiresClosedItem(t *testing.T) {
	item := baseItem(t, "ЮАО")
	if _, err := Exercise.Evidence(item, nil, nil, 0, item.OfferedAt); err == nil {
		t.Fatal("Evidence on a non-closed item should fail")
	}
}

// TestEvidenceExcludesActionsPastCutoff ensures the cutoff really trims
// the action log stored in evidence — a late/rejected command after
// stop_cutoff_log_seq (a later slice's concern) must never appear.
func TestEvidenceExcludesActionsPastCutoff(t *testing.T) {
	item := baseItem(t, "ЮАО")
	item.State = training.ItemClosed
	item.Reaction = content.ReactionAccepted
	pilotCompleted := training.ClosePilotCompleted
	item.CloseReason = &pilotCompleted
	closedAt := item.OfferedAt.Add(10 * time.Second)

	actions := []training.Action{
		closedItemAction(1, 1, item.UserID, training.CommandOpen, mustJSON(t, map[string]any{}), nil, true, "", content.ReactionReceived, item.OfferedAt),
		closedItemAction(2, 2, item.UserID, training.CommandClose, mustJSON(t, map[string]any{}), nil, true, "", content.ReactionAccepted, closedAt),
		// Beyond the cutoff — must not appear in the sealed evidence.
		closedItemAction(2, 3, item.UserID, training.CommandControlReport, mustJSON(t, map[string]any{"text": "поздний комментарий"}), nil, true, "", "", closedAt.Add(time.Second)),
	}

	evidence, err := Exercise.Evidence(item, actions, nil, 2, closedAt)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	var body training.EvidenceBody
	if err := json.Unmarshal(evidence.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Actions) != 2 {
		t.Fatalf("actions = %d, want 2 (cutoff excludes log_seq 3)", len(body.Actions))
	}
}
