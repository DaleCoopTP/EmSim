package training

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"time"

	"emsim/internal/auth"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"emsim/internal/content"
)

// Dictation (ADR-037) turns an operator 112's spoken phrase into text
// for the caller-chat input. It is not a command: it changes no item
// state, journal or evidence, and neither the audio nor the recognised
// text is stored or logged. The engine is a Transcriber port; the
// composition (cmd/emsim) supplies whisper-server or the stub.

var (
	// ErrDictationUnavailable: no engine configured, or the engine
	// failed/timed out — the operator keeps typing.
	ErrDictationUnavailable = errors.New("training: dictation unavailable")
	// ErrDictationBusy: every recognition slot stayed taken for
	// DictationSettings.QueueWait.
	ErrDictationBusy = errors.New("training: dictation busy")
	// ErrDictationNotAllowed: the item is not a connected free_text
	// operator-112 call in a running lesson.
	ErrDictationNotAllowed = errors.New("training: dictation not allowed")
)

// Transcript is one recognition result. Model is a label of the engine
// that produced it.
type Transcript struct {
	Text  string
	Model string
}

// Transcriber recognises one WAV (PCM16, 16 kHz, mono) phrase.
type Transcriber interface {
	Transcribe(ctx context.Context, wav []byte) (Transcript, error)
}

// StubTranscriber returns a fixed phrase without any engine, for e2e and
// offline development (DICTATION=stub).
type StubTranscriber struct{ Text string }

func (s StubTranscriber) Transcribe(context.Context, []byte) (Transcript, error) {
	return Transcript{Text: s.Text, Model: "stub"}, nil
}

// DictationSettings bound one recognition. Zero values are replaced by
// ADR-037's defaults in WithDictation.
type DictationSettings struct {
	MaxSeconds  int
	Timeout     time.Duration
	QueueWait   time.Duration
	Concurrency int
}

// DictationInfo is what an item projection tells the client.
type DictationInfo struct {
	Available  bool
	MaxSeconds int
}

// DictationResult is a successful recognition.
type DictationResult struct {
	Text       string
	Model      string
	DurationMS int64
}

type dictationRuntime struct {
	transcriber Transcriber
	settings    DictationSettings
	slots       chan struct{}
}

// WithDictation enables dictation with t; a nil t leaves it disabled.
func (s *Service) WithDictation(t Transcriber, settings DictationSettings) *Service {
	if t == nil {
		s.dictation = nil
		return s
	}
	if settings.MaxSeconds <= 0 {
		settings.MaxSeconds = 30
	}
	if settings.Timeout <= 0 {
		settings.Timeout = 15 * time.Second
	}
	if settings.Concurrency <= 0 {
		settings.Concurrency = 2
	}
	s.dictation = &dictationRuntime{transcriber: t, settings: settings, slots: make(chan struct{}, settings.Concurrency)}
	return s
}

// DictationInfo reports whether dictation is enabled at all.
func (s *Service) DictationInfo() DictationInfo {
	if s.dictation == nil {
		return DictationInfo{}
	}
	return DictationInfo{Available: true, MaxSeconds: s.dictation.settings.MaxSeconds}
}

// DictationOffered reports whether the client should show the microphone
// for item: dictation is enabled and the item is a free_text 112 chat.
func (s *Service) DictationOffered(item Item) bool {
	return s.dictation != nil && isFreeTextChat(item)
}

func isFreeTextChat(item Item) bool {
	return item.ExerciseType == content.ExerciseTypeOperator112Intake && item.IntakeState != nil &&
		item.IntakeState.Mode == "full_case" && item.IntakeState.CallerMode == content.CallerModeFreeText
}

// dictationAllowed is the pre-recognition check (ADR-037): the item is a
// free_text chat with a connected call, still open, in a running lesson.
func dictationAllowed(lesson Lesson, item Item) bool {
	return lesson.State == LessonRunning && isFreeTextChat(item) &&
		item.IntakeState.CallStatus == "connected" &&
		item.State != ItemClosed && item.State != ItemInterrupted && item.StopCutoffLogSeq == nil
}

// Dictate recognises wav for actor's own item. It reads the item without
// locks and holds no transaction while the engine runs.
func (s *Service) Dictate(ctx context.Context, actor auth.Principal, itemID uuid.UUID, wav []byte) (DictationResult, error) {
	if s.dictation == nil {
		return DictationResult{}, ErrDictationUnavailable
	}
	var allowed bool
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		item, err := s.store.ItemByID(ctx, tx, itemID, LockNone)
		if err != nil {
			return err
		}
		run, err := s.store.RunByID(ctx, tx, item.RunID, LockNone)
		if err != nil {
			return err
		}
		if run.UserID != actor.UserID {
			return ErrNotFound
		}
		if !workstationMatches(actor, run) {
			return ErrWorkstationMismatch
		}
		lesson, err := s.store.LessonByID(ctx, tx, run.LessonID, LockNone)
		if err != nil {
			return err
		}
		allowed = dictationAllowed(lesson, item)
		return nil
	})
	if err != nil {
		return DictationResult{}, err
	}
	if !allowed {
		return DictationResult{}, ErrDictationNotAllowed
	}
	return s.dictation.run(ctx, wav)
}

