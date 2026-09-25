package operator112

import (
	"strings"

	"emsim/internal/content"
	"emsim/internal/content/normalize"
	"emsim/internal/training"
	trainingintake "emsim/internal/training/operator112"
)

// aiDivergent is 112-6/ADR-026's own AI-caller divergence carve-out: a
// free_text field that disagrees with the closed reference is not
// automatically a trainee error when the model (or the timeout
// fallback) plausibly said something different from the scripted
// canonical answer — the trainee correctly transcribed what they heard,
// the model just phrased the fact differently than the reference
// author expected. Detected when (a) this item ever had a model- or
// fallback-sourced caller turn at all, and (b) the trainee's own filled
// value appears, normalized, somewhere in what the applicant actually
// said. This is deliberately a conservative, global heuristic — it does
// not try to correlate a specific field to a specific turn (transcript
// lines carry no turn number of their own), so it only ever adds
// unavailable/needs_review, never silently forgives a real mistake by
// under-triggering: a false negative here just means an ordinary
// not_met, which manual review can still correct.
func aiDivergent(ev trainingintake.EvidenceBody, intake *content.Intake112, actual string) bool {
	if actual == "" || intake.CallerMode != content.CallerModeFreeText {
		return false
	}
	if !anyModelOrFallbackTurn(ev.IntakeState.CallerTurns) {
		return false
	}
	needle := normalize.Value(actual)
	if needle == "" {
		return false
	}
	for _, line := range ev.IntakeState.Transcript {
		if line.Speaker == "caller" && strings.Contains(normalize.Value(line.Text), needle) {
			return true
		}
	}
	return false
}

func anyModelOrFallbackTurn(turns []training.IntakeCallerTurn) bool {
	for _, t := range turns {
		if t.Source == training.CallerTurnSourceModel || t.Source == training.CallerTurnSourceFallback {
			return true
		}
	}
	return false
}
