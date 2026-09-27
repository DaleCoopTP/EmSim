package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"emsim/internal/assessment/operator112/descjudge"
	"emsim/internal/content"
	"emsim/internal/platform/config"
	"emsim/internal/platform/llm"
	"emsim/internal/training"
	"emsim/internal/training/operator112"
	"emsim/internal/training/operator112/aicaller"
)

// bench-llm is the W0 measurement tool of slice-112-5b-plan.md's stage 2
// (ADR-029): it drives the configured model with the load a class
// actually produces — virtual operators holding multi-turn free-text
// dialogues over the AI seed scenarios through the real aicaller.Replier,
// optionally with description-judge requests through the real
// descjudge.Handler on the side — and reports, per concurrency level,
// how long the caller's replies took. It runs inside the compose network
// with the worker's own environment, so it measures exactly the
// endpoint, model and generation parameters the worker uses:
//
//	docker compose run --rm worker bench-llm --concurrency 1,4,8,20 --duration 2m
//
// It never touches the database.

var errBenchUsage = errors.New("bench-llm: invalid arguments")

// benchOperatorLines is one virtual operator's side of a dialogue: an
// opening line (answered by the scenario's own opening, no model), then
// the questions a trainee typically asks. Some match a scenario's
// answer_variants and are answered without the model, exactly as in a
// real lesson; the report counts those separately.
var benchOperatorLines = []string{
	"Алло, служба 112. Что у вас случилось?",
	"Расскажите подробнее, что именно произошло?",
	"Назовите точный адрес, где это случилось.",
	"Есть ли пострадавшие? Сколько человек?",
	"Как вас зовут и кем вы приходитесь пострадавшим?",
	"Что происходит прямо сейчас? Есть ли опасность для вас?",
	"Есть рядом какие-нибудь ориентиры, чтобы бригада быстрее нашла место?",
	"Хорошо, помощь уже направлена. Оставайтесь на месте и не кладите трубку.",
}

// benchCallCap bounds one measured call. It is deliberately far above
// CALLER_REPLY_TIMEOUT: the point is to see how slow replies really get,
// not to cut them off; caller.reply's own task lease is the same 2m.
const benchCallCap = 2 * time.Minute

type benchOptions struct {
	levels     []int
	duration   time.Duration
	think      time.Duration
	judgeLoops int
	scenarios  string
	jsonPath   string
}

type benchScenario struct {
	key       string
	facts     []content.Intake112Fact
	caller    *content.Intake112CallerProfile
	questions []descjudge.Question
}

type latencyStats struct {
	Count     int     `json:"count"`
	Errors    int     `json:"errors"`
	PerMinute float64 `json:"per_minute"`
	P50       float64 `json:"p50_s"`
	P95       float64 `json:"p95_s"`
	P99       float64 `json:"p99_s"`
	Max       float64 `json:"max_s"`
}

type serverStats struct {
	PredictedTokensPerSecond float64 `json:"predicted_tokens_per_s"`
	PromptTokensPerSecond    float64 `json:"prompt_tokens_per_s"`
	MaxProcessing            float64 `json:"max_requests_processing"`
	MaxDeferred              float64 `json:"max_requests_deferred"`
}

type benchLevel struct {
	Concurrency      int          `json:"concurrency"`
	JudgeLoops       int          `json:"judge_loops"`
	Seconds          float64      `json:"seconds"`
	Caller           latencyStats `json:"caller_model_replies"`
	NoModelReplies   int          `json:"caller_no_model_replies"`
	OverTimeout      int          `json:"caller_over_timeout"`
	OverTimeoutShare float64      `json:"caller_over_timeout_share"`
	Judge            latencyStats `json:"judge"`
	Server           *serverStats `json:"server,omitempty"`
}

type benchReport struct {
	CallerURL          string       `json:"caller_url"`
	CallerModel        string       `json:"caller_model"`
	CallerReplyTimeout string       `json:"caller_reply_timeout"`
	Think              string       `json:"think"`
	Scenarios          []string     `json:"scenarios"`
	Levels             []benchLevel `json:"levels"`
}

