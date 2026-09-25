package normalize

import "testing"

func TestTokenSetEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Тверская улица", "ул. Тверская", true},
		{"Москва", "москва", true},
		{"Ёлкино", "Елкино", true},
		{"Тверская", "Садовая", false},
		{"дом 5", "5 дом", true},
		{"", "", true},
		{"дом 5", "дом 5 ", true},
	}
	for _, c := range cases {
		if got := TokenSetEqual(c.a, c.b); got != c.want {
			t.Errorf("TokenSetEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestEqualIsOrderSensitive(t *testing.T) {
	if Equal("Тверская улица", "улица Тверская") {
		t.Fatal("Equal should be order-sensitive, unlike TokenSetEqual")
	}
	if !Equal("Тверская", "тверская") {
		t.Fatal("Equal should still ignore case")
	}
}

func TestMatches(t *testing.T) {
	if !Matches("Тверская ул.", "улица Тверская", nil) {
		t.Fatal("Matches should accept the expected value itself after normalization")
	}
	if !Matches("2-я Тверская-Ямская", "Тверская-Ямская 2-я", []string{"2-я Тверская-Ямская"}) {
		t.Fatal("Matches should accept a listed alternative")
	}
	if Matches("Садовая", "Тверская", []string{"Ленина"}) {
		t.Fatal("Matches should reject a value matching neither the expected value nor its alternatives")
	}
}
