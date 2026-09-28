package dds

import (
	"encoding/json"
	"fmt"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/training"
)

// crewReport is one scenario event with expects.action=="set_status"
// (a crew report the trainee is expected to react to, ADR-031) joined
// against this item's own evidence — everything T_PROGRESS/S_SEQUENCE/
// C_CALLS (ДДС-3/ADR-032) need to know "was this report ever heard, and
// when".
type crewReport struct {
	key      string
	delivery string
	expects  content.EventExpects
	// heardAt is when the trainee could first react to this report:
	// delivered_at for a notice, the answered incoming call's own
	// started_at for a phone_incoming. nil when the report was never
	// heard — see neverCalled/excluded/missedIncoming below.
	heardAt *time.Time
	// neverCalled is true when this report's own since_contact anchor
	// never triggered at all (the trainee never placed the required
	// outgoing call to that contact) — evidence has no item_events row
	// for this event key at all.
	neverCalled bool
	// excluded is true when the report's own item_events row exists but
	// never reached "delivered" — the item closed (by stop or
	// otherwise) before its due_at. Nothing to react to; excluded from
	// every pointer, unlike neverCalled which still counts as a miss.
	excluded bool
	// missedIncoming is true only for a phone_incoming report that did
	// ring (delivered) but was never answered before the ring window
	// closed — training/dds.ReportReactions' own "Missed". It is its
	// own C_CALLS point but excluded from T_PROGRESS/S_SEQUENCE (there
	// is no text to react to).
	missedIncoming bool
}

// crewReports builds one crewReport per body.Events entry with
// expects.action=="set_status", in scenario order.
func crewReports(ev training.EvidenceBody, body content.Body) []crewReport {
	evEvents := make(map[string]training.EvidenceEvent, len(ev.Events))
	for _, e := range ev.Events {
		evEvents[e.Key] = e
	}
	answeredAt := make(map[string]time.Time, len(ev.Calls))
	for _, call := range ev.Calls {
		if !call.Outgoing() && call.EventKey != "" {
			answeredAt[call.EventKey] = call.StartedAt
		}
	}
	var out []crewReport
	for _, e := range body.Events {
		if e.Expects == nil || e.Expects.Action != "set_status" {
			continue
		}
		r := crewReport{key: e.Key, delivery: e.Delivery, expects: *e.Expects}
		evEvt, found := evEvents[e.Key]
		switch {
		case !found:
			r.neverCalled = true
		case evEvt.State == training.EventSkipped:
			r.excluded = true
		case evEvt.State == training.EventDelivered:
			if e.Delivery == "phone_incoming" {
				if at, answered := answeredAt[e.Key]; answered {
					heard := at
					r.heardAt = &heard
				} else {
					r.missedIncoming = true
				}
			} else {
				r.heardAt = evEvt.DeliveredAt
			}
		}
		out = append(out, r)
	}
	return out
}

// statusChange is one accepted set_status action, in the order it was
// applied (ev.Actions is already log_seq-ordered).
type statusChange struct {
	at     time.Time
	status content.Reaction
}

func statusTimeline(ev training.EvidenceBody) []statusChange {
	var out []statusChange
	for _, a := range ev.Actions {
		if !a.Accepted || a.Type != training.CommandSetStatus {
			continue
		}
		var payload struct {
			Status content.Reaction `json:"status"`
		}
		if err := json.Unmarshal(a.Payload, &payload); err != nil {
			continue
		}
		out = append(out, statusChange{at: a.ServerAt, status: payload.Status})
	}
	return out
}

// chainPositions maps each status in chain to its (first) index, so
// "reached expects.status or moved past it" can be read off as a
// position comparison instead of a second workflow walk.
func chainPositions(chain []content.Reaction) map[content.Reaction]int {
	pos := make(map[content.Reaction]int, len(chain))
	for i, s := range chain {
		if _, exists := pos[s]; !exists {
			pos[s] = i
		}
	}
	return pos
}

// statusAtOrBefore returns the last status the timeline reached at or
// before at, if any action happened by then at all.
func statusAtOrBefore(timeline []statusChange, at time.Time) (content.Reaction, bool) {
	var last content.Reaction
	found := false
	for _, sc := range timeline {
		if sc.at.After(at) {
			break
		}
		last, found = sc.status, true
	}
	return last, found
}

// reachedByDeadline is T_PROGRESS's own per-report test: by deadline,
// did the trainee's reaction reach target, or move past it in the
// scenario's own expected_chain order? A status set earlier than its own
// report is still "reached" here — S_SEQUENCE, not T_PROGRESS, is what
// scores a status set out of order (ADR-032).
func reachedByDeadline(timeline []statusChange, positions map[content.Reaction]int, target content.Reaction, deadline time.Time) bool {
	current, ok := statusAtOrBefore(timeline, deadline)
	if !ok {
		return false
	}
	if current == target {
		return true
	}
	curPos, curOK := positions[current]
	targetPos, targetOK := positions[target]
	return curOK && targetOK && curPos >= targetPos
}

