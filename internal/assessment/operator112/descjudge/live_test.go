// Live sanity check against a real Ollama/llama-server instance — run
// manually (decision 7, 2026-09-26), never part of the ordinary test
// suite: go test -tags=llmlive ./internal/assessment/operator112/descjudge/...
// with JUDGE_LLM_URL/JUDGE_LLM_MODEL set (defaults below match the
// archived prototype's own local setup, handoff/
// claude_evaluator_112_20260926.zip). This does not re-measure the
// prototype's own 94.8%-agreement evaluation (evaluation_report_
// 2026-09-26.md already did that on the native Ollama API) — it only
// proves *this* Go implementation's prompt/schema/transport
// (internal/platform/llm's OpenAI-compatible client, ADR-028's
// ResponseFormat) round-trips correctly against the same model, using a
// handful of cases drawn from the three seed scenarios ADR-028 actually
// ships questions for.
//
//go:build llmlive

package descjudge

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"emsim/internal/platform/llm"
)

func liveJudgeURL() string {
	if v := os.Getenv("JUDGE_LLM_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:11434/v1"
}

func liveJudgeModel() string {
	if v := os.Getenv("JUDGE_LLM_MODEL"); v != "" {
		return v
	}
	return "t-tech/T-lite-it-2.1:q5_K_M"
}

// liveCase is one hand-picked description/expected-answer pair drawn
// from the three seed scenarios' own questions (seed/scenarios/
// pilot-112-ai-{car-in-water,mobile-shop,toyota-fire}-01-v2.json) and
// the archived prototype's own case variants (correct/missing_fact) for
// the same facts.
type liveCase struct {
	name        string
	description string
	questions   []Question
	want        map[string]Answer
}

func liveCases() []liveCase {
	return []liveCase{
		{
			name:        "car-in-water/correct",
			description: "Автомобиль упал в воду. Есть ли люди внутри — неизвестно. Нужно направить скорую.",
			questions: []Question{
				{ID: "car", Question: "Указано ли, что автомобиль упал в воду?"},
				{ID: "people", Question: "Передано ли, что наличие людей в автомобиле неизвестно?"},
				{ID: "ambulance", Question: "Указано ли, что скорую помощь необходимо направить?"},
			},
			want: map[string]Answer{"car": AnswerYes, "people": AnswerYes, "ambulance": AnswerYes},
		},
		{
			name:        "car-in-water/missing_fact",
			description: "Автомобиль упал в воду. В машине никого нет.",
			questions: []Question{
				{ID: "car", Question: "Указано ли, что автомобиль упал в воду?"},
				{ID: "people", Question: "Передано ли, что наличие людей в автомобиле неизвестно?"},
				{ID: "ambulance", Question: "Указано ли, что скорую помощь необходимо направить?"},
			},
			want: map[string]Answer{"car": AnswerYes, "people": AnswerNo, "ambulance": AnswerNo},
		},
		{
			name:        "mobile-shop/correct",
			description: "Заявитель поссорился с продавцом в «МегаФоне» и бросил трубку.",
			questions: []Question{
				{ID: "argument", Question: "Указано ли, что заявитель поссорился с продавцом?"},
				{ID: "shop", Question: "Указано ли, что конфликт произошёл в «МегаФоне»?"},
				{ID: "ended_call", Question: "Указано ли, что заявитель бросил трубку?"},
			},
			want: map[string]Answer{"argument": AnswerYes, "shop": AnswerYes, "ended_call": AnswerYes},
		},
		{
			name:        "mobile-shop/missing_fact",
			description: "Заявитель поссорился с продавцом в магазине связи.",
			questions: []Question{
				{ID: "argument", Question: "Указано ли, что заявитель поссорился с продавцом?"},
				{ID: "shop", Question: "Указано ли, что конфликт произошёл в «МегаФоне»?"},
				{ID: "ended_call", Question: "Указано ли, что заявитель бросил трубку?"},
			},
			want: map[string]Answer{"argument": AnswerYes, "shop": AnswerNo, "ended_call": AnswerNo},
		},
		{
			name:        "toyota-fire/correct",
			description: "Горит белая Toyota. У водителя, Дюжева Юрия Петровича, ожоги рук.",
			questions: []Question{
				{ID: "car", Question: "Указано ли, что горит белая Toyota?"},
				{ID: "burns", Question: "Указано ли, что у водителя ожоги рук?"},
				{ID: "age", Question: "Указано ли, что водителю 54 года?"},
				{ID: "name", Question: "Указано ли ФИО водителя — Дюжев Юрий Петрович?"},
			},
			want: map[string]Answer{"car": AnswerYes, "burns": AnswerYes, "age": AnswerNo, "name": AnswerYes},
		},
		{
			name:        "toyota-fire/empty",
			description: "",
			questions: []Question{
				{ID: "car", Question: "Указано ли, что горит белая Toyota?"},
			},
			want: nil, // handled without a model call — see the empty-string branch below
		},
	}
}

// TestLiveDescriptionJudgeAgainstRealModel is the manual sanity check
// itself. It never fails the run on a disagreement — a single live
// sample is not a substitute for the prototype's own 132-description
// evaluation, and this project's own CLAUDE.md workflow explicitly
// treats needs_review/model disagreement as expected, not a bug to
// chase — but it does fail on a transport/schema/decode error, since
// that would mean this Go implementation itself is broken, not that the
// model merely disagreed.
func TestLiveDescriptionJudgeAgainstRealModel(t *testing.T) {
	h := Handler{Chat: llm.NewClient(liveJudgeURL())}
	model := liveJudgeModel()
	agree, total := 0, 0
	for _, c := range liveCases() {
		t.Run(c.name, func(t *testing.T) {
			if c.description == "" {
				t.Skip("empty description is handled by description.go without a model call — nothing to sanity-check here")
			}
			payload, err := json.Marshal(Request{Description: c.description, Questions: c.questions})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			raw, err := h.Answer(ctx, model, map[string]any{"temperature": 0.0, "max_tokens": 1024.0}, payload)
			if err != nil {
				t.Fatalf("Answer: %v (is `ollama serve` running with %q pulled?)", err, model)
			}
			var got map[string]Answer
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode answer: %v (raw=%s)", err, raw)
			}
			for id, want := range c.want {
				total++
				if got[id] == want {
					agree++
				} else {
					t.Logf("%s: question %q got %q, expected %q (model disagreement, not a plumbing failure)", c.name, id, got[id], want)
				}
			}
		})
	}
	t.Logf("live sanity check: %d/%d expected answers matched (informational only, not a pass/fail gate)", agree, total)
}
