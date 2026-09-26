package aicaller

import "emsim/internal/content"

// FactAsked/AskedFacts/DiscloseReveals used to be implemented here; 112-7/
// ADR-027 moved the canonical logic to internal/content
// (intake_classify.go) so the scenario editor's POST
// /scenarios/{id}/probe (content's own endpoint) and this package's
// prompt-building share exactly one implementation — content is the
// lower layer both already depend on, so aicaller cannot be that shared
// home. These three names stay here, unchanged in signature, purely as
// forwarders: every existing call site and test in this package keeps
// working without a rename.

func FactAsked(fact content.Intake112Fact, message string) bool {
	return content.FactAsked(fact, message)
}

func AskedFacts(facts []content.Intake112Fact, message string) []string {
	return content.AskedFacts(facts, message)
}

func DiscloseReveals(facts []content.Intake112Fact, open map[string]bool, reply string) []string {
	return content.DiscloseReveals(facts, open, reply)
}
