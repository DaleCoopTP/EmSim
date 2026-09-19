package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestNewCompiles(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatalf("New: %v", err)
	}
}

// TestValidateFileAcceptsExample wraps design-docs/contracts/
// scenario.example.json — the contract's own worked example, kept valid
// by design-docs/contracts/check.py — in a minimal scenario-file
// envelope and checks it validates end to end (scenario-file schema
// pulling in scenario.schema.json via $ref).
func TestValidateFileAcceptsExample(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	exampleBody := readContractJSON(t, "scenario.example.json")
	file := map[string]any{
		"schema":  "emsim/scenario-file/v1",
		"key":     "example",
		"version": 1.0,
		"title":   "Example",
		"origin":  "manual",
		"body":    exampleBody,
	}

	if err := v.ValidateFile(file); err != nil {
		t.Fatalf("ValidateFile(scenario.example.json wrapped) = %v, want nil", err)
	}
}

func TestValidateFileRejectsMissingRequiredField(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	file := map[string]any{
		"schema": "emsim/scenario-file/v1",
		"key":    "example",
		// version, title, origin, body all missing
	}
	if err := v.ValidateFile(file); err == nil {
		t.Fatalf("ValidateFile(missing required fields) should fail")
	}
}

func TestValidateFileRejectsBadKeyPattern(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	exampleBody := readContractJSON(t, "scenario.example.json")
	file := map[string]any{
		"schema": "emsim/scenario-file/v1", "key": "Not Valid Key!", "version": 1.0,
		"title": "t", "origin": "manual", "body": exampleBody,
	}
	if err := v.ValidateFile(file); err == nil {
		t.Fatalf("ValidateFile(invalid key pattern) should fail")
	}
}

func readContractJSON(t *testing.T, name string) any {
	t.Helper()
	root := findRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "design-docs", "contracts", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc
}

// findRepoRoot walks up from the working directory to the first ancestor
// containing go.mod — schema_test.go's package sits two levels under the
// repo root (internal/content/schema).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// TestValidateFileRejectsInvalidBody confirms the scenario-file schema's
// $ref to scenario.schema.json is actually enforced, not just the
// wrapper's own required/pattern checks (TestValidateFileRejectsMissingRequiredField
// and TestValidateFileRejectsBadKeyPattern only exercise the wrapper).
func TestValidateFileRejectsInvalidBody(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body, ok := readContractJSON(t, "scenario.example.json").(map[string]any)
	if !ok {
		t.Fatalf("scenario.example.json did not decode as an object")
	}
	delete(body, "target_service") // required by scenario.schema.json

	file := map[string]any{
		"schema": "emsim/scenario-file/v1", "key": "example", "version": 1.0,
		"title": "t", "origin": "manual", "body": body,
	}
	if err := v.ValidateFile(file); err == nil {
		t.Fatalf("ValidateFile(body missing target_service) should fail")
	}
}
