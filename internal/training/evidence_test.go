package training

import (
	"testing"
	"time"

	"emsim/internal/content"

	"github.com/google/uuid"
)

func sampleEvidenceBody() EvidenceBody {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	return EvidenceBody{
		Schema:            "emsim/evidence/v1",
		ItemID:            uuid.New(),
		RunID:             uuid.New(),
		LessonID:          uuid.New(),
		TraineeID:         uuid.New(),
		WorkstationNo:     5,
		ScenarioVersionID: uuid.New(),
		ScenarioDigest:    "00000000000000000000000000000000000000000000000000000000000000",
		TargetService:     "dds_district",
		Timing:            EvidenceTiming{OpenS: 30, PrimaryS: 30, CompleteS: 180, OpenAnchor: "offered_at", PrimaryAnchor: "offered_at", CompleteAnchor: "primary_at"},
		OfferedAt:         now,
		ClosedAt:          now.Add(90 * time.Second),
		CloseReason:       ClosePilotCompleted,
		FinalReaction:     content.ReactionAccepted,
		Mode:              ModeTraining,
		FinalCard:         content.CardPreview{Number: "1"},
		Actions:           []EvidenceAction{},
		Events:            []EvidenceEvent{},
		Calls:             []any{},
		Derived:           EvidenceDerived{TotalSeconds: 90},
		CutoffLogSeq:      1,
		Deadlines:         EvidenceDeadlines{OpenAt: now.Add(30 * time.Second), PrimaryAt: now.Add(30 * time.Second)},
		ExerciseType:      content.ExerciseTypeDDSProcessing,
		Interruptions:     []Interruption{},
	}
}

func TestSealEvidenceIsDeterministic(t *testing.T) {
	body := sampleEvidenceBody()
	a, err := SealEvidence(body)
	if err != nil {
		t.Fatalf("SealEvidence: %v", err)
	}
	b, err := SealEvidence(body)
	if err != nil {
		t.Fatalf("SealEvidence: %v", err)
	}
	if a.Digest != b.Digest {
		t.Fatalf("SealEvidence is not deterministic: %x vs %x", a.Digest, b.Digest)
	}
	if string(a.Body) != string(b.Body) {
		t.Fatalf("SealEvidence body bytes are not deterministic")
	}
}

func TestSealEvidenceDetectsChange(t *testing.T) {
	body := sampleEvidenceBody()
	a, err := SealEvidence(body)
	if err != nil {
		t.Fatalf("SealEvidence: %v", err)
	}
	body.CloseReason = CloseRefused
	b, err := SealEvidence(body)
	if err != nil {
		t.Fatalf("SealEvidence: %v", err)
	}
	if a.Digest == b.Digest {
		t.Fatal("changing close_reason must change the digest")
	}
}
