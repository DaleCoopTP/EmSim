package content

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// Canonical serializes v — a value shaped like DecodeFile's raw return
// (nil, bool, json.Number, string, []any, map[string]any) — into a
// deterministic byte form: object keys sorted, no insignificant
// whitespace, numbers written exactly as their source literal
// (json.Number, not re-formatted through float64). Two files that differ
// only in key order or formatting canonicalize identically; Digest is
// sha256 of this form.
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
		return append(buf, val.String()...)
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
