package config

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidAPIConfiguration = errors.New("invalid API configuration")

// defaultSessionTTL and defaultCookieSecure are ADR-008's baseline: a 12h
// session TTL and a cookie marked Secure (only sent over TLS). Compose's
// demo profile — no Caddy in front, plain HTTP — overrides COOKIE_SECURE
// to false so the browser still sends the cookie back.
const defaultSessionTTL = 12 * time.Hour

type API struct {
	DatabaseURL  string
	PublicAddr   string
	AdminAddr    string
	SessionTTL   time.Duration
	CookieSecure bool
	// AssessmentJudge (ADR-028) is api's own half of ASSESSMENT_JUDGE —
	// the same enum Worker.AssessmentJudge reads, read here only to
	// decide which rubric version (content.RubricVersionForJudge, both
	// exercise types since ADR-034) a newly created lesson/preview run
	// freezes at CreateLesson/StartPreview time. It never wires an LLM
	// client here: the api process never calls assessment.Service.Handle,
	// only the worker does. Unset means AssessmentJudgeLLM since ADR-029
	// (a stock `docker compose up` ships the judge's model, so new
	// lessons freeze rubric-v3 — dds/rubric-v3 for DDS too since
	// ADR-034); ASSESSMENT_JUDGE=off keeps freezing rubric-v2 and must be set on the worker the same way.
	AssessmentJudge string
	// CallerWarmup and CallerOpeningDelay are ADR-029's api-side AI
	// caller settings (training.CallerTiming): CALLER_WARMUP (default
	// true) enqueues caller.warmup at answer_incoming and at the first
	// operator message; CALLER_OPENING_DELAY (default 0, at most 10s)
	// holds back the first reply, the scenario's no-model opening.
	CallerWarmup       bool
	CallerOpeningDelay time.Duration
	// Dictation (ADR-037) is the operator 112 voice-input settings; the
	// zero engine value is normalised to DictationOff.
	Dictation Dictation
}

// Dictation engines (ADR-037, DICTATION). DictationStub is a fixed-text
// engine for e2e and offline development, like CALLER_REPLIER=stub.
const (
	DictationOff     = "off"
	DictationWhisper = "whisper"
	DictationStub    = "stub"
)

const (
	defaultDictationLanguage    = "ru"
	defaultDictationModel       = "ggml-small"
	defaultDictationTimeout     = 15 * time.Second
	defaultDictationQueueWait   = 5 * time.Second
	defaultDictationConcurrency = 2
	defaultDictationMaxSeconds  = 30
)

// Dictation configures POST /items/{id}/dictation. STTURL is required
// only for DictationWhisper — a whisper-server base URL (compose's own
// http://stt:8080). Model is only a label carried into the response.
type Dictation struct {
	Engine      string
	STTURL      string
	Language    string
	Model       string
	Timeout     time.Duration
	QueueWait   time.Duration
	Concurrency int
	MaxSeconds  int
}

func dictationFromEnvironment(lookup func(string) string) (Dictation, bool) {
	engine := strings.TrimSpace(lookup("DICTATION"))
	if engine == "" {
		engine = DictationOff
	}
	language := strings.TrimSpace(lookup("STT_LANGUAGE"))
	if language == "" {
		language = defaultDictationLanguage
	}
	model := strings.TrimSpace(lookup("STT_MODEL"))
	if model == "" {
		model = defaultDictationModel
	}
	timeout, err1 := parseDurationOrDefault(lookup("STT_TIMEOUT"), defaultDictationTimeout)
	queueWait, err2 := parseDurationOrDefault(lookup("DICTATION_QUEUE_WAIT"), defaultDictationQueueWait)
	concurrency, err3 := parseIntOrDefault(lookup("DICTATION_CONCURRENCY"), defaultDictationConcurrency)
	maxSeconds, err4 := parseIntOrDefault(lookup("DICTATION_MAX_SECONDS"), defaultDictationMaxSeconds)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return Dictation{}, false
	}
	return Dictation{
		Engine: engine, STTURL: strings.TrimSpace(lookup("STT_URL")), Language: language, Model: model,
		Timeout: timeout, QueueWait: queueWait, Concurrency: concurrency, MaxSeconds: maxSeconds,
	}, true
}

func (d Dictation) validate() bool {
	switch d.Engine {
	case DictationOff:
		return true
	case DictationStub, DictationWhisper:
	default:
		return false
	}
	if d.Engine == DictationWhisper && d.STTURL == "" {
		return false
	}
	return d.Timeout > 0 && d.Timeout <= 2*time.Minute &&
		d.QueueWait >= 0 && d.QueueWait <= 30*time.Second &&
		d.Concurrency >= 1 && d.Concurrency <= 8 &&
		d.MaxSeconds >= 5 && d.MaxSeconds <= 60
}

func APIFromEnvironment(lookup func(string) string) (API, error) {
	sessionTTL, ttlErr := parseDurationOrDefault(lookup("SESSION_TTL"), defaultSessionTTL)
	cookieSecure, secureErr := parseBoolOrDefault(lookup("COOKIE_SECURE"), true)
	assessmentJudge := strings.TrimSpace(lookup("ASSESSMENT_JUDGE"))
	if assessmentJudge == "" {
		assessmentJudge = AssessmentJudgeLLM
	}
	callerWarmup, callerOpeningDelay, callerOK := callerTimingFromEnvironment(lookup)
	dictation, dictationOK := dictationFromEnvironment(lookup)
	config := API{
		DatabaseURL:     strings.TrimSpace(lookup("DATABASE_URL")),
		PublicAddr:      strings.TrimSpace(lookup("API_LISTEN_ADDR")),
		AdminAddr:       strings.TrimSpace(lookup("ADMIN_LISTEN_ADDR")),
		SessionTTL:      sessionTTL,
		CookieSecure:    cookieSecure,
		AssessmentJudge: assessmentJudge,
		CallerWarmup:    callerWarmup, CallerOpeningDelay: callerOpeningDelay,
		Dictation: dictation,
	}
	if ttlErr != nil || secureErr != nil || !callerOK || !dictationOK {
		return API{}, ErrInvalidAPIConfiguration
	}
	if err := config.Validate(); err != nil {
		return API{}, err
	}
	return config, nil
}

func (c API) Validate() error {
	if c.DatabaseURL == "" || !validListenAddress(c.PublicAddr) || !validListenAddress(c.AdminAddr) ||
		c.PublicAddr == c.AdminAddr || c.SessionTTL <= 0 {
		return ErrInvalidAPIConfiguration
	}
	if c.AssessmentJudge != AssessmentJudgeOff && c.AssessmentJudge != AssessmentJudgeLLM {
		return ErrInvalidAPIConfiguration
	}
	if c.Dictation.Engine != "" && !c.Dictation.validate() {
		return ErrInvalidAPIConfiguration
	}
	return nil
}

// parseDurationOrDefault returns fallback for an unset/blank env var, the
// parsed duration for a valid one, or an error for a set-but-unparseable
// one — a mistyped SESSION_TTL should fail startup, not silently fall
// back (CLAUDE.md-wide convention: "fail fast at startup on invalid
// configuration").
func parseDurationOrDefault(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return time.ParseDuration(raw)
}

func parseBoolOrDefault(raw string, fallback bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseBool(raw)
}

func validListenAddress(address string) bool {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	value, err := strconv.ParseUint(port, 10, 16)
	return err == nil && value > 0
}
