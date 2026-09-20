package training

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequestDigestIsDeterministic(t *testing.T) {
	actorID, itemID := uuid.New(), uuid.New()
	clientAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	a, err := RequestDigest(actorID, itemID, CommandSetStatus, []byte(`{"status":"accepted","comment":"ok"}`), 3, &clientAt)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	// Same fields, different key order/whitespace in the raw payload —
	// the digest must canonicalize identically (ADR-004).
	b, err := RequestDigest(actorID, itemID, CommandSetStatus, []byte(`{  "comment" : "ok" , "status":"accepted"  }`), 3, &clientAt)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	if a != b {
		t.Fatalf("digests differ for equivalent payloads: %x vs %x", a, b)
	}
}

func TestRequestDigestDetectsPayloadChange(t *testing.T) {
	actorID, itemID := uuid.New(), uuid.New()
	a, err := RequestDigest(actorID, itemID, CommandSetStatus, []byte(`{"status":"accepted"}`), 0, nil)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	b, err := RequestDigest(actorID, itemID, CommandSetStatus, []byte(`{"status":"not_accepted"}`), 0, nil)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	if a == b {
		t.Fatal("different payloads must not collide")
	}
}

func TestRequestDigestDetectsExpectedSeqAndClientAtChange(t *testing.T) {
	actorID, itemID := uuid.New(), uuid.New()
	base, err := RequestDigest(actorID, itemID, CommandOpen, []byte(`{}`), 0, nil)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	seqChanged, err := RequestDigest(actorID, itemID, CommandOpen, []byte(`{}`), 1, nil)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	if base == seqChanged {
		t.Fatal("expected_seq must be part of the digest")
	}
	clientAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	clientAtChanged, err := RequestDigest(actorID, itemID, CommandOpen, []byte(`{}`), 0, &clientAt)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	if base == clientAtChanged {
		t.Fatal("client_at must be part of the digest")
	}
}

func TestRequestDigestDetectsActorOrItemChange(t *testing.T) {
	itemID := uuid.New()
	a, err := RequestDigest(uuid.New(), itemID, CommandOpen, []byte(`{}`), 0, nil)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	b, err := RequestDigest(uuid.New(), itemID, CommandOpen, []byte(`{}`), 0, nil)
	if err != nil {
		t.Fatalf("RequestDigest: %v", err)
	}
	if a == b {
		t.Fatal("different actor_id must not collide")
	}
}

func TestRejectionHTTPStatus(t *testing.T) {
	cases := map[Rejection]int{
		RejectStaleSeq:             409,
		RejectLessonStopped:        409,
		RejectItemClosed:           409,
		RejectTransitionNotAllowed: 422,
		RejectCommentRequired:      422,
		RejectInvalidPayload:       422,
	}
	for rejection, want := range cases {
		if got := rejection.HTTPStatus(); got != want {
			t.Errorf("%s.HTTPStatus() = %d, want %d", rejection, got, want)
		}
	}
}
