package descjudge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"emsim/internal/platform/llm"
)

type fakeChat struct {
	result llm.Result
	err    error
	gotReq llm.Request
}

func (f *fakeChat) Complete(_ context.Context, req llm.Request) (llm.Result, error) {
	f.gotReq = req
	return f.result, f.err
}

func requestPayload(t *testing.T, description string, questions ...Question) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(Request{Description: description, Questions: questions})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return raw
}

func TestHandlerAnswerSendsStructuredRequestAndParsesAnswers(t *testing.T) {
	chat := &fakeChat{result: llm.Result{Text: `{"floor":"yes","observer":"no"}`, FinishReason: "stop"}}
	h := Handler{Chat: chat}
	payload := requestPayload(t, "Пожар на 13-м этаже. Заявитель на улице.",
		Question{ID: "floor", Question: "Указано ли, что пожар на 13-м этаже?"},
		Question{ID: "observer", Question: "Указано ли, что заявитель наблюдает изнутри?"},
	)
	answer, err := h.Answer(context.Background(), "t-lite", map[string]any{"temperature": 0.0, "max_tokens": 512.0}, payload)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	var decoded map[string]Answer
	if err := json.Unmarshal(answer, &decoded); err != nil {
		t.Fatalf("decode returned answer: %v", err)
	}
	if decoded["floor"] != AnswerYes || decoded["observer"] != AnswerNo {
		t.Fatalf("unexpected answers: %+v", decoded)
	}
	if chat.gotReq.Model != "t-lite" || chat.gotReq.Temperature != 0 || chat.gotReq.MaxTokens != 512 {
		t.Fatalf("unexpected request: %+v", chat.gotReq)
	}
	if len(chat.gotReq.Messages) != 2 || chat.gotReq.Messages[0].Role != "system" || chat.gotReq.Messages[1].Role != "user" {
		t.Fatalf("unexpected messages: %+v", chat.gotReq.Messages)
	}
	format, ok := chat.gotReq.ResponseFormat.(map[string]any)
	if !ok || format["type"] != "json_schema" {
		t.Fatalf("expected a json_schema response_format, got %+v", chat.gotReq.ResponseFormat)
	}
}

func TestHandlerAnswerUsesDefaultsWhenParametersMissing(t *testing.T) {
	chat := &fakeChat{result: llm.Result{Text: `{"q":"no"}`, FinishReason: "stop"}}
	h := Handler{Chat: chat}
	payload := requestPayload(t, "x", Question{ID: "q", Question: "Указано ли …?"})
	if _, err := h.Answer(context.Background(), "m", nil, payload); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if chat.gotReq.Temperature != 0 || chat.gotReq.MaxTokens != defaultMaxTokens {
		t.Fatalf("unexpected defaults: %+v", chat.gotReq)
	}
}

func TestHandlerAnswerStripsThinkBlock(t *testing.T) {
	chat := &fakeChat{result: llm.Result{Text: "<think>рассуждение</think>\n" + `{"q":"yes"}`, FinishReason: "stop"}}
	h := Handler{Chat: chat}
	payload := requestPayload(t, "x", Question{ID: "q", Question: "y?"})
	answer, err := h.Answer(context.Background(), "m", nil, payload)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	var decoded map[string]Answer
	if err := json.Unmarshal(answer, &decoded); err != nil || decoded["q"] != AnswerYes {
		t.Fatalf("unexpected answer: %s (err=%v)", answer, err)
	}
}

func TestHandlerAnswerRejectsTruncatedResponse(t *testing.T) {
	chat := &fakeChat{result: llm.Result{Text: `{"q":"ye`, FinishReason: "length"}}
	h := Handler{Chat: chat}
	payload := requestPayload(t, "x", Question{ID: "q", Question: "y?"})
	if _, err := h.Answer(context.Background(), "m", nil, payload); err == nil {
		t.Fatal("expected an error for a truncated response, not a guessed answer")
	}
}

func TestHandlerAnswerRejectsMalformedJSON(t *testing.T) {
	chat := &fakeChat{result: llm.Result{Text: "not json", FinishReason: "stop"}}
	h := Handler{Chat: chat}
	payload := requestPayload(t, "x", Question{ID: "q", Question: "y?"})
	if _, err := h.Answer(context.Background(), "m", nil, payload); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestHandlerAnswerRejectsMissingOrExtraOrInvalidAnswers(t *testing.T) {
	for name, text := range map[string]string{
		"missing":       `{}`,
		"extra key":     `{"q":"yes","extra":"no"}`,
		"invalid value": `{"q":"maybe"}`,
	} {
		t.Run(name, func(t *testing.T) {
			chat := &fakeChat{result: llm.Result{Text: text, FinishReason: "stop"}}
			h := Handler{Chat: chat}
			payload := requestPayload(t, "x", Question{ID: "q", Question: "y?"})
			if _, err := h.Answer(context.Background(), "m", nil, payload); err == nil {
				t.Fatalf("expected an error for %s", name)
			}
		})
	}
}

func TestHandlerAnswerPropagatesTransportError(t *testing.T) {
	chat := &fakeChat{err: errors.New("connection refused")}
	h := Handler{Chat: chat}
	payload := requestPayload(t, "x", Question{ID: "q", Question: "y?"})
	if _, err := h.Answer(context.Background(), "m", nil, payload); err == nil {
		t.Fatal("expected the transport error to propagate")
	}
}

func TestHandlerAnswerRejectsRequestWithNoQuestions(t *testing.T) {
	h := Handler{Chat: &fakeChat{}}
	payload := requestPayload(t, "x")
	if _, err := h.Answer(context.Background(), "m", nil, payload); err == nil {
		t.Fatal("expected an error for a request with no questions")
	}
}
