// Live accuracy check against a real llama-server/Ollama instance — run
// manually, never part of the ordinary test suite:
//
//	go test -tags=llmlive -run Live -v ./internal/assessment/dds/commentjudge/
//
// with JUDGE_LLM_URL/JUDGE_LLM_MODEL set (defaults match the local
// Ollama setup ADR-028's descjudge live test uses). It labels ~40 fact
// questions (testdata/comments.json, hand-written from the DDS seed
// scenarios' own comment_facts plus deliberately wrong variants) and 16
// grammar comments, prints the agreement rate and per-request latency,
// and fails only below a deliberately low floor — the number to read is
// the printed one (ДДС-4 risk: telegraphic DDS comments were never
// measured the way the 112 description prototype's 94.8% was).
//
//go:build llmlive

package commentjudge

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"emsim/internal/platform/llm"
)

//go:embed testdata/comments.json
var liveDataset []byte

func liveClient(t *testing.T) (*llm.Client, string) {
	t.Helper()
	url := os.Getenv("JUDGE_LLM_URL")
	if url == "" {
		url = "http://127.0.0.1:11434/v1"
	}
	model := os.Getenv("JUDGE_LLM_MODEL")
	if model == "" {
		model = "t-tech/T-lite-it-2.1:q5_K_M"
	}
	return llm.NewClient(url), model
}

type liveFact struct {
	Status  string `json:"status"`
	Comment string `json:"comment"`
	Fact    string `json:"fact"`
	Want    Answer `json:"want"`
}

type liveGrammar struct {
	Comment   string `json:"comment"`
	HasErrors bool   `json:"has_errors"`
}

func TestLiveFactsAgreement(t *testing.T) {
	client, model := liveClient(t)
	var data struct {
		Facts []liveFact `json:"facts"`
	}
	if err := json.Unmarshal(liveDataset, &data); err != nil {
		t.Fatal(err)
	}
	agree, failures := 0, 0
	var total time.Duration
	for i, c := range data.Facts {
		question := "Комментарий сообщает: " + c.Fact
		if strings.HasPrefix(c.Fact, "Комментарий не противоречит") {
			question = c.Fact
		}
		payload, _ := json.Marshal(FactsRequest{
			Comments:  []Comment{{ID: "comment:1", Status: c.Status, Text: c.Comment}},
			Questions: []Question{{ID: "q", CommentID: "comment:1", Question: question}},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		started := time.Now()
		raw, err := FactsHandler{Chat: client}.Answer(ctx, model, nil, payload)
		cancel()
		total += time.Since(started)
		if err != nil {
			failures++
			t.Logf("case %d: judge error: %v", i, err)
			continue
		}
		var got map[string]Answer
		_ = json.Unmarshal(raw, &got)
		if got["q"] == c.Want {
			agree++
		} else {
			t.Logf("case %d disagrees: %q | %q -> got %s, want %s", i, c.Comment, c.Fact, got["q"], c.Want)
		}
	}
	n := len(data.Facts)
	t.Logf("facts agreement: %d/%d = %.1f%%, judge errors %d, mean latency %s", agree, n, 100*float64(agree)/float64(n), failures, total/time.Duration(n))
	if float64(agree)/float64(n) < 0.6 {
		t.Fatalf("agreement %d/%d is below the 60%% floor", agree, n)
	}
}

func TestLiveGrammarAgreement(t *testing.T) {
	client, model := liveClient(t)
	var data struct {
		Grammar []liveGrammar `json:"grammar"`
	}
	if err := json.Unmarshal(liveDataset, &data); err != nil {
		t.Fatal(err)
	}
	agree, failures := 0, 0
	for i, c := range data.Grammar {
		payload, _ := json.Marshal(GrammarRequest{Comments: []Comment{{ID: "comment:1", Text: c.Comment}}})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		raw, err := GrammarHandler{Chat: client}.Answer(ctx, model, nil, payload)
		cancel()
		if err != nil {
			failures++
			t.Logf("case %d: judge error: %v", i, err)
			continue
		}
		var got map[string][]GrammarError
		_ = json.Unmarshal(raw, &got)
		if (len(got["comment:1"]) > 0) == c.HasErrors {
			agree++
		} else {
			t.Logf("case %d disagrees: %q -> %d errors, want has_errors=%v", i, c.Comment, len(got["comment:1"]), c.HasErrors)
		}
	}
	n := len(data.Grammar)
	t.Logf("grammar agreement (has/has-not errors): %d/%d = %.1f%%, judge errors %d", agree, n, 100*float64(agree)/float64(n), failures)
	if float64(agree)/float64(n) < 0.5 {
		t.Fatalf("agreement %d/%d is below the 50%% floor", agree, n)
	}
}
