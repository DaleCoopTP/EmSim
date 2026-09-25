// Package aicaller is 112-5b/ADR-025's AI caller adapter: an
// operator112.CallerReplier that answers a free-text caller-chat turn by
// combining a deterministic regex classifier (which facts an operator
// message asks about, and whether a reply already discloses one) with a
// model call for the reply's own wording. It has no state of its own —
// every turn recomputes what the applicant may currently talk about
// from the transcript alone (slice-112-5b-plan.md's decision 1), so a
// retried caller.reply task landing on a different worker produces the
// same input regardless of which worker ran a previous attempt.
package aicaller

import (
	"emsim/internal/content"
	"emsim/internal/training"
)

// OpenFacts returns the ids of facts the applicant may currently talk
// about: every "initial" fact (known from the start, told without being
// asked) plus every fact any operator transcript line has ever asked
// about (per AskedFacts), accumulated across the whole conversation so
// far — including the operator line that started the turn currently
// being answered, since CallerReplyContext reads the transcript after
// that line is already committed. A fact stays open once asked, even if
// a later message stops asking about it; nothing in this port ever
// closes a fact again.
func OpenFacts(facts []content.Intake112Fact, transcript []training.IntakeLine) map[string]bool {
	open := make(map[string]bool, len(facts))
	for _, fact := range facts {
		if fact.Knowledge == "initial" {
			open[fact.ID] = true
		}
	}
	for _, line := range transcript {
		if line.Speaker != "operator" {
			continue
		}
		for _, id := range AskedFacts(facts, line.Text) {
			open[id] = true
		}
	}
	return open
}

// RevealedFacts returns the ids every prior caller transcript line's own
// Reveals already announced — populated by a previous turn's own
// disclosure-pattern detection (DiscloseReveals) or, for the opening
// line, the scenario's own declared Intake112CallerProfile.Opening.Reveals.
// Combined with OpenFacts, this tells prompt.go which open facts the
// applicant still needs to volunteer versus which it has already
// committed to saying — the model must stay consistent with a fact it
// already revealed, not re-decide it differently on a later turn.
func RevealedFacts(transcript []training.IntakeLine) map[string]bool {
	revealed := make(map[string]bool)
	for _, line := range transcript {
		if line.Speaker != "caller" {
			continue
		}
		for _, id := range line.Reveals {
			revealed[id] = true
		}
	}
	return revealed
}

// lastOperatorMessage returns the text of the last operator-spoken
// transcript line — the message this turn is answering. It scans from
// the end rather than assuming the turn's operator line is strictly the
// final transcript entry, since that is a property of the caller
// established by ApplyCallerReply/CallerReplyContext, not one this pure
// package should have to assume about its input.
func lastOperatorMessage(transcript []training.IntakeLine) string {
	for i := len(transcript) - 1; i >= 0; i-- {
		if transcript[i].Speaker == "operator" {
			return transcript[i].Text
		}
	}
	return ""
}
