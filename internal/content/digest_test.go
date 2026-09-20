package content

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeAny(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	raw, err := decodeUnique(dec)
	if err != nil {
		t.Fatalf("decodeUnique(%s): %v", s, err)
	}
	return raw
}

func TestCanonicalIgnoresKeyOrderAndWhitespace(t *testing.T) {
	a := decodeAny(t, `{"b": 1, "a": {"y": 2, "x": 1}}`)
	b := decodeAny(t, `  {  "a" :  { "x":1 , "y":2 } , "b":1 }  `)
	if string(Canonical(a)) != string(Canonical(b)) {
		t.Fatalf("Canonical differs for reordered/whitespace-varied input:\n%s\n%s", Canonical(a), Canonical(b))
	}
}

func TestCanonicalPreservesBigIntegerPrecision(t *testing.T) {
	v := decodeAny(t, `{"n": 12345678901234567890}`)
	got := string(Canonical(v))
	want := `{"n":12345678901234567890}`
	if got != want {
		t.Fatalf("Canonical = %s, want %s (float64 would have lost precision)", got, want)
	}
}

func TestCanonicalNormalizesNumbersLikePostgreSQLJSONB(t *testing.T) {
	tests := map[string]string{
		`1e2`:       `100`,
		`1.230e-5`:  `0.00001230`,
		`0.001e1`:   `0.01`,
		`123.4500`:  `123.4500`,
		`-12.5e+2`:  `-1250`,
		`-0.00e+10`: `0`,
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := string(Canonical(decodeAny(t, input))); got != want {
				t.Fatalf("Canonical(%s) = %s, want %s", input, got, want)
			}
		})
	}
}

func TestDigestTreatsEquivalentNumberNotationsIdentically(t *testing.T) {
	for _, pair := range [][2]string{
		{`1e2`, `100`},
		{`1.230e-5`, `0.00001230`},
		{`0.001e1`, `0.01`},
	} {
		if got, want := Digest(decodeAny(t, pair[0])), Digest(decodeAny(t, pair[1])); got != want {
			t.Fatalf("Digest(%s) = %x, want Digest(%s) = %x", pair[0], got, pair[1], want)
		}
	}
}

func TestCanonicalDoesNotExpandUnstorableExponent(t *testing.T) {
	const literal = `1e999999999999999999999999`
	if got := string(Canonical(decodeAny(t, literal))); got != literal {
		t.Fatalf("Canonical(%s) = %s, want the unexpanded literal", literal, got)
	}
}

func TestCanonicalDistinguishesDifferentContent(t *testing.T) {
	a := decodeAny(t, `{"okrug": "ЮАО"}`)
	b := decodeAny(t, `{"okrug": "ЮАР"}`)
	if string(Canonical(a)) == string(Canonical(b)) {
		t.Fatalf("Canonical must differ for different content")
	}
}

func TestDigestStableAndDistinct(t *testing.T) {
	a := decodeAny(t, `{"b":1,"a":2}`)
	b := decodeAny(t, `{"a":2,"b":1}`)
	c := decodeAny(t, `{"a":2,"b":2}`)
	if Digest(a) != Digest(b) {
		t.Fatalf("Digest must be stable under key reordering")
	}
	if Digest(a) == Digest(c) {
		t.Fatalf("Digest must differ for different content")
	}
}
