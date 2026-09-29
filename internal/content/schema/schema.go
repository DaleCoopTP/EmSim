package schema

import (
	"bytes"
	"fmt"
	"strings"

	"emsim/design-docs/contracts"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
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

// Violation is one leaf JSON-Schema failure: Path is the failing value's
// location as a dotted path (e.g. "body.intake112.call.aon"), Message
// the library's own description of the failed keyword.
type Violation struct {
	Path    string
	Message string
}

// FileViolations validates doc like ValidateFile but returns every leaf
// failure instead of one error — for callers (the operator-112 editor)
// that report problems as a list rather than rejecting the document.
// Duplicate path+message pairs (the same leaf reached through several
// combinator branches) are reported once.
func (v *Validator) FileViolations(doc any) []Violation {
	err := v.file.Validate(doc)
	if err == nil {
		return nil
	}
	root, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []Violation{{Message: err.Error()}}
	}
	var out []Violation
	seen := map[Violation]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		// A failed oneOf/anyOf lists every alternative's own mismatch
		// (e.g. "mode must be 'card_only'" for a full_case body), which
		// is noise rather than the problem: report the combinator once,
		// at its own location, instead of descending into its branches.
		_, oneOf := e.ErrorKind.(*kind.OneOf)
		_, anyOf := e.ErrorKind.(*kind.AnyOf)
		if len(e.Causes) > 0 && !oneOf && !anyOf {
			for _, cause := range e.Causes {
				walk(cause)
			}
			return
		}
		leaf := &jsonschema.ValidationError{SchemaURL: e.SchemaURL, InstanceLocation: e.InstanceLocation, ErrorKind: e.ErrorKind}
		violation := Violation{Path: strings.Join(e.InstanceLocation, "."), Message: leaf.BasicOutput().Error.String()}
		if !seen[violation] {
			seen[violation] = true
			out = append(out, violation)
		}
	}
	walk(root)
	return out
}
