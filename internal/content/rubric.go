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
	raw, err := contracts.Files.ReadFile("rubric.default.json")
	if err != nil {
		return nil, fmt.Errorf("read rubric.default.json: %w", err)
	}
	var rubric struct {
		Criteria []struct {
			ID string `json:"id"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return nil, fmt.Errorf("parse rubric.default.json: %w", err)
	}
	ids := make(map[string]bool, len(rubric.Criteria))
	for _, c := range rubric.Criteria {
		ids[c.ID] = true
	}
	return ids, nil
})
