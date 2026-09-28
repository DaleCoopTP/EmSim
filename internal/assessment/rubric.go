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
	Kind         string         `json:"kind"` // deterministic | llm | manual | penalty
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

// loadEmbeddedRubric decodes one contracts.Files rubric JSON file. Every
// call site below wraps it in sync.OnceValues so each embedded file is
// parsed at most once per process.
func loadEmbeddedRubric(filename string) (Rubric, error) {
	raw, err := contracts.Files.ReadFile(filename)
	if err != nil {
		return Rubric{}, fmt.Errorf("assessment: read %s: %w", filename, err)
	}
	var rubric Rubric
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return Rubric{}, fmt.Errorf("assessment: parse %s: %w", filename, err)
	}
	return rubric, nil
}

// loadedDefault caches contracts.Files' rubric.default.json, decoded
// once — the same sync.OnceValues pattern internal/content/rubric.go
// uses for the same file, kept separate here since this package needs
// the full structure (content's copy only needs criterion ids/version).
var loadedDefault = sync.OnceValues(func() (Rubric, error) { return loadEmbeddedRubric("rubric.default.json") })

// loadedDDSv2 is dds_processing's own second rubric version (dds/
// rubric-v2, ДДС-3/ADR-032): T_PROGRESS/S_SEQUENCE/C_CALLS replace the
// pilot-only criteria (card editing, call log content, address wording)
// ADR-030 removed from the trainee's own job. loadedDefault (dds/
// rubric-v1) stays embedded and loadable unchanged so a lesson frozen on
// it before this slice keeps scoring against it (ADR-013).
var loadedDDSv2 = sync.OnceValues(func() (Rubric, error) { return loadEmbeddedRubric("rubric.dds.v2.json") })

// loadedDDSv3 is dds/rubric-v3 (ДДС-4/ADR-034) — v2 plus the LLM
// D_COMMENT_CONTENT/G_GRAMMAR criteria, only ever frozen into a lesson
// when its own process is configured with a working judge
// (content.RubricVersionForJudge, training.Service).
var loadedDDSv3 = sync.OnceValues(func() (Rubric, error) { return loadEmbeddedRubric("rubric.dds.v3.json") })

// loaded112v1/loaded112v2 are operator112_intake's own two rubric
// versions (112-6/ADR-026): v1 is the pre-112-6 manual-only rubric,
// preserved unchanged so lessons still running on it are unaffected;
// v2 is the deterministic rubric this slice adds. A lesson freezes
// whichever version was current at its own creation (lessons.
// rubric_version, content.RubricVersionFor) — LoadRubric below is what
// makes that freeze meaningful: an old lesson keeps scoring against v1
// even after v2 ships.
var loaded112v1 = sync.OnceValues(func() (Rubric, error) { return loadEmbeddedRubric("rubric.operator112.v1.json") })
var loaded112v2 = sync.OnceValues(func() (Rubric, error) { return loadEmbeddedRubric("rubric.operator112.json") })

// loaded112v3 is operator112/rubric-v3 (ADR-028) — rubric-v2 plus the
// LLM DESCRIPTION_CONTENT criterion, only ever frozen into a lesson when
// its own process is configured with a working judge (content.
// Operator112RubricVersion(true), training.Service).
var loaded112v3 = sync.OnceValues(func() (Rubric, error) { return loadEmbeddedRubric("rubric.operator112.v3.json") })

// LoadRubric returns exerciseType's own rubric at exactly version —
// never "whatever is current" — so a sealed assessment_inputs snapshot
// or an expert revision on an old item scores against the same rubric
// its lesson actually froze (RFC-001 §6/ADR-013), not one a later
// deploy happened to ship. version must be one of the exact strings
// rubric.schema.json's own "version" field uses (e.g.
// "operator112/rubric-v2") — an unknown version is an error, not a
// silent fallback to "current".
func LoadRubric(exerciseType content.ExerciseType, version string) (Rubric, error) {
	switch exerciseType {
	case content.ExerciseTypeDDSProcessing:
		switch version {
		case "dds/rubric-v1":
			return loadedDefault()
		case "dds/rubric-v2":
			return loadedDDSv2()
		case "dds/rubric-v3":
			return loadedDDSv3()
		}
	case content.ExerciseTypeOperator112Intake:
		switch version {
		case "operator112/rubric-v1":
			return loaded112v1()
		case "operator112/rubric-v2":
			return loaded112v2()
		case "operator112/rubric-v3":
			return loaded112v3()
		}
	}
	return Rubric{}, fmt.Errorf("assessment: no rubric for exercise_type %q version %q", exerciseType, version)
}

// LoadDefaultFor returns exerciseType's own *current* rubric — the one
// content.RubricVersionFor freezes into a newly created lesson. It is
// never the right call for scoring an existing item (use LoadRubric with
// that item's own frozen rubric_version instead); it exists for the
// handful of callers that genuinely want "today's rubric" — currently
// none inside this module (kept for parity with content.RubricVersion's
// own "current" convention and for tests).
func LoadDefaultFor(exerciseType content.ExerciseType) (Rubric, error) {
	if exerciseType == content.ExerciseTypeOperator112Intake {
		return loaded112v2()
	}
	if exerciseType == content.ExerciseTypeDDSProcessing {
		return loadedDDSv2()
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

// ScoringFor extracts a scenario version's own reference.scoring
// override (ADR-013's "только отличия" layer) regardless of
// exercise_type: DDS keeps it on body.Reference.Scoring, operator112
// (112-6/ADR-026) keeps its own copy nested under body.Intake112.
// Reference.Scoring instead, since the entire 112 reference lives there.
// Merge accepts the result either way.
func ScoringFor(body content.Body) *content.Scoring {
	if body.Intake112 != nil {
		return body.Intake112.Reference.Scoring
	}
	return body.Reference.Scoring
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
