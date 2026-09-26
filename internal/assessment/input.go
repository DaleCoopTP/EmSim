package assessment

import (
	"bytes"
	"encoding/json"
	"fmt"

	"emsim/internal/content"

	"github.com/google/uuid"
)

// InputBody is assessment-inputs.schema.json's own shape (ADR-006's
// single sealed input: rules, rubric, transcripts-with-origin, and the
// judge's exact prepared input, all in one document). Transcripts and
// RuleResults must be non-nil (possibly empty) slices when sealed —
// json.Marshal would otherwise emit "null" for them, which the schema's
// "type":"array" rejects; the coordinator (service.go) is responsible
// for always constructing them that way, never leaving them nil.
type InputBody struct {
	Schema          string               `json:"schema"`
	ItemID          uuid.UUID            `json:"item_id"`
	EvidenceDigest  string               `json:"evidence_digest"` // hex sha256
	RubricVersion   string               `json:"rubric_version"`
	RubricEffective Rubric               `json:"rubric_effective"`
	Transcripts     []Transcript         `json:"transcripts"`
	Judge           Judge                `json:"judge"`
	SemanticInput   map[string]any       `json:"semantic_input"`
	ExerciseType    content.ExerciseType `json:"exercise_type"`
	RuleResults     []RuleResult         `json:"rule_results"`
}

// Transcript is one assessment-inputs.schema.json transcripts[] entry —
// slice 9 populates state=ready with the actual text; slice 6 never
// produces one at all (DDS pilots' calls are covered by C_CALL_MADE/
// C_CALL_LOG's deterministic rules over the call record itself, not its
// transcript).
type Transcript struct {
	CallID           uuid.UUID      `json:"call_id"`
	RecordingSHA256  *string        `json:"recording_sha256"`
	State            string         `json:"state"` // ready | missing | failed
	TranscriptDigest string         `json:"transcript_digest,omitempty"`
	Reason           string         `json:"reason,omitempty"`
	Text             string         `json:"text,omitempty"`
	SourceTaskID     *uuid.UUID     `json:"source_task_id,omitempty"`
	Model            string         `json:"model,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
}

// Judge is assessment-inputs.schema.json's judge object — model=nil for
// dds_processing (JUDGE=off, ADR-013) and for any operator112_intake
// lesson with no judge configured (ASSESSMENT_JUDGE=off) or nothing for
// the judge to answer. ADR-028 fills Model/PromptVersions/Parameters
// from Service.judge (JudgeConfig) whenever a SemanticPreparer actually
// prepared something to seal into InputBody.SemanticInput.
type Judge struct {
	Model          *string           `json:"model"`
	PromptVersions map[string]string `json:"prompt_versions"`
	Parameters     map[string]any    `json:"parameters"`
}

// RuleResult is one assessment-inputs.schema.json rule_results[] entry —
// the id/status half of a CriterionResult, without the display-only
// Score/Weight/EvidenceRefs/Explanation fields (those live only in the
// assessments row Score/RecordAuto produce from the same evaluator pass).
type RuleResult struct {
	ID     string          `json:"id"`
	Status CriterionStatus `json:"status"`
}

// SealInput canonicalizes body into the exact bytes assessment_inputs.
// digest hashes — the same canonicalization convention
// training.SealEvidence uses (content.Canonical/Digest over a
// UseNumber-decoded tree), so a sealed input is byte-for-byte
// reproducible from the same evidence and rules (ADR-006).
func SealInput(body InputBody) (canonical []byte, digest [32]byte, err error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("assessment: marshal input body: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, [32]byte{}, fmt.Errorf("assessment: decode input body for canonicalization: %w", err)
	}
	return content.Canonical(tree), content.Digest(tree), nil
}
