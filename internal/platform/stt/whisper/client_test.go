package whisper

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTranscribeSendsMultipartAndNormalisesText(t *testing.T) {
	var gotPath, gotMethod, gotLanguage, gotFormat, gotTemperature string
	var gotFile []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		gotLanguage, gotFormat, gotTemperature = r.FormValue("language"), r.FormValue("response_format"), r.FormValue("temperature")
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("file field: %v", err)
		}
		gotFile, _ = io.ReadAll(file)
		_, _ = w.Write([]byte(`{"text":"  Горит   дом \n по адресу Ленина 5 "}`))
	}))
	defer server.Close()

	result, err := NewClient(server.URL+"/", "ru", "ggml-small").Transcribe(context.Background(), []byte("RIFFwav"))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/inference" {
		t.Fatalf("got %s %s", gotMethod, gotPath)
	}
	if gotLanguage != "ru" || gotFormat != "json" || gotTemperature != "0" || string(gotFile) != "RIFFwav" {
		t.Fatalf("form = language %q format %q temperature %q file %q", gotLanguage, gotFormat, gotTemperature, gotFile)
	}
	if result.Text != "Горит дом по адресу Ленина 5" || result.Model != "ggml-small" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTranscribeEmptyTextIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"text":"   "}`))
	}))
	defer server.Close()
	result, err := NewClient(server.URL, "ru", "m").Transcribe(context.Background(), []byte("x"))
	if err != nil || result.Text != "" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestTranscribeErrorsDoNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret transcript details", http.StatusInternalServerError)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "ru", "m").Transcribe(context.Background(), []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestTranscribeHonoursContext(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := NewClient(server.URL, "ru", "m").Transcribe(ctx, []byte("x")); err == nil {
		t.Fatal("expected a context error")
	}
}

func TestTranscribeBadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "ru", "m").Transcribe(context.Background(), []byte("x")); err == nil {
		t.Fatal("expected a decode error")
	}
}
