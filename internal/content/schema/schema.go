// Package schema is internal/content's JSON Schema adapter (CLAUDE.md:
// infrastructure adapters are narrow and separate from domain rules).
// It compiles scenario.schema.json and scenario-file.schema.json once
// from the embedded copies in design-docs/contracts, entirely offline —
// no $ref is ever resolved over the network.
package schema

import (
	"bytes"
	"fmt"

	"emsim/design-docs/contracts"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	scenarioSchemaID     = "https://emsim.local/schemas/scenario/v1"
	scenarioFileSchemaID = "https://emsim.local/schemas/scenario-file/v1"
)

// Validator validates a decoded scenario file document (as
// internal/content.DecodeFile's raw return, or anything shaped the same
// way — json.Number for numbers, map[string]any/[]any, no float64)
// against scenario-file.schema.json, which itself refs scenario.schema.json
// for the "body" property.
type Validator struct {
	file *jsonschema.Schema
}

// New compiles both embedded schemas. It fails only if the embedded
// contracts themselves are malformed — a build-time invariant, not
// something callers need to retry.
func New() (*Validator, error) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()

	for id, name := range map[string]string{
		scenarioSchemaID:     "scenario.schema.json",
		scenarioFileSchemaID: "scenario-file.schema.json",
	} {
		raw, err := contracts.Files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("register %s: %w", name, err)
		}
	}

	sch, err := c.Compile(scenarioFileSchemaID)
	if err != nil {
		return nil, fmt.Errorf("compile scenario-file schema: %w", err)
	}
	return &Validator{file: sch}, nil
}

// ValidateFile validates doc — a full scenario file document, e.g.
// internal/content.DecodeFile's raw return — against
// scenario-file.schema.json + scenario.schema.json. A non-nil error is a
// *jsonschema.ValidationError; callers that need field-level detail can
// walk its Causes, mirroring design-docs/contracts/check.py's own use of
// this library's Python counterpart.
func (v *Validator) ValidateFile(doc any) error {
	return v.file.Validate(doc)
}