func runBenchLLM(ctx context.Context, args []string) error {
	opts, err := parseBenchOptions(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	processConfig, err := config.WorkerFromEnvironment(os.Getenv, "worker")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench-llm: worker configuration: %v\n", err)
		return err
	}
	if processConfig.CallerReplier != config.CallerReplierLLM {
		fmt.Fprintln(os.Stderr, "bench-llm: CALLER_REPLIER must be llm")
		return errBenchUsage
	}
	if opts.judgeLoops > 0 && processConfig.AssessmentJudge != config.AssessmentJudgeLLM {
		fmt.Fprintln(os.Stderr, "bench-llm: --judge needs ASSESSMENT_JUDGE=llm")
		return errBenchUsage
	}
	scenarios, err := loadBenchScenarios(opts.scenarios)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench-llm: %v\n", err)
		return err
	}
	report, err := benchRun(ctx, opts, processConfig, scenarios, os.Stderr)
	if err != nil {
		return err
	}
	printBenchTable(os.Stdout, report)
	return writeBenchJSON(opts.jsonPath, os.Stdout, report)
}

func parseBenchOptions(args []string, stderr io.Writer) (benchOptions, error) {
	fs := flag.NewFlagSet("bench-llm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	levels := fs.String("concurrency", "1,2,4", "comma-separated numbers of simultaneous dialogues, one measurement per value")
	duration := fs.Duration("duration", 2*time.Minute, "measurement time per concurrency level")
	think := fs.Duration("think", 5*time.Second, "operator pause between receiving a reply and sending the next message")
	judge := fs.Int("judge", 0, "simultaneous description-judge request loops running alongside the dialogues")
	scenarios := fs.String("scenarios", "/app/seed/scenarios", "directory with the pilot-112-ai-*.json seed scenarios")
	jsonPath := fs.String("json", "", `also write the report as JSON to this file ("-" for stdout)`)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return benchOptions{}, flag.ErrHelp
		}
		return benchOptions{}, errBenchUsage
	}
	if fs.NArg() != 0 || *duration <= 0 || *think < 0 || *judge < 0 {
		fs.Usage()
		return benchOptions{}, errBenchUsage
	}
	opts := benchOptions{duration: *duration, think: *think, judgeLoops: *judge, scenarios: *scenarios, jsonPath: *jsonPath}
	for _, part := range strings.Split(*levels, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 {
			fmt.Fprintf(stderr, "bench-llm: invalid --concurrency value %q\n", part)
			return benchOptions{}, errBenchUsage
		}
		opts.levels = append(opts.levels, n)
	}
	return opts, nil
}

// loadBenchScenarios reads every pilot-112-ai-*.json file in dir that has
// a caller profile, keeping only the newest version of each scenario key.
func loadBenchScenarios(dir string) ([]benchScenario, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "pilot-112-ai-*.json"))
	if err != nil {
		return nil, err
	}
	newest := map[string]int{}
	byKey := map[string]benchScenario{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		_, file, err := content.DecodeFile(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		intake := file.Body.Intake112
		if intake == nil || intake.Dialogue == nil || intake.Dialogue.Caller == nil || file.Version < newest[file.Key] {
			continue
		}
		scenario := benchScenario{key: file.Key, facts: intake.Dialogue.Facts, caller: intake.Dialogue.Caller}
		for _, q := range intake.Reference.DescriptionQuestions {
			scenario.questions = append(scenario.questions, descjudge.Question{ID: q.ID, Question: q.Question})
		}
		newest[file.Key] = file.Version
		byKey[file.Key] = scenario
	}
	if len(byKey) == 0 {
		return nil, fmt.Errorf("no AI caller scenarios (pilot-112-ai-*.json with a caller profile) in %s", dir)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	scenarios := make([]benchScenario, 0, len(keys))
	for _, key := range keys {
		scenarios = append(scenarios, byKey[key])
	}
	return scenarios, nil
}

// timedChat measures every model call aicaller.Replier makes; no-model
// replies (opening, scripted answer_variants) never reach it.
type timedChat struct {
	inner    aicaller.ChatCompleter
	recorder *latencyRecorder
}

func (c timedChat) Complete(ctx context.Context, req llm.Request) (llm.Result, error) {
	start := time.Now()
	result, err := c.inner.Complete(ctx, req)
	c.recorder.add(time.Since(start), err)
	return result, err
}

type latencyRecorder struct {
	mu        sync.Mutex
	latencies []time.Duration
	errors    int
}

func (r *latencyRecorder) add(d time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.errors++
		return
	}
	r.latencies = append(r.latencies, d)
}

