package content

import "fmt"

// Workflow is services.workflow — the per-service card status graph a
// prepared scenario's reference must stay consistent with (Validate).
// The full command/transition machinery training/dds enforces at
// runtime (slice 3) is out of scope here; content only needs enough
// structure to catch an inconsistent scenario at import time: which
// statuses follow which, which require a comment, and which end the
// card.
type Workflow struct {
	Transitions     map[Reaction][]Reaction `json:"transitions"`
	CommentRequired []Reaction              `json:"comment_required"`
	Terminal        []Reaction              `json:"terminal"`
}

// reachableFrom returns every Reaction reachable from start by zero or
// more Transitions hops (start itself included).
func (w Workflow) reachableFrom(start Reaction) map[Reaction]bool {
	seen := map[Reaction]bool{start: true}
	queue := []Reaction{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range w.Transitions[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

// knownStatuses is every Reaction Workflow mentions anywhere — the set a
// reference's before_status fields are checked against, since the schema
// itself only guarantees a syntactically valid Reaction, not one this
// particular service's workflow actually uses.
func (w Workflow) knownStatuses() map[Reaction]bool {
	known := make(map[Reaction]bool)
	for from, tos := range w.Transitions {
		known[from] = true
		for _, to := range tos {
			known[to] = true
		}
	}
	for _, r := range w.CommentRequired {
		known[r] = true
	}
	for _, r := range w.Terminal {
		known[r] = true
	}
	return known
}

// Validate is the import-time check of one service's workflow (ADR-030):
// every status it names is a known Reaction, a terminal status has no
// outgoing transitions (saving it closes the card, so nothing may follow
// it), and every terminal status is reachable from "received". A
// workflow with no terminal statuses — the pilot services of slices 2–7
// (ADR-017) — passes as long as its statuses are valid.
func (w Workflow) Validate() error {
	for status := range w.knownStatuses() {
		if !status.Valid() {
			return invalid("workflow", fmt.Sprintf("unknown_status:%s", status))
		}
	}
	reachable := w.reachableFrom(ReactionReceived)
	for _, terminal := range w.Terminal {
		if len(w.Transitions[terminal]) > 0 {
			return invalid("workflow.terminal", fmt.Sprintf("has_outgoing_transitions:%s", terminal))
		}
		if !reachable[terminal] {
			return invalid("workflow.terminal", fmt.Sprintf("unreachable_from_received:%s", terminal))
		}
	}
	return nil
}

// IsTerminal reports whether saving status closes the card under this
// workflow (ADR-030). Always false for a workflow without terminal
// statuses.
func (w Workflow) IsTerminal(status Reaction) bool {
	for _, t := range w.Terminal {
		if t == status {
			return true
		}
	}
	return false
}
