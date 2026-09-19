package content

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