func (r *latencyRecorder) stats(elapsed time.Duration) latencyStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	sorted := append([]time.Duration(nil), r.latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	s := latencyStats{Count: len(sorted), Errors: r.errors}
	if elapsed > 0 {
		s.PerMinute = round2(float64(len(sorted)) / elapsed.Minutes())
	}
	if len(sorted) > 0 {
		s.P50 = percentile(sorted, 50).Seconds()
		s.P95 = percentile(sorted, 95).Seconds()
		s.P99 = percentile(sorted, 99).Seconds()
		s.Max = sorted[len(sorted)-1].Seconds()
	}
	s.P50, s.P95, s.P99, s.Max = round2(s.P50), round2(s.P95), round2(s.P99), round2(s.Max)
	return s
}

func (r *latencyRecorder) over(limit time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, d := range r.latencies {
		if d > limit {
			n++
		}
	}
	return n
}

// percentile is the nearest-rank percentile of an ascending slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func benchRun(ctx context.Context, opts benchOptions, cfg config.Worker, scenarios []benchScenario, progress io.Writer) (benchReport, error) {
	report := benchReport{
		CallerURL: cfg.CallerLLMURL, CallerModel: cfg.CallerLLMModel,
		CallerReplyTimeout: cfg.CallerReplyTimeout.String(), Think: opts.think.String(),
	}
	for _, s := range scenarios {
		report.Scenarios = append(report.Scenarios, s.key)
	}
	metricsURL := strings.TrimSuffix(strings.TrimSuffix(cfg.CallerLLMURL, "/"), "/v1") + "/metrics"
	for i, level := range opts.levels {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if i > 0 {
			// Let the server drain the previous level's tail first.
			select {
			case <-ctx.Done():
				return report, ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
		fmt.Fprintf(progress, "bench-llm: %d dialogue(s), %d judge loop(s), %s…\n", level, opts.judgeLoops, opts.duration)
		report.Levels = append(report.Levels, benchLevelRun(ctx, opts, cfg, scenarios, level, metricsURL))
	}
	return report, nil
}

func benchLevelRun(ctx context.Context, opts benchOptions, cfg config.Worker, scenarios []benchScenario, level int, metricsURL string) benchLevel {
	callerRec, judgeRec := &latencyRecorder{}, &latencyRecorder{}
	replier := aicaller.Replier{
		Chat:  timedChat{inner: llm.NewClient(cfg.CallerLLMURL), recorder: callerRec},
		Model: cfg.CallerLLMModel, Temperature: cfg.CallerTemperature, TopP: cfg.CallerTopP,
		RepeatPenalty: cfg.CallerRepeatPenalty, MaxTokens: cfg.CallerMaxTokens,
		Stub: operator112.StubCallerReplier{},
	}
	levelCtx, cancel := context.WithTimeout(ctx, opts.duration)
	defer cancel()

	sampler := startMetricsSampler(metricsURL)
	start := time.Now()
	var noModel int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < level; i++ {
		wg.Add(1)
		go func(session int) {
			defer wg.Done()
			// Stagger the sessions so they do not all send in lockstep.
			if !benchSleep(levelCtx, opts.think*time.Duration(session)/time.Duration(level)) {
				return
			}
			n := benchDialogues(levelCtx, replier, scenarios, session, opts.think)
			mu.Lock()
			noModel += n
			mu.Unlock()
		}(i)
	}
	judged := judgeScenarios(scenarios)
	for i := 0; i < opts.judgeLoops && len(judged) > 0; i++ {
		wg.Add(1)
		go func(loop int) {
			defer wg.Done()
			benchJudgeLoop(levelCtx, cfg, judged, loop, opts.think, judgeRec)
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	server := sampler.stop(elapsed)

	result := benchLevel{
		Concurrency: level, JudgeLoops: opts.judgeLoops, Seconds: round2(elapsed.Seconds()),
		Caller: callerRec.stats(elapsed), NoModelReplies: noModel,
		OverTimeout: callerRec.over(cfg.CallerReplyTimeout), Judge: judgeRec.stats(elapsed), Server: server,
	}
	if result.Caller.Count > 0 {
		result.OverTimeoutShare = round2(float64(result.OverTimeout) / float64(result.Caller.Count))
	}
	return result
}

// benchDialogues runs one virtual operator until ctx is done: dialogue
// after dialogue, cycling through the scenarios from a per-session
// offset. A reply already in flight when ctx ends is still awaited and
// counted — the level's report covers every request it started. It
// returns how many replies were answered without the model.
func benchDialogues(ctx context.Context, replier aicaller.Replier, scenarios []benchScenario, session int, think time.Duration) int {
	noModel := 0
	for n := session; ; n++ {
		scenario := scenarios[n%len(scenarios)]
		var transcript []training.IntakeLine
		for turn, text := range benchOperatorLines {
			if ctx.Err() != nil {
				return noModel
			}
			transcript = append(transcript, training.IntakeLine{Speaker: "operator", Text: text, ServerAt: time.Now()})
			callCtx, cancel := context.WithTimeout(context.Background(), benchCallCap)
			reply, err := replier.Reply(callCtx, operator112.CallerReplyRequest{
				Facts: scenario.facts, Caller: scenario.caller, Transcript: transcript, Turn: turn + 1,
			})
			cancel()
			if err != nil {
				reply = replier.Fallback(operator112.CallerReplyRequest{})
			} else if reply.Source != training.CallerTurnSourceModel {
				noModel++
			}
			transcript = append(transcript, training.IntakeLine{Speaker: "caller", Text: reply.Text, Reveals: reply.Reveals, ServerAt: time.Now()})
			if !benchSleep(ctx, think) {
				return noModel
			}
		}
	}
}

// judgeScenarios keeps the scenarios that have description questions and
// builds each one a plausible description from its facts' statements.
func judgeScenarios(scenarios []benchScenario) []descjudge.Request {
	var requests []descjudge.Request
	for _, s := range scenarios {
		if len(s.questions) == 0 {
			continue
		}
		var statements []string
		for _, fact := range s.facts {
			if fact.Statement != "" {
				statements = append(statements, fact.Statement)
			}
		}
		requests = append(requests, descjudge.Request{Description: strings.Join(statements, " "), Questions: s.questions})
	}
	return requests
}

func benchJudgeLoop(ctx context.Context, cfg config.Worker, requests []descjudge.Request, loop int, think time.Duration, rec *latencyRecorder) {
	handler := descjudge.Handler{Chat: llm.NewClient(cfg.JudgeLLMURL)}
	parameters := map[string]any{"temperature": 0, "max_tokens": cfg.JudgeMaxTokens}
	for n := loop; ctx.Err() == nil; n++ {
		payload, err := json.Marshal(requests[n%len(requests)])
		if err != nil {
			return
		}
		callCtx, cancel := context.WithTimeout(context.Background(), benchCallCap)
		start := time.Now()
		_, err = handler.Answer(callCtx, cfg.JudgeLLMModel, parameters, payload)
		cancel()
		rec.add(time.Since(start), err)
		if !benchSleep(ctx, think) {
			return
		}
	}
}

func benchSleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// metricsSampler reads llama-server's Prometheus /metrics once a second
// (--metrics, ADR-029). Against a server without it (Ollama) the level is
// reported without server stats.
type metricsSampler struct {
	mu            sync.Mutex
	first, last   map[string]float64
	maxProcessing float64
	maxDeferred   float64
	done          chan struct{}
	finished      chan struct{}
}

func startMetricsSampler(url string) *metricsSampler {
	s := &metricsSampler{done: make(chan struct{}), finished: make(chan struct{})}
	go func() {
		defer close(s.finished)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		// Sampling continues past the level's deadline until stop: replies
		// already in flight then still count towards the level.
		for {
			s.sample(url)
			select {
			case <-s.done:
				s.sample(url)
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

func (s *metricsSampler) sample(url string) {
	values, err := fetchLlamaMetrics(url)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first == nil {
		s.first = values
	}
	s.last = values
	s.maxProcessing = math.Max(s.maxProcessing, values["llamacpp:requests_processing"])
	s.maxDeferred = math.Max(s.maxDeferred, values["llamacpp:requests_deferred"])
}

func (s *metricsSampler) stop(elapsed time.Duration) *serverStats {
	close(s.done)
	<-s.finished
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first == nil || elapsed <= 0 {
		return nil
	}
	rate := func(name string) float64 {
		return round2((s.last[name] - s.first[name]) / elapsed.Seconds())
	}
	return &serverStats{
		PredictedTokensPerSecond: rate("llamacpp:tokens_predicted_total"),
		PromptTokensPerSecond:    rate("llamacpp:prompt_tokens_total"),
		MaxProcessing:            s.maxProcessing, MaxDeferred: s.maxDeferred,
	}
}

func fetchLlamaMetrics(url string) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parsePromText(string(body)), nil
}

// parsePromText reads the llamacpp:* samples of a Prometheus text
// exposition; llama-server's metrics carry no labels.
func parsePromText(text string) map[string]float64 {
	values := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "llamacpp:") {
			continue
		}
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
			values[fields[0]] = v
		}
	}
	return values
}

func printBenchTable(w io.Writer, report benchReport) {
	fmt.Fprintf(w, "model %s at %s, CALLER_REPLY_TIMEOUT %s, operator pause %s\n", report.CallerModel, report.CallerURL, report.CallerReplyTimeout, report.Think)
	fmt.Fprintf(w, "scenarios: %s\n\n", strings.Join(report.Scenarios, ", "))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "dialogues\treplies/min\tp50 s\tp95 s\tp99 s\tmax s\tover timeout\terrors\tno-model\tjudge p95 s\tjudge err\ttok/s out\ttok/s in\tmax queued\t")
	for _, l := range report.Levels {
		server := []string{"n/a", "n/a", "n/a"}
		if l.Server != nil {
			server = []string{fmtFloat(l.Server.PredictedTokensPerSecond), fmtFloat(l.Server.PromptTokensPerSecond), fmtFloat(l.Server.MaxDeferred)}
		}
		judgeP95, judgeErr := "-", "-"
		if l.JudgeLoops > 0 {
			judgeP95, judgeErr = fmtFloat(l.Judge.P95), strconv.Itoa(l.Judge.Errors)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%d (%.0f%%)\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t\n",
			l.Concurrency, fmtFloat(l.Caller.PerMinute), fmtFloat(l.Caller.P50), fmtFloat(l.Caller.P95),
			fmtFloat(l.Caller.P99), fmtFloat(l.Caller.Max), l.OverTimeout, l.OverTimeoutShare*100,
			l.Caller.Errors, l.NoModelReplies, judgeP95, judgeErr, server[0], server[1], server[2])
	}
	_ = tw.Flush()
}

func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func writeBenchJSON(path string, stdout io.Writer, report benchReport) error {
	if path == "" {
		return nil
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if path == "-" {
		_, err = stdout.Write(body)
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
