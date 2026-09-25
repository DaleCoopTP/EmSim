package aicaller

import (
	"regexp"
	"strings"
	"sync"

	"emsim/internal/content"
)

// patternCache compiles each scenario-authored pattern at most once per
// process. content.Validate already rejects an uncompilable pattern (or
// one containing \b) at import time, so every pattern reaching here is
// expected to compile; compilePattern still fails soft (a pattern that
// somehow doesn't compile simply never matches) rather than panicking a
// worker over bad persisted data.
var patternCache sync.Map // pattern string -> *regexp.Regexp

func compilePattern(pattern string) (*regexp.Regexp, bool) {
	if cached, ok := patternCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), true
	}
	compiled, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, false
	}
	patternCache.Store(pattern, compiled)
	return compiled, true
}

func matchesAny(patterns []string, message string) bool {
	for _, pattern := range patterns {
		if compiled, ok := compilePattern(pattern); ok && compiled.MatchString(message) {
			return true
		}
	}
	return false
}

// FactAsked reports whether message asks about fact: at least one of
// fact.AskPatterns matches and none of fact.AskExcludePatterns does —
// the "address vs. landmark" special case from the local MVP's caller.py
// (a question about a general address must not also count as asking for
// a landmark) is now scenario data instead of a hardcoded fact id
// (slice-112-5b-plan.md's decision 5). A fact with no AskPatterns is
// never detected as asked — a statement-only fact the trainee cannot
// separately probe for.
func FactAsked(fact content.Intake112Fact, message string) bool {
	if !matchesAny(fact.AskPatterns, message) {
		return false
	}
	return !matchesAny(fact.AskExcludePatterns, message)
}

// AskedFacts returns the ids of every fact message asks about, in facts'
// own scenario order (never map iteration order) so callers that build
// deterministic prompt text from this list don't have to re-sort it.
func AskedFacts(facts []content.Intake112Fact, message string) []string {
	var asked []string
	for _, fact := range facts {
		if FactAsked(fact, message) {
			asked = append(asked, fact.ID)
		}
	}
	return asked
}

// DiscloseReveals returns the ids of every currently-open, non-unknown
// fact that reply already states — the caller reply's own Reveals
// (training.IntakeLine.Reveals), the same mechanism the prepared 112-2
// dialogue already uses for a scripted answer's own reveals, computed
// here instead of declared in the scenario since a model/scripted reply's
// exact wording is not fixed in advance. provided_phone/on_site_phone
// compare by digits rather than DisclosurePatterns — caller.py's own
// special case: a phone number can be said with or without a leading
// "+7"/"8" and with arbitrary spacing/punctuation, so a literal regex
// match against it is unreliable. Only facts already in open are
// considered: DiscloseReveals decides what a reply reveals among facts
// the applicant was already allowed to talk about, not whether a reply
// accidentally matches a pattern for a fact never opened.
func DiscloseReveals(facts []content.Intake112Fact, open map[string]bool, reply string) []string {
	var reveals []string
	for _, fact := range facts {
		if !open[fact.ID] || fact.Knowledge == "unknown" {
			continue
		}
		if fact.CardPath == "/provided_phone" || fact.CardPath == "/on_site_phone" {
			if phoneDisclosed(fact.Value, reply) {
				reveals = append(reveals, fact.ID)
			}
			continue
		}
		if matchesAny(fact.DisclosurePatterns, reply) {
			reveals = append(reveals, fact.ID)
		}
	}
	return reveals
}

func phoneDisclosed(factValue, reply string) bool {
	want := digitsOnly(factValue)
	if len(want) > 10 {
		want = want[len(want)-10:]
	}
	if want == "" {
		return false
	}
	return strings.Contains(digitsOnly(reply), want)
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
