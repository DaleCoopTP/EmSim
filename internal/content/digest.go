package content

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Canonical serializes v — a value shaped like DecodeFile's raw return
// (nil, bool, json.Number, string, []any, map[string]any) — into a
// deterministic byte form: object keys sorted, no insignificant
// whitespace, numbers normalized to PostgreSQL jsonb's exact-decimal
// output form (without converting through float64). Two files that differ
// only in key order, formatting, or equivalent numeric notation
// canonicalize identically; Digest is sha256 of this form.
func Canonical(v any) []byte {
	buf := make([]byte, 0, 256)
	buf = appendCanonical(buf, v)
	return buf
}

// Digest is scenario_versions.digest: sha256(Canonical(v)).
func Digest(v any) [32]byte {
	return sha256.Sum256(Canonical(v))
}

func appendCanonical(buf []byte, v any) []byte {
	switch val := v.(type) {
	case nil:
		return append(buf, "null"...)
	case bool:
		if val {
			return append(buf, "true"...)
		}
		return append(buf, "false"...)
	case json.Number:
		return append(buf, normalizeJSONNumber(val.String())...)
	case string:
		return appendCanonicalString(buf, val)
	case []any:
		buf = append(buf, '[')
		for i, e := range val {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendCanonical(buf, e)
		}
		return append(buf, ']')
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf = append(buf, '{')
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendCanonicalString(buf, k)
			buf = append(buf, ':')
			buf = appendCanonical(buf, val[k])
		}
		return append(buf, '}')
	default:
		// DecodeFile never produces any other type; a caller passing
		// something else (e.g. a decoded-without-UseNumber float64) is a
		// programming error, not a data problem this function should mask.
		panic(fmt.Sprintf("content.Canonical: unsupported type %T", v))
	}
}

// normalizeJSONNumber expands exponent notation into the ordinary decimal
// notation PostgreSQL uses when it renders jsonb numbers. It preserves the
// decimal scale that remains after applying the exponent (1.230e-5 becomes
// 0.00001230), including insignificant fractional zeroes, because jsonb's
// underlying numeric value preserves that scale too. Keeping the work on
// strings avoids the precision loss float64 would introduce for large JSON
// integers.
func normalizeJSONNumber(s string) string {
	original := s
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}

	mantissa := s
	exponent := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa = s[:i]
		parsed, err := strconv.Atoi(s[i+1:])
		// PostgreSQL numeric/jsonb cannot store a decimal outside roughly
		// 131k integral and 16k fractional digits. Avoid an unbounded Repeat
		// (or an int overflow) for an exponent the database will reject
		// anyway; retaining the literal lets the later INSERT report that
		// ordinary storage error instead of crashing the importer.
		if err != nil || parsed > 150_000 || parsed < -150_000 {
			// Canonical only receives json.Number values produced by a JSON
			// decoder, so failure here is range rather than syntax.
			return original
		}
		exponent = parsed
	}

	integer, fraction, _ := strings.Cut(mantissa, ".")
	digits := integer + fraction
	decimalPos := len(integer) + exponent

	var whole, decimal string
	switch {
	case decimalPos <= 0:
		whole = "0"
		decimal = strings.Repeat("0", -decimalPos) + digits
	case decimalPos >= len(digits):
		whole = digits + strings.Repeat("0", decimalPos-len(digits))
	default:
		whole = digits[:decimalPos]
		decimal = digits[decimalPos:]
	}

	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	allZero := whole == "0" && strings.Trim(decimal, "0") == ""
	if negative && !allZero {
		whole = "-" + whole
	}
	if decimal == "" {
		return whole
	}
	return whole + "." + decimal
}

// appendCanonicalString reuses encoding/json's own string escaping —
// it already produces the shortest correct JSON string literal, exactly
// what a canonical form wants.
func appendCanonicalString(buf []byte, s string) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		// s is a Go string; json.Marshal of a string cannot fail.
		panic(err)
	}
	return append(buf, b...)
}
