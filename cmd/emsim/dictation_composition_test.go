package main

import (
	"testing"
	"time"

	"emsim/internal/platform/config"
	"emsim/internal/training"
)

func TestNewTranscriberByEngine(t *testing.T) {
	if newTranscriber(config.Dictation{Engine: config.DictationOff}) != nil {
		t.Fatal("dictation off must give no transcriber")
	}
	if _, ok := newTranscriber(config.Dictation{Engine: config.DictationStub}).(training.StubTranscriber); !ok {
		t.Fatal("stub engine must give the stub transcriber")
	}
	if _, ok := newTranscriber(config.Dictation{Engine: config.DictationWhisper, STTURL: "http://stt:8080"}).(whisperTranscriber); !ok {
		t.Fatal("whisper engine must give the whisper adapter")
	}
	settings := dictationSettings(config.Dictation{MaxSeconds: 20, Timeout: time.Second, QueueWait: 2 * time.Second, Concurrency: 3})
	if settings != (training.DictationSettings{MaxSeconds: 20, Timeout: time.Second, QueueWait: 2 * time.Second, Concurrency: 3}) {
		t.Fatalf("settings = %+v", settings)
	}
}
