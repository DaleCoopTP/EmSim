package assessment

import (
	"encoding/json"
	"fmt"
	"sync"

	"emsim/design-docs/contracts"
	"emsim/internal/content"
)

// Rubric is rubric.schema.json's own shape — the type contracts/
// rubric.default.json and a scenario version's reference.scoring merge
// into (ADR-013). Field names mirror the schema; RubricCriterion.Params
// stays a generic map since each rule/prompt interprets its own shape
// (see internal/assessment/dds for the deterministic rules that read it).
type Rubric struct {
	Schema        string               `json:"schema"`
	ID            string               `json:"id"`
	Version       string               `json:"version"`
	PassThreshold float64              `json:"pass_threshold"`
	CriticalCap   float64              `json:"critical_cap"`
	Criteria      []RubricCriterion    `json:"criteria"`
	ExerciseType  content.ExerciseType `json:"exercise_type"`
}

// RubricCriterion is one rubric.schema.json criteria[] entry.
type RubricCriterion struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Kind         string         `json:"kind"` // deterministic | llm
	Weight       float64        `json:"weight"`
	Critical     bool           `json:"critical"`
	CriticalWhen string         `json:"critical_when,omitempty"`
	Rule         string         `json:"rule,omitempty"`
	Prompt       string         `json:"prompt,omitempty"`
	Sources      []string       `json:"sources,omitempty"`
	Params       map[string]any `json:"params"`
}

// ByID returns the criterion with the given id, if the rubric has one.
func (r Rubric) ByID(id string) (RubricCriterion, bool) {
	for _, c := range r.Criteria {
		if c.ID == id {
			return c, true
		}
	}
	return RubricCriterion{}, false
}

// loadedDefault caches contracts.Files' rubric.default.json, decoded
// once — the same sync.OnceValues pattern internal/content/rubric.go
// uses for the same file, kept separate here since this package needs
// the full structure (content's copy only needs criterion ids/version).
var loadedDefault = sync.OnceValues(func() (Rubric, error) {
	raw, err := contracts.Files.ReadFile("rubric.default.json")
	if err != nil {
		return Rubric{}, fmt.Errorf("assessment: read rubric.default.json: %w", err)
	}
	var rubric Rubric
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return Rubric{}, fmt.Errorf("assessment: parse rubric.default.json: %w", err)
	}
	return rubric, nil
})

var loaded112 = sync.OnceValues(func() (Rubric, error) {
	raw, err := contracts.Files.ReadFile("rubric.operator112.json")
	if err != nil {
		return Rubric{}, fmt.Errorf("assessment: read rubric.operator112.json: %w", err)
	}
	var rubric Rubric
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return Rubric{}, fmt.Errorf("assessment: parse rubric.operator112.json: %w", err)
	}
	return rubric, nil
})

func LoadDefaultFor(exerciseType content.ExerciseType) (Rubric, error) {
	if exerciseType == content.ExerciseTypeOperator112Intake {
		return loaded112()
	}
	if exerciseType == content.ExerciseTypeDDSProcessing {
		return loadedDefault()
	}
	return Rubric{}, fmt.Errorf("assessment: unsupported exercise_type %q", exerciseType)
}

// LoadDefault returns the embedded default DDS rubric (ADR-013's layer
// 1). Callers must not mutate the returned value's slices — Merge always
// returns a fresh copy, so this is safe to call repeatedly without
// defensive copying at the call site.
func LoadDefault() (Rubric, error) {
	return loadedDefault()
}

// Merge applies a scenario version's reference.scoring (ADR-013's layer
// 2 — "только отличия") onto the default rubric: weights overrides
// replace a criterion's weight, critical adds criticality on top of the
// default (never removes it — a rubric-critical criterion the case does
// not mention stays critical), and disabled drops a criterion from the
// merged set entirely (Score then treats a disabled id as absent, not
// not_applicable — the caller's evaluator must simply not produce a
// result for it, see Score's own doc comment). A nil scoring is exactly
// the default rubric, copied.
func Merge(base Rubric, scoring *content.Scoring) Rubric {
	merged := base
	merged.Criteria = make([]RubricCriterion, 0, len(base.Criteria))
	if scoring == nil {
		merged.Criteria = append(merged.Criteria, base.Criteria...)
		return merged
	}
	disabled := make(map[string]bool, len(scoring.Disabled))
	for _, id := range scoring.Disabled {
		disabled[id] = true
	}
	critical := make(map[string]bool, len(scoring.Critical))
	for _, id := range scoring.Critical {
		critical[id] = true
	}
	for _, c := range base.Criteria {
		if disabled[c.ID] {
			continue
		}
		if weight, ok := scoring.Weights[c.ID]; ok {
			c.Weight = weight
		}
		if critical[c.ID] {
			c.Critical = true
		}
		merged.Criteria = append(merged.Criteria, c)
	}
	return merged
}
