package dds

import (
	"encoding/json"
	"testing"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// effectiveRubricV2 is effectiveRubric's own dds/rubric-v2 counterpart
// (ДДС-3/ADR-032): it merges scoring onto the real embedded
// rubric.dds.v2.json, the same call training/service.go's coordinator
// makes for a lesson frozen on v2.
func effectiveRubricV2(t *testing.T, scoring *content.Scoring) assessment.Rubric {
	t.Helper()
	base, err := assessment.LoadDefaultFor(content.ExerciseTypeDDSProcessing)
	if err != nil {
		t.Fatalf("LoadDefaultFor(dds_processing): %v", err)
	}
	if base.Version != "dds/rubric-v2" {
		t.Fatalf("LoadDefaultFor(dds_processing).Version = %q, want dds/rubric-v2", base.Version)
	}
	return assessment.Merge(base, scoring)
}

// crewEvidenceCase is one t_progress/s_sequence_reports/c_calls fixture:
// a closed item's timeline (crew reports it actually heard, and the
// trainee's own set_status reactions to them), evaluated against a
// three-report scenario body modeled on seed/scenarios/
// dds-district-tree-cycle-01-v2.json (ADR-031/032).
type crewEvidenceCase struct {
	// events overrides the scenario's own three report definitions
	// (e1/e2/e3, all expects.action=="set_status") when non-nil; nil
	// keeps threeReportEvents().
	events []content.Event
	// expectedChain overrides reference.expected_chain; nil keeps the
	// full four-status chain.
	expectedChain []content.Reaction
	// evEvents/evCalls/actions build the evidence itself.
	evEvents []training.EvidenceEvent
	evCalls  []training.EvidenceCall
	actions  []statusAction
}

type statusAction struct {
	at     time.Duration // offset from t0
	status content.Reaction
}

func t0() time.Time { return time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC) }

// threeReportEvents mirrors dds-district-tree-cycle-01-v2's own first
// three crew reports (выехали → responding, на месте → arrived,
// приступили → working); its fourth report (работы завершены →
// completed) is left out of the fixtures below on purpose, so
// expected_chain's own trailing "completed" step exercises the
// no-report case (a plain workflow transition, nothing to react to).
func threeReportEvents() []content.Event {
	return []content.Event{
		{Key: "e1", Since: content.EventSinceCallEnded, SinceContact: "crew_leader", Delivery: "notice", From: "crew_leader", Text: "выехали",
			Expects: &content.EventExpects{Status: content.ReactionResponding, WithinS: 30, Action: "set_status"}},
		{Key: "e2", Since: content.EventSinceCallEnded, SinceContact: "crew_leader", Delivery: "phone_incoming", From: "crew_leader", Text: "на месте",
			Expects: &content.EventExpects{Status: content.ReactionArrived, WithinS: 30, Action: "set_status"}},
		{Key: "e3", Since: content.EventSinceCallEnded, SinceContact: "crew_leader", Delivery: "notice", From: "crew_leader", Text: "приступили",
			Expects: &content.EventExpects{Status: content.ReactionWorking, WithinS: 30, Action: "set_status"}},
	}
}

func fullExpectedChain() []content.Reaction {
	return []content.Reaction{content.ReactionResponding, content.ReactionArrived, content.ReactionWorking, content.ReactionCompleted}
}

func crewBody(tc crewEvidenceCase) content.Body {
	events := tc.events
	if events == nil {
		events = threeReportEvents()
	}
	chain := tc.expectedChain
	if chain == nil {
		chain = fullExpectedChain()
	}
	return content.Body{
		ExerciseType: content.ExerciseTypeDDSProcessing,
		Contacts:     []content.Contact{{Key: "crew_leader", Label: "Руководитель бригады", Role: content.ContactRoleCrew}},
		Events:       events,
		Reference:    content.Reference{PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted}, ExpectedChain: chain},
	}
}

func crewEvidence(t *testing.T, tc crewEvidenceCase) training.EvidenceBody {
	t.Helper()
	ev := baseEvidence()
	ev.Events = tc.evEvents
	ev.Calls = tc.evCalls
	var actions []training.EvidenceAction
	// The primary decision itself — always the first set_status, dropped
	// by sequenceReportsRule/derived.Chain the same way v1's own
	// sequenceRule already drops it.
	actions = append(actions, statusEvidenceAction(t, t0().Add(10*time.Second), content.ReactionAccepted))
	for _, a := range tc.actions {
		actions = append(actions, statusEvidenceAction(t, t0().Add(a.at), a.status))
	}
	ev.Actions = actions
	primary := content.ReactionAccepted
	ev.Derived.PrimaryStatus = &primary
	return ev
}

