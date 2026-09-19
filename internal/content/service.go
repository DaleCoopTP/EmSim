package content

import "emsim/internal/content/schema"

// Service implements the content module's slice-2 use cases
// (slice-planning.md §3): importing prepared reference data
// (import.go — ImportServices/ImportClassifierTypes/ImportScenarios)
// and the instructor's read-only catalogue (read.go —
// ListServices/ListScenarios/ScenarioDetail/ScenarioVersions/
// ScenarioPreview), the same one-Service-per-module shape
// internal/auth.Service uses.
type Service struct {
	store           Store
	schemaValidator *schema.Validator
}

// NewService constructs a Service. validator compiles
// scenario-file.schema.json/scenario.schema.json once
// (internal/content/schema.New) — ImportScenarios reuses it for every
// file in a batch rather than recompiling per call.
func NewService(store Store, validator *schema.Validator) *Service {
	return &Service{store: store, schemaValidator: validator}
}
