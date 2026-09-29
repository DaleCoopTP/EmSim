package training

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"emsim/internal/content"
)

// testWAV builds a canonical 44-byte-header WAV of the given format.
func testWAV(rate uint32, channels, bits uint16, samples int) []byte {
	data := make([]byte, samples*int(bits/8)*int(channels))
	out := make([]byte, 44, 44+len(data))
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(data)))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1)
	binary.LittleEndian.PutUint16(out[22:], channels)
	binary.LittleEndian.PutUint32(out[24:], rate)
	binary.LittleEndian.PutUint32(out[28:], rate*uint32(channels)*uint32(bits/8))
	binary.LittleEndian.PutUint16(out[32:], channels*bits/8)
	binary.LittleEndian.PutUint16(out[34:], bits)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(data)))
	return append(out, data...)
}

func TestCheckDictationWAV(t *testing.T) {
	cases := []struct {
		name   string
		wav    []byte
		reason string
	}{
		{"ok", testWAV(16000, 1, 16, 16000), ""},
		{"not wav", []byte("OggS....not a wav file"), "not_wav"},
		{"stereo", testWAV(16000, 2, 16, 16000), "unsupported_format"},
		{"44 kHz", testWAV(44100, 1, 16, 44100), "unsupported_format"},
		{"8 bit", testWAV(16000, 1, 8, 16000), "unsupported_format"},
		{"too short", testWAV(16000, 1, 16, 1000), "too_short"},
		{"too long", testWAV(16000, 1, 16, 16000*31), "too_long"},
	}
	for _, c := range cases {
		_, err := checkDictationWAV(c.wav, 30)
		if c.reason == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Reason != c.reason {
			t.Errorf("%s: err = %v, want reason %q", c.name, err, c.reason)
		}
	}
}

func TestDictationAllowed(t *testing.T) {
	open := func(callStatus, callerMode string) Item {
		return Item{ExerciseType: content.ExerciseTypeOperator112Intake, State: ItemInProgress,
			IntakeState: &IntakeState{Mode: "full_case", CallerMode: callerMode, CallStatus: callStatus}}
	}
	running := Lesson{State: LessonRunning}
	if !dictationAllowed(running, open("connected", content.CallerModeFreeText)) {
		t.Fatal("connected free_text chat must be allowed")
	}
	closed := open("connected", content.CallerModeFreeText)
	closed.State = ItemClosed
	cutoff := int64(3)
	barrier := open("connected", content.CallerModeFreeText)
	barrier.StopCutoffLogSeq = &cutoff
	prepared := open("connected", content.CallerModePrepared)
	cardOnly := open("connected", content.CallerModeFreeText)
	cardOnly.IntakeState.Mode = "card_only"
	dds := open("connected", content.CallerModeFreeText)
	dds.ExerciseType = content.ExerciseTypeDDSProcessing
	for name, tc := range map[string]struct {
		lesson Lesson
		item   Item
	}{
		"held":     {running, open("held", content.CallerModeFreeText)},
		"ended":    {running, open("ended", content.CallerModeFreeText)},
		"closed":   {running, closed},
		"barrier":  {running, barrier},
		"prepared": {running, prepared},
		"cardOnly": {running, cardOnly},
		"dds":      {running, dds},
		"stopped":  {Lesson{State: LessonStopped}, open("connected", content.CallerModeFreeText)},
	} {
		if dictationAllowed(tc.lesson, tc.item) {
			t.Errorf("%s: must not be allowed", name)
		}
	}
}

type gateTranscriber struct {
	release chan struct{}
	err     error
}

func (g gateTranscriber) Transcribe(ctx context.Context, wav []byte) (Transcript, error) {
	select {
	case <-g.release:
	case <-ctx.Done():
		return Transcript{}, ctx.Err()
	}
	return Transcript{Text: "ok", Model: "gate"}, g.err
}

func TestDictationRuntimeBusyAndTimeout(t *testing.T) {
	wav := testWAV(16000, 1, 16, 16000)
	gate := gateTranscriber{release: make(chan struct{})}
	service := (&Service{}).WithDictation(gate, DictationSettings{Concurrency: 1, QueueWait: 30 * time.Millisecond, Timeout: time.Second})
	first := make(chan error, 1)
	go func() {
		_, err := service.dictation.run(context.Background(), wav)
		first <- err
	}()
	time.Sleep(50 * time.Millisecond) // the first call holds the only slot
	if _, err := service.dictation.run(context.Background(), wav); !errors.Is(err, ErrDictationBusy) {
		t.Fatalf("second call err = %v, want ErrDictationBusy", err)
	}
	close(gate.release)
	if err := <-first; err != nil {
		t.Fatalf("first call: %v", err)
	}

	slow := (&Service{}).WithDictation(gateTranscriber{release: make(chan struct{})}, DictationSettings{Timeout: 30 * time.Millisecond})
	if _, err := slow.dictation.run(context.Background(), wav); !errors.Is(err, ErrDictationUnavailable) {
		t.Fatalf("timeout err = %v, want ErrDictationUnavailable", err)
	}
	failing := (&Service{}).WithDictation(gateTranscriber{release: func() chan struct{} { c := make(chan struct{}); close(c); return c }(), err: errors.New("engine down")}, DictationSettings{})
	if _, err := failing.dictation.run(context.Background(), wav); !errors.Is(err, ErrDictationUnavailable) {
		t.Fatalf("engine error = %v, want ErrDictationUnavailable", err)
	}
}

func TestDictationDisabledAndStub(t *testing.T) {
	service := &Service{}
	if service.DictationInfo().Available {
		t.Fatal("dictation must be off without an engine")
	}
	if service.DictationOffered(Item{}) {
		t.Fatal("nothing is offered while disabled")
	}
	service.WithDictation(StubTranscriber{Text: "привет"}, DictationSettings{MaxSeconds: 20})
	if info := service.DictationInfo(); !info.Available || info.MaxSeconds != 20 {
		t.Fatalf("info = %+v", info)
	}
	result, err := service.dictation.run(context.Background(), testWAV(16000, 1, 16, 8000))
	if err != nil || result.Text != "привет" || result.Model != "stub" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}