func statusEvidenceAction(t *testing.T, at time.Time, status content.Reaction) training.EvidenceAction {
	t.Helper()
	payload, err := json.Marshal(struct {
		Status content.Reaction `json:"status"`
	}{Status: status})
	if err != nil {
		t.Fatalf("marshal status payload: %v", err)
	}
	return training.EvidenceAction{Type: training.CommandSetStatus, Accepted: true, ServerAt: at, ActionID: uuid.New(), Payload: payload}
}

func evaluateCrew(t *testing.T, tc crewEvidenceCase, rubric assessment.Rubric) []assessment.CriterionResult {
	t.Helper()
	ev := crewEvidence(t, tc)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	results, err := Evaluator.Evaluate(raw, crewBody(tc), rubric, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return results
}

// deliveredNotice is one threeReportEvents() key delivered as a notice
// at t0()+at.
func deliveredNotice(key string, at time.Duration) training.EvidenceEvent {
	deliveredAt := t0().Add(at)
	return training.EvidenceEvent{Key: key, State: training.EventDelivered, DeliveredAt: &deliveredAt}
}

// deliveredIncoming is a phone_incoming event that rang at t0()+ringAt;
// pair it with an EvidenceCall (answeredIncoming) to mark it answered,
// or leave it alone for a missed call.
func deliveredIncoming(key string, ringAt time.Duration) training.EvidenceEvent {
	deliveredAt := t0().Add(ringAt)
	return training.EvidenceEvent{Key: key, State: training.EventDelivered, DeliveredAt: &deliveredAt}
}

func answeredIncoming(key string, at time.Duration) training.EvidenceCall {
	return training.EvidenceCall{CallID: uuid.New(), ContactKey: "crew_leader", Direction: training.CallIncoming, EventKey: key, StartedAt: t0().Add(at)}
}

func skippedEvent(key string) training.EvidenceEvent {
	reason := training.SkipReasonLessonStopped
	return training.EvidenceEvent{Key: key, State: training.EventSkipped, SkipReason: &reason}
}

// TestTProgressOnTime is the fully-correct run: every report answered
// within its own within_s.
func TestTProgressOnTime(t *testing.T) {
	tc := crewEvidenceCase{
		evEvents: []training.EvidenceEvent{deliveredNotice("e1", 15*time.Second), deliveredIncoming("e2", 35*time.Second), deliveredNotice("e3", 60*time.Second)},
		evCalls:  []training.EvidenceCall{answeredIncoming("e2", 40*time.Second)},
		actions: []statusAction{
			{20 * time.Second, content.ReactionResponding},
			{45 * time.Second, content.ReactionArrived},
			{65 * time.Second, content.ReactionWorking},
			{100 * time.Second, content.ReactionCompleted},
		},
	}
	results := evaluateCrew(t, tc, effectiveRubricV2(t, nil))
	if r := findResult(t, results, "T_PROGRESS"); r.Status != assessment.CriterionMet {
		t.Fatalf("T_PROGRESS = %+v, want met", r)
	}
	if r := findResult(t, results, "S_SEQUENCE"); r.Status != assessment.CriterionMet {
		t.Fatalf("S_SEQUENCE = %+v, want met", r)
	}
}

// TestTProgressLateDoesNotFailSequence is ADR-032's own asymmetry: a
// late (but otherwise correctly ordered) status fails T_PROGRESS without
// touching S_SEQUENCE.
func TestTProgressLateDoesNotFailSequence(t *testing.T) {
	tc := crewEvidenceCase{
		evEvents: []training.EvidenceEvent{deliveredNotice("e1", 15*time.Second), deliveredIncoming("e2", 35*time.Second), deliveredNotice("e3", 60*time.Second)},
		evCalls:  []training.EvidenceCall{answeredIncoming("e2", 40*time.Second)},
		actions: []statusAction{
			{20 * time.Second, content.ReactionResponding},
			{75 * time.Second, content.ReactionArrived}, // heard at 40s, deadline 70s — late
			{85 * time.Second, content.ReactionWorking},
			{110 * time.Second, content.ReactionCompleted},
		},
	}
	results := evaluateCrew(t, tc, effectiveRubricV2(t, nil))
	progress := findResult(t, results, "T_PROGRESS")
	if progress.Status != assessment.CriterionPartial || progress.Score == nil || *progress.Score < 0.6 || *progress.Score > 0.7 {
		t.Fatalf("T_PROGRESS = %+v, want partial ~2/3", progress)
	}
	if r := findResult(t, results, "S_SEQUENCE"); r.Status != assessment.CriterionMet {
		t.Fatalf("S_SEQUENCE = %+v, want met (lateness is not a sequence error)", r)
	}
}

// TestEarlyStatusFailsOnlySequence is ADR-032's other half of the same
// asymmetry: a status set before its own crew report still satisfies
// T_PROGRESS (it did reach the target by the deadline). S_SEQUENCE's own
// subsequence match (unchanged from v1's own sequenceRule) consumes
// timeline entries left to right while searching for each expected
// status in turn; an early, rejected "arrived" is never re-set, so the
// search for it consumes the rest of the timeline without a match — the
// same way a status that never appears at all already scores in v1 — and
// every later step (working, completed) fails too, not just this one.
func TestEarlyStatusFailsOnlySequence(t *testing.T) {
	tc := crewEvidenceCase{
		evEvents: []training.EvidenceEvent{deliveredNotice("e1", 15*time.Second), deliveredIncoming("e2", 35*time.Second), deliveredNotice("e3", 60*time.Second)},
		evCalls:  []training.EvidenceCall{answeredIncoming("e2", 40*time.Second)},
		actions: []statusAction{
			{20 * time.Second, content.ReactionResponding},
			{38 * time.Second, content.ReactionArrived}, // heard at 40s — set 2s early
			{65 * time.Second, content.ReactionWorking},
			{100 * time.Second, content.ReactionCompleted},
		},
	}
	results := evaluateCrew(t, tc, effectiveRubricV2(t, nil))
	if r := findResult(t, results, "T_PROGRESS"); r.Status != assessment.CriterionMet {
		t.Fatalf("T_PROGRESS = %+v, want met (early still reaches the target by the deadline)", r)
	}
	seq := findResult(t, results, "S_SEQUENCE")
	if seq.Status != assessment.CriterionPartial || seq.Score == nil || *seq.Score != 0.25 {
		t.Fatalf("S_SEQUENCE = %+v, want partial 1/4 (only responding matched; the rejected early arrived consumes the rest of the search)", seq)
	}
}

// TestMissedIncomingExcludedFromProgressPenalizesCalls is ADR-032's own
// missed-call rule: a phone_incoming report nobody answered is excluded
// from T_PROGRESS/S_SEQUENCE's own denominators (nothing was heard) but
// still counts, unmet, against C_CALLS.
func TestMissedIncomingExcludedFromProgressPenalizesCalls(t *testing.T) {
	tc := crewEvidenceCase{
		evEvents: []training.EvidenceEvent{deliveredNotice("e1", 15*time.Second), deliveredIncoming("e2", 35*time.Second), deliveredNotice("e3", 60*time.Second)},
		// e2 rings but is never answered: no matching EvidenceCall. The
		// trainee never reacts to e1 at all (skips straight from
		// accepted to working, workflow-legal); e3 is answered on time.
		actions: []statusAction{
			{70 * time.Second, content.ReactionWorking}, // e1 (responding, deadline 45s) never reached; e3 (working, deadline 90s) on time
		},
	}
	results := evaluateCrew(t, tc, effectiveRubricV2(t, nil))
	progress := findResult(t, results, "T_PROGRESS")
	// Denominator is 2 (e1, e3) — e2 excluded, not counted as a 3rd
	// applicable point.
	if progress.Status != assessment.CriterionPartial || progress.Score == nil || *progress.Score != 0.5 {
		t.Fatalf("T_PROGRESS = %+v, want partial 1/2 (e2 excluded from the denominator)", progress)
	}
	calls := findResult(t, results, "C_CALLS")
	if calls.Status != assessment.CriterionNotMet {
		t.Fatalf("C_CALLS = %+v, want not_met (the only point — the missed incoming call — was not answered)", calls)
	}
}

// TestCrewNeverCalledFailsProgress is ADR-032's "бригаде не звонили"
// case: the scenario expects three reports, but since_contact's own
// anchor never fired (no item_events row for any of them) because the
// trainee never called crew_leader — every point is a real miss, not an
// exclusion.
func TestCrewNeverCalledFailsProgress(t *testing.T) {
	tc := crewEvidenceCase{evEvents: nil, evCalls: nil, actions: nil}
	results := evaluateCrew(t, tc, effectiveRubricV2(t, nil))
	if r := findResult(t, results, "T_PROGRESS"); r.Status != assessment.CriterionNotMet {
		t.Fatalf("T_PROGRESS = %+v, want not_met (no crew report was ever heard)", r)
	}
}

// TestStopExcludesUnreachedReportsFromProgress is the stop-mid-cycle
// case: e1 was heard and answered correctly before the barrier; e2/e3
// never got delivered (item_events state=skipped, lesson_stopped) — they
// are excluded, not scored as misses.
func TestStopExcludesUnreachedReportsFromProgress(t *testing.T) {
	tc := crewEvidenceCase{
		evEvents: []training.EvidenceEvent{deliveredNotice("e1", 15*time.Second), skippedEvent("e2"), skippedEvent("e3")},
		actions:  []statusAction{{20 * time.Second, content.ReactionResponding}},
	}
	results := evaluateCrew(t, tc, effectiveRubricV2(t, nil))
	progress := findResult(t, results, "T_PROGRESS")
	if progress.Status != assessment.CriterionMet {
		t.Fatalf("T_PROGRESS = %+v, want met (1/1 — e2/e3 excluded by stop)", progress)
	}
}

// TestCorrectlyRejectedCardHasNoApplicableCrewCriteria is ADR-032's own
// "верное «Не принята»" case: a non-profile scenario defines no crew
// report events and no required calls at all, so T_PROGRESS/S_SEQUENCE/
// C_CALLS are all not_applicable rather than penalized misses.
func TestCorrectlyRejectedCardHasNoApplicableCrewCriteria(t *testing.T) {
	tc := crewEvidenceCase{events: []content.Event{}, expectedChain: []content.Reaction{}}
	results := evaluateCrew(t, tc, effectiveRubricV2(t, nil))
	for _, id := range []string{"T_PROGRESS", "S_SEQUENCE", "C_CALLS"} {
		if r := findResult(t, results, id); r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("%s = %+v, want not_applicable", id, r)
		}
	}
}

