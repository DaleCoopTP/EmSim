package training

import (
	"strings"
	"testing"
)

// TestValidIntakeCardCountsCharactersNotBytes: field limits are
// characters, as in OpenAPI and the UI — a Cyrillic letter is two UTF-8
// bytes, so a byte count halved every Russian limit (review 2026-09-26,
// item 9).
func TestValidIntakeCardCountsCharactersNotBytes(t *testing.T) {
	card := UnansweredIntakeCard("112-test", "", "", "")
	if !ValidIntakeCard(card) {
		t.Fatal("baseline unanswered card must be valid")
	}
	card.Complaint = IntakeField{State: "known", Value: strings.Repeat("я", 1999)}
	if !ValidIntakeCard(card) {
		t.Fatal("1999-character Russian description rejected")
	}
	card.Complaint.Value = strings.Repeat("я", 2000)
	if ValidIntakeCard(card) {
		t.Fatal("2000-character description accepted")
	}
	card = UnansweredIntakeCard("112-test", "", "", "")
	card.ApplicantName = IntakeField{State: "known", Value: strings.Repeat("я", 1000)}
	if !ValidIntakeCard(card) {
		t.Fatal("1000-character Russian field rejected")
	}
}
