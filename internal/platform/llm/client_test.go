package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCompleteSendsExpectedRequestShape(t *testing.T) {
	var captured Request
	var gotPath, gotMethod, gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotContentType = r.URL.Path, r.Method, r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Здравствуйте."},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.Complete(context.Background(), Request{
		Model:         "t-tech/T-lite-it-2.1:q5_K_M",
		Messages:      []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "Алло?"}},
		Temperature:   0.3,
		TopP:          0.9,
		RepeatPenalty: 1.1,
		MaxTokens:     150,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/chat/completions" {
		t.Fatalf("got method=%s path=%s", gotMethod, gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("got content-type %q", gotContentType)
	}
	if captured.Stream {
		t.Fatal("stream must always be sent as false")
	}
	if captured.Model != "t-tech/T-lite-it-2.1:q5_K_M" || len(captured.Messages) != 2 || captured.RepeatPenalty != 1.1 {
		t.Fatalf("unexpected captured request: %+v", captured)
	}
	if result.Text != "Здравствуйте." || result.FinishReason != "stop" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestClientCompleteRejectsNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server secret stack trace"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
	if strings.Contains(err.Error(), "secret stack trace") {
		t.Fatalf("error must not leak the response body: %v", err)
	}
}

func TestClientCompleteHonorsContextCancellation(t *testing.T) {
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
	}))
	defer func() { close(unblock); server.Close() }()

	client := NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Complete(ctx, Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected the timed-out context to abort Complete")
	}
}

func TestClientCompleteRejectsEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if _, err := client.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("expected an error for a response with no choices")
	}
}