// TestAmbulanceRefusedProfileIncidentIsCritical is ADR-032's own
// extension of D_PRIMARY's refused_profile_incident rule to 03's
// completed_without_team (it has neither not_accepted nor refused,
// ADR-030) — a profile incident wrongly waved off that way must still
// critical-fail D_PRIMARY.
func TestAmbulanceRefusedProfileIncidentIsCritical(t *testing.T) {
	ev := baseEvidence()
	completedWithoutTeam := content.ReactionCompletedWithoutTeam
	ev.Derived.PrimaryStatus = &completedWithoutTeam
	ref := content.Reference{PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted}}
	rubric := effectiveRubricV2(t, nil)
	r := findResult(t, evaluate(t, ev, ref, rubric), "D_PRIMARY")
	if r.Status != assessment.CriterionNotMet || !r.Critical {
		t.Fatalf("D_PRIMARY = %+v, want not_met and critical", r)
	}
}

// TestCrewCriteriaNotApplicableOnServerInterruption mirrors v1's own
// TestTimingRules server-restart case for the two new deterministic
// rules that read the evidence timeline.
func TestCrewCriteriaNotApplicableOnServerInterruption(t *testing.T) {
	tc := crewEvidenceCase{
		evEvents: []training.EvidenceEvent{deliveredNotice("e1", 15*time.Second)},
		actions:  []statusAction{{20 * time.Second, content.ReactionResponding}},
	}
	ev := crewEvidence(t, tc)
	ev.Interruptions = []training.Interruption{{RecoveryID: uuid.New(), Cause: "server_restart", DetectedAt: ev.ClosedAt}}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	results, err := Evaluator.Evaluate(raw, crewBody(tc), effectiveRubricV2(t, nil), nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, id := range []string{"T_PROGRESS", "S_SEQUENCE"} {
		if r := findResult(t, results, id); r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("%s = %+v, want not_applicable", id, r)
		}
	}
}

