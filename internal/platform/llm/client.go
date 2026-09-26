// Package llm is the minimal OpenAI-compatible chat-completions client
// this project's worker-side model calls share (ADR-003/ADR-025):
// 112-5b's caller.reply (internal/training/operator112/aicaller) today,
// 112-6's assessment.evaluate later. It wraps net/http directly — no
// vendor SDK — since every server this project targets (Ollama during
// development, llama-server at the customer — slice-112-5b-plan.md)
// speaks the same POST {base}/chat/completions shape, and nothing here
// needs more than that one call.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Message is one chat-completions message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is the subset of the OpenAI-compatible chat-completions
// request body this client sends. RepeatPenalty is llama.cpp's own
// extension, absent from OpenAI's own API — Ollama's /v1 endpoint
// accepts it but may not honor it the same way llama-server does (noted
// in slice-112-5b-plan.md and ADR-025); it is always included rather
// than special-cased per server, since sending an extra field an
// OpenAI-compatible server does not recognize is harmless. Stream is
// always false: this client only ever wants the complete text.
type Request struct {
	Model         string    `json:"model"`
	Messages      []Message `json:"messages"`
	Stream        bool      `json:"stream"`
	Temperature   float64   `json:"temperature,omitempty"`
	TopP          float64   `json:"top_p,omitempty"`
	MaxTokens     int       `json:"max_tokens,omitempty"`
	RepeatPenalty float64   `json:"repeat_penalty,omitempty"`
	// ResponseFormat (ADR-028) is the OpenAI-compatible structured-output
	// request field — {"type":"json_schema","json_schema":{"name":...,
	// "schema":{...},"strict":true}} — for a caller (internal/assessment/
	// operator112/descjudge) that needs the server to constrain its
	// output to a fixed JSON shape, rather than parse free-form text and
	// hope. Left as `any` rather than a typed struct: this client stays
	// a thin, protocol-agnostic pass-through (its own doc comment), and
	// the exact accepted shape differs slightly between Ollama's /v1
	// endpoint and llama-server (slice-112-5b-plan.md's stage 2) — the
	// caller that actually needs a schema is best placed to build it and
	// adjust if either server's dialect diverges. Omitted (nil) for
	// every existing caller (aicaller.Replier), so this is additive.
	ResponseFormat any `json:"response_format,omitempty"`
}

// Result is what a caller needs from a completion: the first choice's
// text and whether the server cut it off (FinishReason="length") — a
// caller that cares about truncation (aicaller.Replier) uses this to
// decide whether to trim the text to its last full sentence rather than
// hand a trainee a reply cut off mid-word.
type Result struct {
	Text         string
	FinishReason string
}

// Client calls one OpenAI-compatible /v1/chat/completions endpoint.
// BaseURL carries no trailing slash requirement; Complete appends
// "/chat/completions" itself, so BaseURL is exactly what
// CALLER_LLM_URL/config.Worker.CallerLLMURL documents (e.g.
// "http://host.docker.internal:11434/v1").
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient returns a Client with a plain *http.Client — no implicit
// timeout of its own, since every call site bounds Complete through ctx
// (callerReplyHandler's own CALLER_REPLY_TIMEOUT), the same convention
// every other worker-side external call in this codebase already
// follows.
func NewClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTPClient: &http.Client{}}
}

// Complete sends req to POST {BaseURL}/chat/completions and returns its
// first choice. It never logs req or the response body (RFC-001 §9 —
// operational detail stays out of structured logs; a chat-completions
// request carries a scenario's caller persona and the trainee's own
// message text, both out of place there) — every returned error is
// wrapped with only the HTTP status or a fixed description, never a
// response body.
func (c *Client) Complete(ctx context.Context, req Request) (Result, error) {
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return Result{}, fmt.Errorf("llm: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("llm: request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("llm: unexpected status %d", resp.StatusCode)
	}
	var decoded chatCompletionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, fmt.Errorf("llm: decode response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return Result{}, fmt.Errorf("llm: response has no choices")
	}
	return Result{Text: decoded.Choices[0].Message.Content, FinishReason: decoded.Choices[0].FinishReason}, nil
}

type chatCompletionsResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
}
