package assessment

import (
	"encoding/json"
	"testing"

	"emsim/internal/content"

	"github.com/google/uuid"
)

func sampleInputBody() InputBody {
	return InputBody{
		Schema:         "emsim/assessment-inputs/v1",
		ItemID:         uuid.MustParse("019230a4-6b1e-7c0a-9a1f-3f2a1b2c3d4e"),
		EvidenceDigest: "9f2c1b5e0a7d4c3b8e6f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d",
		RubricVersion:  "dds/rubric-v1",
		RubricEffective: Rubric{
			Schema: "emsim/rubric/v1", ID: "dds_default", Version: "dds/rubric-v1",
			PassThreshold: 70, CriticalCap: 40, ExerciseType: content.ExerciseTypeDDSProcessing,
			Criteria: []RubricCriterion{{ID: "A", Title: "A", Kind: "deterministic", Weight: 100, Rule: "a", Params: map[string]any{}}},
		},
		Transcripts:   []Transcript{},
		Judge:         Judge{PromptVersions: map[string]string{}, Parameters: map[string]any{}},
		SemanticInput: map[string]any{},
		ExerciseType:  content.ExerciseTypeDDSProcessing,
		RuleResults:   []RuleResult{{ID: "A", Status: CriterionMet}},
	}
}

func TestSealInputMatchesSchemaShape(t *testing.T) {
	body := sampleInputBody()
	canonical, digest, err := SealInput(body)
	if err != nil {
		t.Fatalf("SealInput: %v", err)
	}
	if digest == ([32]byte{}) {
		t.Fatal("digest is zero")
	}
	var decoded map[string]any
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("unmarshal canonical bytes: %v", err)
	}
	for _, field := range []string{"schema", "item_id", "evidence_digest", "rubric_version", "rubric_effective", "transcripts", "judge", "semantic_input", "exercise_type", "rule_results"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("canonical body missing required field %q", field)
		}
	}
	if _, isArray := decoded["transcripts"].([]any); !isArray {
		t.Fatal("transcripts must serialize as an array, not null")
	}
	if _, isArray := decoded["rule_results"].([]any); !isArray {
		t.Fatal("rule_results must serialize as an array, not null")
	}
}

func TestSealInputIsReproducible(t *testing.T) {
	body := sampleInputBody()
	canonical1, digest1, err := SealInput(body)
	if err != nil {
		t.Fatal(err)
	}
	canonical2, digest2, err := SealInput(body)
	if err != nil {
		t.Fatal(err)
	}
	if digest1 != digest2 {
		t.Fatal("SealInput must be deterministic for the same body (ADR-006)")
	}
	if string(canonical1) != string(canonical2) {
		t.Fatal("canonical bytes must be identical for the same body")
	}
}

func TestSealInputDigestChangesWithContent(t *testing.T) {
	body1 := sampleInputBody()
	body2 := sampleInputBody()
	body2.RuleResults = []RuleResult{{ID: "A", Status: CriterionNotMet}}
	_, digest1, err := SealInput(body1)
	if err != nil {
		t.Fatal(err)
	}
	_, digest2, err := SealInput(body2)
	if err != nil {
		t.Fatal(err)
	}
	if digest1 == digest2 {
		t.Fatal("digest must change when rule_results differ")
	}
}