// TestCallsRuleCountsRequiredCallAndContacts checks C_CALLS' other two
// point kinds (reference.call and reference.required_contacts) beyond
// crew phone_incoming reports.
func TestCallsRuleCountsRequiredCallAndContacts(t *testing.T) {
	body := content.Body{
		ExerciseType: content.ExerciseTypeDDSProcessing,
		Contacts:     []content.Contact{{Key: "crew_leader", Role: content.ContactRoleCrew}, {Key: "control", Role: content.ContactRoleControl112}},
		Reference: content.Reference{
			PrimaryDecision:  content.PrimaryDecision{Status: content.ReactionAccepted},
			Call:             content.Call{Required: true, To: "crew_leader"},
			RequiredContacts: []string{"control"},
		},
	}
	ev := baseEvidence()
	primary := content.ReactionAccepted
	ev.Derived.PrimaryStatus = &primary
	ended := t0().Add(20 * time.Second)
	ev.Calls = []training.EvidenceCall{{CallID: uuid.New(), ContactKey: "crew_leader", Direction: training.CallOutgoing, StartedAt: t0(), EndedAt: &ended}}
	// control was never called — one of two points fails.
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	results, err := Evaluator.Evaluate(raw, body, effectiveRubricV2(t, nil), nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	r := findResult(t, results, "C_CALLS")
	if r.Status != assessment.CriterionPartial || r.Score == nil || *r.Score != 0.5 {
		t.Fatalf("C_CALLS = %+v, want partial 1/2 (crew_leader called, control not)", r)
	}
}
