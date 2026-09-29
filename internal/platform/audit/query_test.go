package audit

import (
	"testing"
	"time"
)

func TestFilterValidate(t *testing.T) {
	from := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	before := from.Add(-time.Hour)
	tests := []struct {
		name   string
		filter Filter
		ok     bool
	}{
		{"empty", Filter{}, true},
		{"prefix", Filter{ActionPrefix: "admin.user."}, true},
		{"underscore is legal", Filter{ActionPrefix: "auth.login_x"}, true},
		{"uppercase", Filter{ActionPrefix: "Admin."}, false},
		{"quote", Filter{ActionPrefix: "a'b"}, false},
		{"outcome", Filter{Outcome: OutcomeRejected}, true},
		{"unknown outcome", Filter{Outcome: "maybe"}, false},
		{"resource type", Filter{ResourceType: "user"}, true},
		{"bad resource type", Filter{ResourceType: "Us er"}, false},
		{"reversed period", Filter{From: &from, To: &before}, false},
	}
	for _, tt := range tests {
		if err := tt.filter.Validate(); (err == nil) != tt.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}

func TestListRejectsInvalidInput(t *testing.T) {
	if _, err := List(nil, nil, Filter{}, 0, 10); err != ErrInvalidEntry {
		t.Fatalf("nil querier: %v", err)
	}
}