// run validates wav, takes a slot and calls the engine.
func (d *dictationRuntime) run(ctx context.Context, wav []byte) (DictationResult, error) {
	duration, err := checkDictationWAV(wav, d.settings.MaxSeconds)
	if err != nil {
		return DictationResult{}, err
	}
	// A free slot is taken without touching the timer, so QueueWait=0
	// means "never wait" rather than a coin flip between two ready cases.
	select {
	case d.slots <- struct{}{}:
	default:
		wait := time.NewTimer(d.settings.QueueWait)
		defer wait.Stop()
		select {
		case d.slots <- struct{}{}:
		case <-wait.C:
			slog.WarnContext(ctx, "dictation", "outcome", "busy")
			return DictationResult{}, ErrDictationBusy
		case <-ctx.Done():
			return DictationResult{}, ctx.Err()
		}
	}
	defer func() { <-d.slots }()

	callCtx, cancel := context.WithTimeout(ctx, d.settings.Timeout)
	defer cancel()
	started := time.Now()
	transcript, err := d.transcriber.Transcribe(callCtx, wav)
	elapsed := time.Since(started)
	if err != nil {
		if ctx.Err() != nil {
			return DictationResult{}, ctx.Err()
		}
		// Neither the audio nor the engine's error text is logged
		// (ADR-037): only the outcome and timings.
		slog.WarnContext(ctx, "dictation", "outcome", "engine_failed", "audio_ms", duration.Milliseconds(), "latency_ms", elapsed.Milliseconds())
		return DictationResult{}, ErrDictationUnavailable
	}
	slog.InfoContext(ctx, "dictation", "outcome", "ok", "audio_ms", duration.Milliseconds(), "latency_ms", elapsed.Milliseconds())
	return DictationResult{Text: transcript.Text, Model: transcript.Model, DurationMS: elapsed.Milliseconds()}, nil
}

const (
	dictationSampleRate  = 16000
	dictationMinDuration = 300 * time.Millisecond
)

// MaxDictationBytes is the largest WAV Dictate accepts for maxSeconds:
// PCM16 mono 16 kHz plus a generous header allowance.
func MaxDictationBytes(maxSeconds int) int64 {
	return int64(maxSeconds)*dictationSampleRate*2 + 4096
}

// checkDictationWAV parses a RIFF/WAVE file and requires PCM16, mono,
// 16 kHz; it returns the audio duration. Failures are ValidationErrors
// (HTTP 422); the reason is a fixed word, never file content.
func checkDictationWAV(wav []byte, maxSeconds int) (time.Duration, error) {
	if len(wav) < 12 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return 0, validationErr("audio", "not_wav")
	}
	var (
		haveFmt  bool
		dataSize int
		haveData bool
	)
	for pos := 12; pos+8 <= len(wav); {
		id := string(wav[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(wav[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			if size < 16 || body+16 > len(wav) {
				return 0, validationErr("audio", "not_wav")
			}
			format := binary.LittleEndian.Uint16(wav[body:])
			channels := binary.LittleEndian.Uint16(wav[body+2:])
			rate := binary.LittleEndian.Uint32(wav[body+4:])
			bits := binary.LittleEndian.Uint16(wav[body+14:])
			if format != 1 || channels != 1 || rate != dictationSampleRate || bits != 16 {
				return 0, validationErr("audio", "unsupported_format")
			}
			haveFmt = true
		case "data":
			// A streamed WAV may declare a size beyond the buffer; use
			// what is actually there.
			if size < 0 || body+size > len(wav) {
				size = len(wav) - body
			}
			dataSize, haveData = size, true
		}
		if haveFmt && haveData {
			break
		}
		if size < 0 || body+size < body {
			return 0, validationErr("audio", "not_wav")
		}
		pos = body + size + size%2
	}
	if !haveFmt || !haveData {
		return 0, validationErr("audio", "not_wav")
	}
	duration := time.Duration(dataSize/2) * time.Second / dictationSampleRate
	if duration < dictationMinDuration {
		return 0, validationErr("audio", "too_short")
	}
	if duration > time.Duration(maxSeconds)*time.Second {
		return 0, validationErr("audio", "too_long")
	}
	return duration, nil
}
