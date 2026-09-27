package llm

import (
	"regexp"
	"strings"
)

var (
	closedThinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)
	openThinkTail    = regexp.MustCompile(`(?s)<think>.*$`)
)

// StripThinking removes a model's leaked reasoning from a completion's
// text (ADR-029). llama-server runs with --reasoning-budget 0 and the
// Ollama models used in development have reasoning off, but that is a
// server-side setting this client cannot enforce, so every caller strips
// defensively. Three shapes are handled:
//
//   - a closed "<think>…</think>" block, anywhere in the text;
//   - an unclosed "<think>…" running to the end of the text — generation
//     was cut off (max_tokens) mid-reasoning, so nothing after it is a
//     real answer;
//   - a lone "</think>" with no opening tag — the chat template put
//     "<think>" into the prompt itself, so the completion starts inside
//     the reasoning; everything up to the tag is dropped.
//
// The result is trimmed of surrounding whitespace. Text with no reasoning
// markers comes back only trimmed.
func StripThinking(text string) string {
	text = closedThinkBlock.ReplaceAllString(text, "")
	if i := strings.Index(text, "</think>"); i >= 0 {
		text = text[i+len("</think>"):]
	}
	text = openThinkTail.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}
