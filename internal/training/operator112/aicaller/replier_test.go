package aicaller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/platform/llm"
	"emsim/internal/training"
	"emsim/internal/training/operator112"
)

func testFacts() []content.Intake112Fact {
	return []content.Intake112Fact{
		{
			ID: "address_city", Label: "Город", CardPath: "/address/city", Knowledge: "initial", Value: "Москва",
			Statement: "Мы в Москве", AskPatterns: []string{"город"}, DisclosurePatterns: []string{"москв"},
		},
		{
			ID: "age", Label: "Возраст", Knowledge: "on_question", Value: "40",
			Statement: "Мне сорок лет", AskPatterns: []string{"возраст", "сколько.*лет"}, AskExcludePatterns: []string{"сколько.*человек"},
			AnswerVariants:     []content.Intake112AnswerVariant{{When: "возраст", Text: "Мне сорок лет."}, {Text: "Сорок."}},
			DisclosurePatterns: []string{`\d{2}\s*лет`, `сорок`},
		},
		{
			ID: "phone", Label: "Телефон", CardPath: "/provided_phone", Knowledge: "on_question", Value: "+79001234567",
			Statement: "Мой телефон +7 900 123-45-67", AskPatterns: []string{"номер телефона", "ваш телефон"},
		},
		{
			ID: "exact_location", Label: "Точное место", Knowledge: "unknown",
			AskPatterns: []string{"где именно", "ориентир"},
		},
	}
}

func testCaller() *content.Intake112CallerProfile {
	return &content.Intake112CallerProfile{
		Persona: "Женщина, 40 лет, испугана, но говорит связно.",
		Opening: content.Intake112Utterance{ID: "opening", Text: "Помогите, у нас пахнет газом!", Reveals: []string{"address_city"}},
	}
}

func line(speaker, text string, reveals ...string) training.IntakeLine {
	return training.IntakeLine{Speaker: speaker, Text: text, Reveals: reveals, ServerAt: time.Now()}
}

// erroringChat fails the test if Complete is ever called — used by every
// test that expects a no-model path (opening/scripted) to answer without
// touching the model at all.
type erroringChat struct{ t *testing.T }

func (e erroringChat) Complete(context.Context, llm.Request) (llm.Result, error) {
	e.t.Fatal("Complete must not be called for a no-model reply")
	return llm.Result{}, nil
}

type stubChat struct {
	result llm.Result
	err    error
	gotReq llm.Request
}

func (s *stubChat) Complete(_ context.Context, req llm.Request) (llm.Result, error) {
	s.gotReq = req
	return s.result, s.err
}

func TestReplyOpeningAnswersWithoutModelCall(t *testing.T) {
	r := Replier{Chat: erroringChat{t}, Model: "m"}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 1,
		Transcript: []training.IntakeLine{line("operator", "Алло, слушаю вас")},
	}
	reply, err := r.Reply(context.Background(), req)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Text != req.Caller.Opening.Text || reply.Source != training.CallerTurnSourceOpening || reply.Adapter != Adapter {
		t.Fatalf("unexpected opening reply: %+v", reply)
	}
	if len(reply.Reveals) != 1 || reply.Reveals[0] != "address_city" {
		t.Fatalf("opening reveals must come from the scenario's own Opening.Reveals: %+v", reply.Reveals)
	}
	if reply.Generation != nil {
		t.Fatalf("opening must not carry generation params: %+v", reply.Generation)
	}
}

func TestReplyNoProfileDelegatesToStub(t *testing.T) {
	stub := operator112.StubCallerReplier{}
	r := Replier{Chat: erroringChat{t}, Model: "m", Stub: stub}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: nil, Turn: 1,
		Transcript: []training.IntakeLine{line("operator", "Алло")},
	}
	reply, err := r.Reply(context.Background(), req)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	want, _ := stub.Reply(context.Background(), req)
	if reply.Text != want.Text || reply.Adapter != operator112.StubCallerReplierAdapter || reply.Source != training.CallerTurnSourceStub {
		t.Fatalf("expected delegation to the stub, got %+v", reply)
	}
}

func TestFactAskedExcludesLandmarkFromAddressQuestion(t *testing.T) {
	facts := []content.Intake112Fact{
		{ID: "address", AskPatterns: []string{"адрес", "где вы находитесь"}, AskExcludePatterns: []string{"ориентировк|местоположени"}},
		{ID: "exact_location", AskPatterns: []string{"ориентир|местоположени"}},
	}
	if FactAsked(facts[0], "Назовите ваше местоположение") {
		t.Fatal("a landmark question must not count as asking for the address")
	}
	if !FactAsked(facts[0], "Назовите ваш адрес") {
		t.Fatal("a plain address question must still be detected")
	}
	if !FactAsked(facts[1], "Уточните ориентир рядом с домом") {
		t.Fatal("the landmark fact itself must still match")
	}
}

