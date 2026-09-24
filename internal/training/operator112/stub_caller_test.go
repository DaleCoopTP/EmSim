package operator112

import (
	"context"
	"testing"
	"time"
)

func TestStubCallerReplierSequenceAndRepeat(t *testing.T) {
	r := StubCallerReplier{}
	seen := make([]string, 0, 8)
	for turn := 1; turn <= 8; turn++ {
		reply, err := r.Reply(context.Background(), CallerReplyRequest{Turn: turn})
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		seen = append(seen, reply.Text)
	}
	for i := 0; i < len(stubCallerPhrases); i++ {
		if seen[i] != stubCallerPhrases[i] {
			t.Fatalf("turn %d: got %q, want %q", i+1, seen[i], stubCallerPhrases[i])
		}
	}
	last := stubCallerPhrases[len(stubCallerPhrases)-1]
	for i := len(stubCallerPhrases); i < len(seen); i++ {
		if seen[i] != last {
			t.Fatalf("turn %d past the sixth: got %q, want repeated %q", i+1, seen[i], last)
		}
	}
}

func TestStubCallerReplierTurnBelowOne(t *testing.T) {
	r := StubCallerReplier{}
	reply, err := r.Reply(context.Background(), CallerReplyRequest{Turn: 0})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != stubCallerPhrases[0] {
		t.Fatalf("turn 0: got %q, want the first phrase", reply.Text)
	}
}

func TestStubCallerReplierHonorsContextCancellation(t *testing.T) {
	r := StubCallerReplier{Delay: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Reply(ctx, CallerReplyRequest{Turn: 1}); err == nil {
		t.Fatal("expected the already-cancelled context to abort the reply")
	}
}

func TestStubCallerReplierDelay(t *testing.T) {
	r := StubCallerReplier{Delay: 20 * time.Millisecond}
	start := time.Now()
	if _, err := r.Reply(context.Background(), CallerReplyRequest{Turn: 1}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < r.Delay {
		t.Fatalf("returned after %v, want at least %v", elapsed, r.Delay)
	}
}
