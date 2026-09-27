package aicaller

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"emsim/internal/content"
	"emsim/internal/platform/llm"
	"emsim/internal/training"
	"emsim/internal/training/operator112"
)

// Adapter identifies every Replier-produced turn (IntakeCallerTurn.Adapter)
// regardless of which no-model/model path answered it — which model
// answered a given turn is training.IntakeCallerGeneration.Model's own
// job, at the finer grain evidence needs, so Adapter itself only needs
// to say "the 112-5b adapter answered this", mirroring
// StubCallerReplierAdapter's own granularity (ADR-024).
const Adapter = "aicaller/v1"

// FallbackText is the neutral reply callerReplyHandler applies
// (cmd/emsim/worker_composition.go) when the model errors out on the
// caller.reply task's last allowed attempt, instead of leaving the turn
// to fail (ADR-025's decision 6) — scenario-agnostic on purpose, since
// it must never claim a fact the trainee has not actually heard.
const FallbackText = "Алло, вас плохо слышно… Повторите, пожалуйста."

// ChatCompleter is aicaller's own narrow port onto an OpenAI-compatible
// chat-completions call (ADR-003/ADR-025): platform/llm.Client
// implements it. Declaring it here, rather than depending on
// *llm.Client directly, keeps this package's domain logic (state,
// classify, prompt) testable without an HTTP server, per this project's
// hexagonal convention (CLAUDE.md) — only Request/Result, plain data
// types, cross the boundary.
type ChatCompleter interface {
	Complete(ctx context.Context, req llm.Request) (llm.Result, error)
}

// Replier is 112-5b's own operator112.CallerReplier: an AI adapter that
// falls back to Stub (ordinarily operator112.StubCallerReplier) whenever
// a scenario has no caller profile — slice-112-5b-plan.md's decision 7 —
// so CALLER_REPLIER=llm never invents a persona for a scenario that
// never declared one; the turn's own Adapter/Source (stub/v1, "stub")
// then makes that visible in the instructor's review exactly as it
// would under CALLER_REPLIER=stub.
type Replier struct {
	Chat          ChatCompleter
	Model         string
	Temperature   float64
	TopP          float64
	RepeatPenalty float64
	MaxTokens     int
	Stub          operator112.CallerReplier
}

// Reply implements operator112.CallerReplier. Its own no-model paths —
// opening (turn 1) and a scripted answer_variants match — mirror
// StubCallerReplier/112-2's preparedCaller exactly in spirit: some
// replies never need a model call at all, and deciding that is this
// package's classifier, not the model's job.
func (r Replier) Reply(ctx context.Context, req operator112.CallerReplyRequest) (operator112.CallerReply, error) {
	if req.Caller == nil {
		return r.Stub.Reply(ctx, req)
	}
	open := OpenFacts(req.Facts, req.Transcript)
	if req.Turn <= 1 {
		return operator112.CallerReply{
			Text: req.Caller.Opening.Text, Adapter: Adapter, Source: training.CallerTurnSourceOpening,
			Reveals: append([]string(nil), req.Caller.Opening.Reveals...),
		}, nil
	}
	current := lastOperatorMessage(req.Transcript)
	askedThisTurn := AskedFacts(req.Facts, current)
	if text, ok := scriptedAnswer(req.Facts, askedThisTurn, current); ok {
		return operator112.CallerReply{
			Text: text, Adapter: Adapter, Source: training.CallerTurnSourceScripted,
			Reveals: DiscloseReveals(req.Facts, open, text),
		}, nil
	}
	revealed := RevealedFacts(req.Transcript)
	messages := BuildMessages(req.Caller.Persona, req.Facts, req.Transcript, open, revealed, askedThisTurn, current)
	result, err := r.Chat.Complete(ctx, llm.Request{
		Model: r.Model, Messages: messages, Temperature: r.Temperature,
		TopP: r.TopP, RepeatPenalty: r.RepeatPenalty, MaxTokens: r.MaxTokens,
	})
	if err != nil {
		return operator112.CallerReply{}, err
	}
	text := CleanReply(result.Text, result.FinishReason)
	if text == "" {
		return operator112.CallerReply{}, fmt.Errorf("aicaller: empty reply after cleanup")
	}
	return operator112.CallerReply{
		Text: text, Adapter: Adapter, Source: training.CallerTurnSourceModel,
		Reveals: DiscloseReveals(req.Facts, open, text), Generation: r.generation(),
	}, nil
}