func TestReplyScriptedAnswerWhenExactlyOneFactAsked(t *testing.T) {
	r := Replier{Chat: erroringChat{t}, Model: "m"}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 2,
		Transcript: []training.IntakeLine{
			line("operator", "Алло"), line("caller", "Помогите, у нас пахнет газом!", "address_city"),
			line("operator", "Какой у вас возраст?"),
		},
	}
	reply, err := r.Reply(context.Background(), req)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Text != "Мне сорок лет." || reply.Source != training.CallerTurnSourceScripted {
		t.Fatalf("expected the matching answer_variant, got %+v", reply)
	}
	if len(reply.Reveals) != 1 || reply.Reveals[0] != "age" {
		t.Fatalf("scripted reply must reveal the fact it answers: %+v", reply.Reveals)
	}
}

func TestReplyScriptedAnswerSkippedWhenMultipleFactsAsked(t *testing.T) {
	chat := &stubChat{result: llm.Result{Text: "Сорок лет, Москва.", FinishReason: "stop"}}
	r := Replier{Chat: chat, Model: "m"}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 2,
		Transcript: []training.IntakeLine{
			line("operator", "Алло"), line("caller", "Помогите, у нас пахнет газом!", "address_city"),
			line("operator", "Сколько вам лет и в каком вы городе?"),
		},
	}
	reply, err := r.Reply(context.Background(), req)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Source != training.CallerTurnSourceModel {
		t.Fatalf("two facts asked at once must fall through to the model, got source=%q", reply.Source)
	}
}

func TestReplyModelCallCleansThinkBlockAndSetsGeneration(t *testing.T) {
	chat := &stubChat{result: llm.Result{Text: "<think>ума не приложу</think>  Мы в Москве.  ", FinishReason: "stop"}}
	r := Replier{Chat: chat, Model: "t-tech/T-lite-it-2.1:q5_K_M", Temperature: 0.3, TopP: 0.9, RepeatPenalty: 1.1, MaxTokens: 150}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 2,
		Transcript: []training.IntakeLine{
			line("operator", "Алло"), line("caller", "Помогите, у нас пахнет газом!", "address_city"),
			line("operator", "Где вы находитесь, успокойтесь пожалуйста и назовите город"),
		},
	}
	reply, err := r.Reply(context.Background(), req)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Text != "Мы в Москве." {
		t.Fatalf("expected the <think> block stripped and text trimmed, got %q", reply.Text)
	}
	if reply.Source != training.CallerTurnSourceModel || reply.Adapter != Adapter {
		t.Fatalf("unexpected source/adapter: %+v", reply)
	}
	if reply.Generation == nil || reply.Generation.Model != r.Model || reply.Generation.PromptVersion != PromptVersion {
		t.Fatalf("unexpected generation: %+v", reply.Generation)
	}
	if len(reply.Reveals) != 1 || reply.Reveals[0] != "address_city" {
		t.Fatalf("expected address_city to be re-detected as revealed: %+v", reply.Reveals)
	}
}

func TestReplyModelErrorPropagates(t *testing.T) {
	chat := &stubChat{err: errors.New("connection refused")}
	r := Replier{Chat: chat, Model: "m"}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 2,
		Transcript: []training.IntakeLine{
			line("operator", "Алло"), line("caller", "Помогите!", "address_city"),
			line("operator", "Расскажите подробнее, что случилось"),
		},
	}
	if _, err := r.Reply(context.Background(), req); err == nil {
		t.Fatal("expected the model error to propagate")
	}
}

func TestReplyRejectsEmptyReplyAfterCleanup(t *testing.T) {
	chat := &stubChat{result: llm.Result{Text: "<think>только рассуждение, ничего больше</think>   ", FinishReason: "stop"}}
	r := Replier{Chat: chat, Model: "m"}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 2,
		Transcript: []training.IntakeLine{
			line("operator", "Алло"), line("caller", "Помогите!", "address_city"),
			line("operator", "Расскажите подробнее, что случилось"),
		},
	}
	if _, err := r.Reply(context.Background(), req); err == nil {
		t.Fatal("expected an error for a reply that is only a <think> block")
	}
}

// TestReplyRejectsUnclosedThinkBlock is ADR-029's own case: generation
// cut off by max_tokens in the middle of reasoning leaves an unclosed
// <think>, which must never reach the trainee as a caller line.
func TestReplyRejectsUnclosedThinkBlock(t *testing.T) {
	chat := &stubChat{result: llm.Result{Text: "<think>начинаю рассуждать. Думаю, что адрес", FinishReason: "length"}}
	r := Replier{Chat: chat, Model: "m"}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 2,
		Transcript: []training.IntakeLine{
			line("operator", "Алло"), line("caller", "Помогите!", "address_city"),
			line("operator", "Расскажите подробнее, что случилось"),
		},
	}
	if reply, err := r.Reply(context.Background(), req); err == nil {
		t.Fatalf("expected an error for a reply that is only unclosed reasoning, got %q", reply.Text)
	}
}

