// Package normalize compares free-text field values (address components,
// names, incident descriptions) for equality without being sensitive to
// case, ё/е, punctuation, common address abbreviations, or word order.
// Both internal/content's import-time validation (a scenario's
// expected_card must agree with any dialogue fact sharing the same
// card_path — 112-6/ADR-026, slice-112-6-plan.md's c2) and
// internal/assessment/operator112's deterministic rules (112-6/ADR-026's
// c5/c6 — comparing a trainee's filled card against the scenario's
// reference) use the exact same comparison, so a scenario author and the
// scorer never disagree about what counts as a match.
package normalize

import (
	"strings"
	"unicode"
)

// abbreviations expands the address abbreviations an operator commonly
// types (ADR-026 §2.8) to their full word before comparison, so "ул." and
// "улица" — in either order relative to the street name — compare equal.
var abbreviations = map[string]string{
	"ул":    "улица",
	"пер":   "переулок",
	"просп": "проспект",
	"пркт":  "проспект",
	"бр":    "бульвар",
	"наб":   "набережная",
	"пл":    "площадь",
	"ш":     "шоссе",
	"д":     "дом",
	"корп":  "корпус",
	"стр":   "строение",
	"кв":    "квартира",
	"г":     "город",
	"пос":   "посёлок",
	"дер":   "деревня",
	"мкр":   "микрорайон",
	"окр":   "округ",
}

// Value canonicalizes s: trims, lowercases, folds ё→е, strips punctuation
// (replaced with a space so "ул.Тверская" splits into two tokens), and
// expands known abbreviations token by token. The result is a
// space-joined, order-preserving token sequence — use Equal or
// TokenSetEqual to compare two canonicalized values.
func Value(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	fields := strings.Fields(b.String())
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if full, ok := abbreviations[f]; ok {
			f = full
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

// Equal reports whether a and b canonicalize to the same token sequence,
// in the same order.
func Equal(a, b string) bool {
	return Value(a) == Value(b)
}

// TokenSetEqual reports whether a and b canonicalize to the same
// multiset of tokens regardless of order — "Тверская улица" equals
// "улица Тверская".
func TokenSetEqual(a, b string) bool {
	ta := strings.Fields(Value(a))
	tb := strings.Fields(Value(b))
	if len(ta) != len(tb) {
		return false
	}
	counts := make(map[string]int, len(ta))
	for _, t := range ta {
		counts[t]++
	}
	for _, t := range tb {
		counts[t]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

// Matches reports whether actual equals expected or any of its
// alternatives (scenario.schema.json's intake112.reference.alternatives,
// ADR-026) — always order-independent (TokenSetEqual), since an
// alternative phrasing is exactly the case an operator reorders words in.
func Matches(actual, expected string, alternatives []string) bool {
	if TokenSetEqual(actual, expected) {
		return true
	}
	for _, alt := range alternatives {
		if TokenSetEqual(actual, alt) {
			return true
		}
	}
	return false
}
