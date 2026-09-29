package main

import (
	"context"

	"emsim/internal/platform/config"
	"emsim/internal/platform/stt/whisper"
	"emsim/internal/training"
)

// defaultStubDictation is what DICTATION=stub answers to any phrase.
const defaultStubDictation = "Горит дом, улица Ленина, дом пять."

// newTranscriber wires ADR-037's engine port: nil when dictation is off,
// otherwise whisper-server or the stub. A new engine (Vosk) is one more
// case here and one more package next to internal/platform/stt/whisper.
func newTranscriber(cfg config.Dictation) training.Transcriber {
	switch cfg.Engine {
	case config.DictationWhisper:
		return whisperTranscriber{client: whisper.NewClient(cfg.STTURL, cfg.Language, cfg.Model)}
	case config.DictationStub:
		return training.StubTranscriber{Text: defaultStubDictation}
	default:
		return nil
	}
}

func dictationSettings(cfg config.Dictation) training.DictationSettings {
	return training.DictationSettings{
		MaxSeconds: cfg.MaxSeconds, Timeout: cfg.Timeout, QueueWait: cfg.QueueWait, Concurrency: cfg.Concurrency,
	}
}

// whisperTranscriber adapts the platform client to training.Transcriber.
type whisperTranscriber struct{ client *whisper.Client }

func (w whisperTranscriber) Transcribe(ctx context.Context, wav []byte) (training.Transcript, error) {
	result, err := w.client.Transcribe(ctx, wav)
	if err != nil {
		return training.Transcript{}, err
	}
	return training.Transcript{Text: result.Text, Model: result.Model}, nil
}
