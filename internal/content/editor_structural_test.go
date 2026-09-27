package content

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"emsim/internal/content/schema"
)

func loadSeedBody(t *testing.T, name string) Body {
	t.Helper()
	raw, err := os.ReadFile("../../seed/scenarios/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	_, body, err := DecodeBody(bytes.NewReader(file.Body))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestStructuralIssuesCatchSchemaViolations: the editor holds a body to
// scenario.schema.json like a file import (review 2026-09-26, item 4) —
// a real seed has no structural issue, while an invalid AON/time/zone
// and an empty fact label are each reported as a blocking error.
func TestStructuralIssuesCatchSchemaViolations(t *testing.T) {
	validator, err := schema.New()
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{schemaValidator: validator}

	// Every editor-eligible seed (full_case + free_text) already passed
	// the schema at import, so its Go round trip must not invent issues.
	entries, err := os.ReadDir("../../seed/scenarios")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		body := loadSeedBody(t, entry.Name())
		if !operator112EditorEligible(body) {
			continue
		}
		checked++
		issues, err := s.structuralIssues(body)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 {
			t.Fatalf("%s: structural issues = %+v, want none", entry.Name(), issues)
		}
	}
	if checked == 0 {
		t.Fatal("no editor-eligible seed found")
	}

	// The web editor always sends a free_text dialogue with an empty
	// initial utterance and questions: [] (content.ts's
	// emptyIntake112Body) — that shape must not be reported either.
	webShaped := loadSeedBody(t, "pilot-112-ai-car-in-water-01-v2.json")
	webShaped.Intake112.Dialogue.Initial = Intake112Utterance{Reveals: []string{}}
	webShaped.Intake112.Dialogue.Questions = []Intake112Question{}
	webShaped.Schema = ""
	if issues, err := s.structuralIssues(webShaped); err != nil || len(issues) != 0 {
		t.Fatalf("web-shaped body: issues = %+v, err = %v; want none", issues, err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*Body)
		path   string
	}{
		{"aon", func(b *Body) { b.Intake112.Call.AON = "invalid" }, "intake112.call.aon"},
		{"aon placeholder", func(b *Body) { b.Intake112.Call.AON = "+7" }, "intake112.call.aon"},
		{"local time", func(b *Body) { b.Intake112.Call.LocalTime = "99:99" }, "intake112.call.local_time"},
		{"time zone", func(b *Body) { b.Intake112.Call.TimeZone = "bad-zone" }, "intake112.call.time_zone"},
		{"fact label", func(b *Body) { b.Intake112.Dialogue.Facts[0].Label = "" }, "intake112.dialogue.facts.0.label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := loadSeedBody(t, "pilot-112-ai-car-in-water-01-v2.json")
			tc.mutate(&broken)
			issues, err := s.structuralIssues(broken)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, issue := range issues {
				if issue.Severity != SeverityError || issue.Code != "schema_violation" {
					t.Fatalf("issue = %+v, want a schema_violation error", issue)
				}
				if issue.Path == tc.path {
					found = true
				}
			}
			if !found {
				t.Fatalf("issues = %+v, want one at %s", issues, tc.path)
			}
		})
	}
}
