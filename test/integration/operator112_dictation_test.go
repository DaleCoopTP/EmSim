// 112-8a/ADR-037: dictation against a real PostgreSQL — the pre-recognition
// checks read the item, run and lesson rows, the real whisper client talks
// to a fake whisper-server, and the "voice" mark travels through the
// send_caller_message command into items.intake_state.

//go:build integration

package integration_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"emsim/internal/auth"
	"emsim/internal/platform/stt/whisper"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// whisperTranscriber is cmd/emsim's own adapter (package main, not
// importable): the real whisper.Client behind training.Transcriber.
type whisperTranscriber struct{ client *whisper.Client }

func (w whisperTranscriber) Transcribe(ctx context.Context, wav []byte) (training.Transcript, error) {
	result, err := w.client.Transcribe(ctx, wav)
	return training.Transcript{Text: result.Text, Model: result.Model}, err
}

// dictationWAV is one second of PCM16 mono 16 kHz silence.
func dictationWAV() []byte {
	const samples = 16000
	out := make([]byte, 44+samples*2)
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+samples*2))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1)
	binary.LittleEndian.PutUint16(out[22:], 1)
	binary.LittleEndian.PutUint32(out[24:], 16000)
	binary.LittleEndian.PutUint32(out[28:], 32000)
	binary.LittleEndian.PutUint16(out[32:], 2)
	binary.LittleEndian.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], samples*2)
	return out
}

func TestOperator112DictationThroughRealServiceAndWhisperClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	fixture := setupFreeTextChatItem(t, ctx, databaseURL)

	var received atomic.Int32
	var receivedBytes atomic.Int64
	whisperServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		if file, _, err := r.FormFile("file"); err == nil {
			data, _ := io.ReadAll(file)
			receivedBytes.Store(int64(len(data)))
		}
		received.Add(1)
		_, _ = w.Write([]byte(`{"text":" Что   у вас случилось? "}`))
	}))
	defer whisperServer.Close()
	fixture.service.WithDictation(whisperTranscriber{client: whisper.NewClient(whisperServer.URL, "ru", "ggml-small")},
		training.DictationSettings{MaxSeconds: 30, Timeout: 5 * time.Second, QueueWait: time.Second, Concurrency: 2})
	wav := dictationWAV()

	// The call is still ringing: nothing to dictate into, and the engine
	// is never reached.
	if _, err := fixture.service.Dictate(ctx, fixture.operator, fixture.itemID, wav); !errors.Is(err, training.ErrDictationNotAllowed) {
		t.Fatalf("dictation on a ringing call err = %v, want ErrDictationNotAllowed", err)
	}
	fixture.send(t, ctx, training.CommandOpen, map[string]any{})
	if r := fixture.send(t, ctx, training.CommandAnswerIncoming, map[string]any{}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("answer_incoming: %+v", r)
	}
	if received.Load() != 0 {
		t.Fatal("the engine was called for a call that was not connected")
	}

	before, _, _, err := fixture.service.ItemForTrainee(ctx, fixture.operator, fixture.itemID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Dictate(ctx, fixture.operator, fixture.itemID, wav)
	if err != nil {
		t.Fatalf("Dictate: %v", err)
	}
	if result.Text != "Что у вас случилось?" || result.Model != "ggml-small" {
		t.Fatalf("result = %+v", result)
	}
	if received.Load() != 1 || receivedBytes.Load() != int64(len(wav)) {
		t.Fatalf("engine got %d calls, %d bytes", received.Load(), receivedBytes.Load())
	}
	// Dictation is not a command: it leaves the item, its journal and seq alone.
	after, _, _, err := fixture.service.ItemForTrainee(ctx, fixture.operator, fixture.itemID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Seq != before.Seq || len(after.IntakeState.Transcript) != len(before.IntakeState.Transcript) {
		t.Fatalf("dictation changed the item: seq %d→%d, transcript %d→%d", before.Seq, after.Seq,
			len(before.IntakeState.Transcript), len(after.IntakeState.Transcript))
	}
	var actions int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE item_id=$1`, fixture.itemID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions != 2 { // open + answer_incoming
		t.Fatalf("actions = %d, want 2", actions)
	}

	// Somebody else's item looks like a missing one.
	stranger := auth.Principal{UserID: uuid.New(), Role: auth.RoleTrainee, WorkstationID: fixture.operator.WorkstationID}
	if _, err := fixture.service.Dictate(ctx, stranger, fixture.itemID, wav); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("dictation on a stranger's item err = %v, want ErrNotFound", err)
	}

	// The dictated text is sent as an ordinary message carrying the mark.
	if r := fixture.send(t, ctx, training.CommandSendCallerMessage, map[string]any{"text": result.Text, "input": "voice"}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("send voice message: %+v", r)
	}
	var voice *string
	if err := fixture.pool.QueryRow(ctx, `SELECT intake_state->'transcript'->0->>'input' FROM items WHERE id=$1`, fixture.itemID).Scan(&voice); err != nil {
		t.Fatal(err)
	}
	if voice == nil || *voice != "voice" {
		t.Fatalf("stored input mark = %v, want voice", voice)
	}
	if r := fixture.send(t, ctx, training.CommandEndIncoming, map[string]any{}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("end_incoming: %+v", r)
	}

	// After the call ended dictation is refused again.
	if _, err := fixture.service.Dictate(ctx, fixture.operator, fixture.itemID, wav); !errors.Is(err, training.ErrDictationNotAllowed) {
		t.Fatalf("dictation after the call err = %v, want ErrDictationNotAllowed", err)
	}
}