// Warm asks the model to process req's dialogue prefix ahead of the next
// reply (ADR-029's prompt-cache warm-up): WarmupMessages with a single
// generated token, the output discarded. It is called right after the
// no-model opening, while the trainee reads it and types, so the first
// model-answered turn does not have to process the whole system prompt
// and opening from scratch. A scenario with no caller profile never
// reaches the model and is not warmed. The call has no effect on the
// dialogue; an error only means the next reply starts cold.
func (r Replier) Warm(ctx context.Context, req operator112.CallerReplyRequest) error {
	if req.Caller == nil || len(req.Transcript) == 0 {
		return nil
	}
	_, err := r.Chat.Complete(ctx, llm.Request{
		Model: r.Model, Messages: WarmupMessages(req.Caller.Persona, req.Transcript),
		Temperature: r.Temperature, TopP: r.TopP, RepeatPenalty: r.RepeatPenalty, MaxTokens: 1,
	})
	return err
}

// Fallback returns FallbackText for req — see FallbackText's own doc
// comment. Reveals is always empty: a fallback discloses nothing new,
// and generation parameters are still recorded (Source=fallback) so
// evidence shows this reply came from the fallback path with these
// generation settings, not a genuine model answer.
func (r Replier) Fallback(req operator112.CallerReplyRequest) operator112.CallerReply {
	return operator112.CallerReply{
		Text: FallbackText, Adapter: Adapter, Source: training.CallerTurnSourceFallback,
		Generation: r.generation(),
	}
}

func (r Replier) generation() *training.IntakeCallerGeneration {
	return &training.IntakeCallerGeneration{
		Model: r.Model, PromptVersion: PromptVersion, Temperature: r.Temperature,
		TopP: r.TopP, RepeatPenalty: r.RepeatPenalty, MaxTokens: r.MaxTokens,
	}
}

// scriptedAnswer mirrors the local MVP's caller.py _scripted_reply: when
// the current message asks about exactly one fact, and that fact has an
// answer_variants entry whose When matches message (or has no When at
// all — unconditional), the reply is that entry's Text, verbatim from
// the scenario, no model call (slice-112-5b-plan.md's decision 3).
func scriptedAnswer(facts []content.Intake112Fact, askedThisTurn []string, message string) (string, bool) {
	if len(askedThisTurn) != 1 {
		return "", false
	}
	id := askedThisTurn[0]
	for _, fact := range facts {
		if fact.ID != id {
			continue
		}
		for _, variant := range fact.AnswerVariants {
			if variant.When == "" || content.MatchesPattern(variant.When, message) {
				return variant.Text, true
			}
		}
	}
	return "", false
}

// CleanReply strips leaked model reasoning through llm.StripThinking
// (a closed <think>...</think> block, an unclosed one cut off by
// max_tokens, or a template-opened one ending in a lone </think> —
// reasoning is disabled server-side, ADR-029, but this stays a
// defensive strip rather than a trust assumption), trims whitespace,
// and — when finishReason is "length" (the server cut the reply off
// mid-generation) — trims to the last full sentence so a trainee never
// sees a reply cut off mid-word. A reply that is empty after cleanup
// (including one that was only reasoning) returns "" — Reply
// treats that as an error, same as any other model failure.
func CleanReply(text, finishReason string) string {
	cleaned := llm.StripThinking(text)
	if finishReason == "length" {
		cleaned = trimToLastSentence(cleaned)
	}
	return cleaned
}

func trimToLastSentence(text string) string {
	last := -1
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' || r == '…' {
			last = i + utf8.RuneLen(r)
		}
	}
	if last <= 0 {
		return text
	}
	return strings.TrimSpace(text[:last])
}
