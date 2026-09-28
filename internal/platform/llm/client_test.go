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
	if result.Text != "Здравствуйте." || result.FinishReason != "stop" || result.Timings != nil {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestClientCompleteReadsLlamaServerTimings: llama-server reports how
// many prompt tokens it processed and how many came from the prefix
// cache; bench-llm uses them to check the caller warm-up (ADR-029).
func TestClientCompleteReadsLlamaServerTimings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Да."},"finish_reason":"stop"}],"timings":{"cache_n":480,"prompt_n":37,"prompt_ms":2310.5}}`))
	}))
	defer server.Close()
	result, err := NewClient(server.URL).Complete(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Timings == nil || result.Timings.PromptTokens != 37 || result.Timings.CachedTokens != 480 {
		t.Fatalf("Timings = %+v, want prompt 37, cached 480", result.Timings)
	}
}

// TestClientCompleteSendsResponseFormatWhenSet is ADR-028's own addition:
// a caller that sets ResponseFormat (descjudge's structured-output
// request) must see it reach the wire exactly as given, and a caller
// that leaves it nil (every pre-ADR-028 caller, aicaller.Replier) must
// not send the field at all — omitempty, not a null.
func TestClientCompleteSendsResponseFormatWhenSet(t *testing.T) {
	var rawBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	format := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "x", "schema": map[string]any{"type": "object"}}}
	if _, err := client.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}, ResponseFormat: format}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := rawBody["response_format"]; !ok {
		t.Fatalf("response_format missing from request body: %+v", rawBody)
	}

	rawBody = nil
	if _, err := client.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := rawBody["response_format"]; ok {
		t.Fatalf("response_format must be omitted when unset: %+v", rawBody)
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

// TestClientCompleteSendsZeroTemperature is ADR-029's own regression: a
// judge configured with temperature 0 must put an explicit 0 on the wire,
// otherwise the server substitutes its own sampling default and the
// sealed judge parameters no longer describe what actually ran.
func TestClientCompleteSendsZeroTemperature(t *testing.T) {
	var rawBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	if _, err := NewClient(server.URL).Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}, Temperature: 0}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	temperature, ok := rawBody["temperature"]
	if !ok {
		t.Fatalf("temperature missing from request body: %+v", rawBody)
	}
	if temperature != float64(0) {
		t.Fatalf("temperature = %v, want 0", temperature)
	}
}

// TestClientCompleteSendsBearerKeyAndOpenAIDialect (ADR-033): a remote
// model gets the key as a Bearer header, and the strict OpenAI dialect
// drops llama.cpp's own repeat_penalty; the bundled llama-server gets
// neither a header nor a changed body.
func TestClientCompleteSendsBearerKeyAndOpenAIDialect(t *testing.T) {
	var gotAuth string
	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw = nil
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Да."},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	req := Request{Model: "m", Messages: []Message{{Role: "user", Content: "?"}}, RepeatPenalty: 1.1}

	remote := NewClientWith(server.URL, Options{APIKey: "sk-test-secret", Dialect: DialectOpenAI})
	if _, err := remote.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer sk-test-secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if _, ok := raw["repeat_penalty"]; ok {
		t.Fatalf("openai dialect must not send repeat_penalty: %v", raw)
	}

	local := NewClient(server.URL)
	if _, err := local.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("local client must not send Authorization, got %q", gotAuth)
	}
	if raw["repeat_penalty"] != 1.1 {
		t.Fatalf("llama dialect must keep repeat_penalty: %v", raw)
	}
}

// TestClientCompleteErrorNeverCarriesAPIKey: a rejected key surfaces as
// a status-only error, with neither the key nor the response body in it.
func TestClientCompleteErrorNeverCarriesAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key sk-test-secret"}`))
	}))
	defer server.Close()
	_, err := NewClientWith(server.URL, Options{APIKey: "sk-test-secret"}).Complete(context.Background(), Request{Model: "m"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "sk-test-secret") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("unexpected error text: %q", err.Error())
	}
}
