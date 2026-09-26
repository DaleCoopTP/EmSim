package content

import (
	"encoding/json"
	"fmt"
	"sync"

	"emsim/design-docs/contracts"
)

// rubricCriterionIDs is the set of criterion ids rubric.default.json
// defines (design-docs/contracts/rubric.default.json, ADR-013) — loaded
// once from the same embedded contracts scenario.schema.json/
// scenario-file.schema.json ride along in (contracts.Files). Validate
// checks a scenario's reference.scoring overrides against this set: a
// case may only re-weight, mark critical, or disable a criterion the
// default rubric actually has.
var rubricCriterionIDs = sync.OnceValues(func() (map[string]bool, error) {
	return loadRubricCriterionIDs("rubric.default.json")
})

// operator112RubricCriterionIDs is rubricCriterionIDs' own counterpart for
// operator112 (112-6/ADR-026, ADR-028) — a scenario's intake112.
// reference.scoring may only re-weight, mark critical, or disable a
// criterion at least one operator112 rubric version actually has, the
// same rule DDS's reference.scoring already follows against rubric.
// default.json. It is the union of v1/v2/v3's own criterion ids, not just
// whichever version a given lesson happens to freeze: a scenario's own
// reference.scoring is authored once and must validate the same way
// regardless of which rubric version a future lesson assigns it to (a
// v2-only id like DESCRIPTION_PRESENT stays valid to disable even after
// v3 replaces it with DESCRIPTION_CONTENT for judge-enabled lessons).
var operator112RubricCriterionIDs = sync.OnceValues(func() (map[string]bool, error) {
	ids := make(map[string]bool)
	for _, filename := range []string{"rubric.operator112.v1.json", "rubric.operator112.json", "rubric.operator112.v3.json"} {
		fileIDs, err := loadRubricCriterionIDs(filename)
		if err != nil {
			return nil, err
		}
		for id := range fileIDs {
			ids[id] = true
		}
	}
	return ids, nil
})

func loadRubricCriterionIDs(name string) (map[string]bool, error) {
	raw, err := contracts.Files.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	var rubric struct {
		Criteria []struct {
			ID string `json:"id"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	ids := make(map[string]bool, len(rubric.Criteria))
	for _, c := range rubric.Criteria {
		ids[c.ID] = true
	}
	return ids, nil
}

// RubricVersion returns rubric.default.json's own "version" string
// (ADR-013) — the value training's lesson creation (slice 3's C4)
// freezes into lessons.rubric_version at start, so a later edit to the
// default rubric cannot retroactively change what an in-progress
// lesson's cards are scored against. Not cached like
// rubricCriterionIDs: it is one string field, not worth a sync.
// OnceValues of its own next to that larger, structurally shaped read.
func RubricVersion() (string, error) {
	return readRubricVersion("rubric.default.json")
}

// RubricVersionFor is RubricVersion's own generalization across
// exercise_type (112-6/ADR-026's c4): operator112_intake's *current*
// rubric version (today "operator112/rubric-v2") is what a newly
// created 112 lesson freezes into lessons.rubric_version, exactly the
// way DDS's own lesson creation already freezes RubricVersion(). An
// existing lesson's own frozen version is never re-derived from this —
// only a new lesson's creation ever calls it.
func RubricVersionFor(exerciseType ExerciseType) (string, error) {
	switch exerciseType {
	case ExerciseTypeDDSProcessing:
		return readRubricVersion("rubric.default.json")
	case ExerciseTypeOperator112Intake:
		return readRubricVersion("rubric.operator112.json")
	}
	return "", fmt.Errorf("content: unsupported exercise_type %q", exerciseType)
}

// Operator112RubricVersion is ADR-028's own version selector for a new
// operator112_intake lesson/preview run — RubricVersionFor's own
// operator112 case (rubric-v2) stays the safe, judge-independent
// default; judgeEnabled=true instead freezes rubric-v3 (adds the LLM
// DESCRIPTION_CONTENT criterion), so a lesson only ever gets a rubric
// version its own deployment can actually score. training.Service is
// the only caller — it derives judgeEnabled from its own process
// configuration (ASSESSMENT_JUDGE), never from "whichever rubric file
// happens to be newest".
func Operator112RubricVersion(judgeEnabled bool) (string, error) {
	if !judgeEnabled {
		return RubricVersionFor(ExerciseTypeOperator112Intake)
	}
	return readRubricVersion("rubric.operator112.v3.json")
}

func readRubricVersion(filename string) (string, error) {
	raw, err := contracts.Files.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filename, err)
	}
	var rubric struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return "", fmt.Errorf("parse %s: %w", filename, err)
	}
	if rubric.Version == "" {
		return "", fmt.Errorf("%s: empty version", filename)
	}
	return rubric.Version, nil
}
