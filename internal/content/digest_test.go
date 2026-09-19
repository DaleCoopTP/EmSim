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

func TestCanonicalPreservesBigIntegerLiteral(t *testing.T) {
	v := decodeAny(t, `{"n": 12345678901234567890}`)
	got := string(Canonical(v))
	want := `{"n":12345678901234567890}`
	if got != want {
		t.Fatalf("Canonical = %s, want %s (float64 would have lost precision)", got, want)
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
