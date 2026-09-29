// Package whisper is a minimal client for whisper.cpp's whisper-server
// (ADR-037): one POST {BaseURL}/inference with a WAV file, answering the
// recognised text. It wraps net/http directly, the same way
// internal/platform/llm does, and is the first engine behind
// training.Transcriber — another engine (Vosk) is another package with
// the same one method.
package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// Client calls one whisper-server. Language is an ISO 639-1 code
// ("ru"); Model is only a label for what the server was started with
// (the server does not report it), carried back to the caller so a
// result can say which engine produced it.
type Client struct {
	BaseURL    string
	Language   string
	Model      string
	HTTPClient *http.Client
}

// NewClient returns a Client with a plain *http.Client — no timeout of
// its own; every call site bounds Transcribe through ctx.
func NewClient(baseURL, language, model string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Language: language, Model: model, HTTPClient: &http.Client{}}
}

// Transcript is one recognition result.
type Transcript struct {
	Text  string
	Model string
}

// Transcribe sends wav to whisper-server and returns the recognised
// text, whitespace-normalised to one line. It never logs or returns the
// audio or the response body: every error carries only a fixed
// description or an HTTP status (RFC-001 §9, ADR-037).
func (c *Client) Transcribe(ctx context.Context, wav []byte) (Transcript, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "dictation.wav")
	if err != nil {
		return Transcript{}, fmt.Errorf("whisper: build request: %w", err)
	}
	if _, err = file.Write(wav); err != nil {
		return Transcript{}, fmt.Errorf("whisper: build request: %w", err)
	}
	fields := [][2]string{{"response_format", "json"}, {"temperature", "0"}}
	if c.Language != "" {
		fields = append(fields, [2]string{"language", c.Language})
	}
	for _, field := range fields {
		if err = form.WriteField(field[0], field[1]); err != nil {
			return Transcript{}, fmt.Errorf("whisper: build request: %w", err)
		}
	}
	if err = form.Close(); err != nil {
		return Transcript{}, fmt.Errorf("whisper: build request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/inference", &body)
	if err != nil {
		return Transcript{}, fmt.Errorf("whisper: build request: %w", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Transcript{}, fmt.Errorf("whisper: request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return Transcript{}, fmt.Errorf("whisper: unexpected status %d", resp.StatusCode)
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Transcript{}, fmt.Errorf("whisper: decode response: %w", err)
	}
	return Transcript{Text: strings.Join(strings.Fields(decoded.Text), " "), Model: c.Model}, nil
}