func TestSystemMessageStaysByteIdenticalAcrossTurns(t *testing.T) {
	chat := &stubChat{result: llm.Result{Text: "ответ", FinishReason: "stop"}}
	r := Replier{Chat: chat, Model: "m"}
	caller := testCaller()
	transcriptTurn2 := []training.IntakeLine{
		line("operator", "Алло"), line("caller", "Помогите, у нас пахнет газом!", "address_city"),
		line("operator", "Расскажите подробнее"),
	}
	if _, err := r.Reply(context.Background(), operator112.CallerReplyRequest{Facts: testFacts(), Caller: caller, Turn: 2, Transcript: transcriptTurn2}); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	systemAtTurn2 := chat.gotReq.Messages[0].Content

	transcriptTurn3 := append(append([]training.IntakeLine{}, transcriptTurn2...), line("caller", "ответ"), line("operator", "А в каком вы городе?"))
	if _, err := r.Reply(context.Background(), operator112.CallerReplyRequest{Facts: testFacts(), Caller: caller, Turn: 3, Transcript: transcriptTurn3}); err != nil {
		t.Fatalf("turn 3: %v", err)
	}
	systemAtTurn3 := chat.gotReq.Messages[0].Content

	if systemAtTurn2 != systemAtTurn3 {
		t.Fatalf("system message changed between turns, breaking prefix-cache reuse:\nturn2=%q\nturn3=%q", systemAtTurn2, systemAtTurn3)
	}
	// The dynamic facts/task block must live in the final user message,
	// not the system message — the whole point of decision 4.
	if len(chat.gotReq.Messages) < 2 {
		t.Fatal("expected at least a system and a final user message")
	}
	last := chat.gotReq.Messages[len(chat.gotReq.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "ФАКТЫ") || !strings.Contains(last.Content, "ЗАДАЧА ТЕКУЩЕЙ РЕПЛИКИ") {
		t.Fatalf("expected the dynamic block appended to the final user message: %+v", last)
	}
}

func TestClosedFactsNeverAppearInThePrompt(t *testing.T) {
	chat := &stubChat{result: llm.Result{Text: "ответ", FinishReason: "stop"}}
	r := Replier{Chat: chat, Model: "m"}
	req := operator112.CallerReplyRequest{
		Facts: testFacts(), Caller: testCaller(), Turn: 2,
		Transcript: []training.IntakeLine{
			line("operator", "Алло"), line("caller", "Помогите, у нас пахнет газом!", "address_city"),
			line("operator", "Расскажите подробнее, что случилось"),
		},
	}
	if _, err := r.Reply(context.Background(), req); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	last := chat.gotReq.Messages[len(chat.gotReq.Messages)-1].Content
	// age/phone/exact_location were never asked about — only address_city
	// (open from the start) belongs in the facts block.
	if strings.Contains(last, "Мне сорок лет") || strings.Contains(last, "телефон") || strings.Contains(last, "Точное место") {
		t.Fatalf("a never-asked fact leaked into the prompt: %q", last)
	}
	if !strings.Contains(last, "Мы в Москве") {
		t.Fatalf("the open fact should be in the prompt: %q", last)
	}
}

func TestFallbackReturnsNeutralPhraseWithGeneration(t *testing.T) {
	r := Replier{Model: "t-tech/T-lite-it-2.1:q5_K_M", Temperature: 0.3, TopP: 0.9, RepeatPenalty: 1.1, MaxTokens: 150}
	reply := r.Fallback(operator112.CallerReplyRequest{Facts: testFacts(), Caller: testCaller(), Turn: 2})
	if reply.Text != FallbackText || reply.Source != training.CallerTurnSourceFallback || reply.Adapter != Adapter {
		t.Fatalf("unexpected fallback reply: %+v", reply)
	}
	if len(reply.Reveals) != 0 {
		t.Fatalf("fallback must not reveal anything: %+v", reply.Reveals)
	}
	if reply.Generation == nil || reply.Generation.Model != r.Model {
		t.Fatalf("fallback must still record generation params: %+v", reply.Generation)
	}
}

func TestCleanReplyTrimsToLastSentenceWhenTruncated(t *testing.T) {
	got := CleanReply("Мы в Москве. Дом горит, но я вышел на", "length")
	if got != "Мы в Москве." {
		t.Fatalf("got %q", got)
	}
}

func TestCleanReplyKeepsFullTextWhenNotTruncated(t *testing.T) {
	got := CleanReply("  Мы в Москве.  ", "stop")
	if got != "Мы в Москве." {
		t.Fatalf("got %q", got)
	}
}
