package config

import "time"

// maxCallerOpeningDelay bounds CALLER_OPENING_DELAY: the delay only buys
// the prompt-cache warm-up a head start, and a longer silence after the
// operator's first line would itself look like a broken call.
const maxCallerOpeningDelay = 10 * time.Second

// callerTimingFromEnvironment reads the two AI caller settings both
// processes need (ADR-029): CALLER_WARMUP (default true) — api enqueues
// the warm-ups, the worker runs them — and CALLER_OPENING_DELAY
// (default 0) — api holds the opening back, bench-llm reproduces it.
func callerTimingFromEnvironment(lookup func(string) string) (warmup bool, openingDelay time.Duration, ok bool) {
	warmup, err := parseBoolOrDefault(lookup("CALLER_WARMUP"), true)
	if err != nil {
		return false, 0, false
	}
	openingDelay, err = parseDurationOrDefault(lookup("CALLER_OPENING_DELAY"), 0)
	if err != nil || openingDelay < 0 || openingDelay > maxCallerOpeningDelay {
		return false, 0, false
	}
	return warmup, openingDelay, true
}