// tProgressRule is T_PROGRESS: for every crew report the trainee could
// actually hear, did the reaction (set_status) reach the report's own
// expects.status within expects.within_s of hearing it. A report whose
// own call was never placed (neverCalled) still counts as a miss; a
// report the item closed before delivering, or an unanswered incoming
// call, does not count at all — see crewReport's own field docs.
func tProgressRule(ev training.EvidenceBody, body content.Body, c assessment.RubricCriterion) assessment.CriterionResult {
	if serverInterrupted(ev) {
		return na(c)
	}
	positions := chainPositions(body.Reference.ExpectedChain)
	timeline := statusTimeline(ev)
	total, matched := 0, 0
	for _, r := range crewReports(ev, body) {
		if r.excluded || r.missedIncoming {
			continue
		}
		total++
		if r.neverCalled || r.heardAt == nil {
			continue
		}
		deadline := r.heardAt.Add(time.Duration(r.expects.WithinS) * time.Second)
		if reachedByDeadline(timeline, positions, r.expects.Status, deadline) {
			matched++
		}
	}
	if total == 0 {
		return na(c)
	}
	switch {
	case matched == total:
		return met(c, fmt.Sprintf("%d/%d crew reports got a timely status", matched, total))
	case matched > 0:
		return partial(c, float64(matched)/float64(total), fmt.Sprintf("%d/%d crew reports got a timely status", matched, total))
	default:
		return notMet(c, "no crew report got a timely status")
	}
}

// reportMomentsByStatus is S_SEQUENCE's own lookup from an
// expected_chain status to the earliest moment a report actually
// expecting that status was heard — only heard (non-excluded,
// non-missed, non-never-called) reports count, matching T_PROGRESS'
// own applicability.
func reportMomentsByStatus(reports []crewReport) map[content.Reaction]time.Time {
	out := make(map[content.Reaction]time.Time)
	for _, r := range reports {
		if r.heardAt == nil {
			continue
		}
		if existing, ok := out[r.expects.Status]; !ok || r.heardAt.Before(existing) {
			out[r.expects.Status] = *r.heardAt
		}
	}
	return out
}

// sequenceReportsRule is S_SEQUENCE for dds/rubric-v2: rule name
// s_sequence_reports (kept distinct from v1's own "s_sequence" so a
// scenario's reference.scoring can target either rubric version's own
// criterion by id without touching this dispatch). It keeps v1's own
// subsequence match of reference.expected_chain against the actual
// chain of statuses, and additionally rejects a status set earlier than
// the crew report that was supposed to prompt it — early is a
// sequencing error here, not a timing one (T_PROGRESS already scored it
// met, ADR-032).
func sequenceReportsRule(ev training.EvidenceBody, body content.Body, c assessment.RubricCriterion) assessment.CriterionResult {
	ref := body.Reference
	if len(ref.ExpectedChain) == 0 {
		return na(c)
	}
	if serverInterrupted(ev) {
		return na(c)
	}
	timeline := statusTimeline(ev)
	if len(timeline) > 0 {
		// timeline[0] is the primary decision itself, same as v1's own
		// derived.Chain[0] — expected_chain only describes what comes
		// after it.
		timeline = timeline[1:]
	}
	reportMoments := reportMomentsByStatus(crewReports(ev, body))

	pos, matched := 0, 0
	for _, want := range ref.ExpectedChain {
		for pos < len(timeline) {
			sc := timeline[pos]
			pos++
			if sc.status != want {
				continue
			}
			if at, ok := reportMoments[want]; ok && sc.at.Before(at) {
				// Set before its own report was heard: this occurrence
				// does not satisfy the step: keep scanning forward for a
				// later, valid occurrence of the same status.
				continue
			}
			matched++
			break
		}
	}
	switch {
	case matched == len(ref.ExpectedChain):
		return met(c, "expected chain observed in order, none earlier than its own crew report")
	case matched > 0:
		return partial(c, float64(matched)/float64(len(ref.ExpectedChain)), fmt.Sprintf("%d/%d expected transitions observed in order", matched, len(ref.ExpectedChain)))
	default:
		return notMet(c, "none of the expected transitions were observed in the right order")
	}
}

// completedOutgoingCallTo reports whether ev has a completed (ended)
// outgoing call to contactKey — the same shape completedRequiredCall
// checks for reference.call.to, generalized to an arbitrary contact for
// reference.required_contacts.
func completedOutgoingCallTo(ev training.EvidenceBody, contactKey string) bool {
	for _, call := range ev.Calls {
		if call.Outgoing() && call.ContactKey == contactKey && call.EndedAt != nil {
			return true
		}
	}
	return false
}

// callsRule is C_CALLS (dds/rubric-v2, replaces v1's own C_CALL_MADE):
// the required call (reference.call), every reference.required_contacts
// entry, and an answer to every crew phone_incoming report that actually
// rang — each an independent point, scored as a fraction like
// T_PROGRESS/S_SEQUENCE rather than all-or-nothing, since a scenario can
// carry several independent calls.
func callsRule(ev training.EvidenceBody, body content.Body, c assessment.RubricCriterion) assessment.CriterionResult {
	ref := body.Reference
	total, matched := 0, 0
	if ref.Call.Required {
		total++
		if completedRequiredCall(ev, ref) != nil {
			matched++
		}
	}
	for _, key := range ref.RequiredContacts {
		total++
		if completedOutgoingCallTo(ev, key) {
			matched++
		}
	}
	for _, r := range crewReports(ev, body) {
		if r.delivery != "phone_incoming" {
			continue
		}
		if r.heardAt == nil && !r.missedIncoming {
			// Never rang at all (neverCalled/excluded) — nothing the
			// trainee could have answered.
			continue
		}
		total++
		if r.heardAt != nil {
			matched++
		}
	}
	if total == 0 {
		return na(c)
	}
	switch {
	case matched == total:
		return met(c, fmt.Sprintf("%d/%d required calls completed", matched, total))
	case matched > 0:
		return partial(c, float64(matched)/float64(total), fmt.Sprintf("%d/%d required calls completed", matched, total))
	default:
		return notMet(c, "no required call was completed")
	}
}
