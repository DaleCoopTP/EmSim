package config

import (
	"errors"
	"testing"
	"time"
)

func TestAPIFromEnvironmentRequiresSafeCompleteConfiguration(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL":      "postgres://example.invalid/emsim",
		"API_LISTEN_ADDR":   "127.0.0.1:8080",
		"ADMIN_LISTEN_ADDR": "127.0.0.1:8081",
	}
	lookup := func(name string) string { return values[name] }
	if _, err := APIFromEnvironment(lookup); err != nil {
		t.Fatalf("APIFromEnvironment() error = %v", err)
	}
	for name, value := range map[string]string{
		"DATABASE_URL": "", "API_LISTEN_ADDR": "bad-address", "ADMIN_LISTEN_ADDR": "127.0.0.1:8080",
		"SESSION_TTL": "not-a-duration", "COOKIE_SECURE": "not-a-bool",
	} {
		original := values[name]
		values[name] = value
		_, err := APIFromEnvironment(lookup)
		if !errors.Is(err, ErrInvalidAPIConfiguration) {
			t.Fatalf("APIFromEnvironment() with %s=%q error = %v", name, value, err)
		}
		values[name] = original
	}
}

func TestAPIFromEnvironmentSessionAndCookieDefaults(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL":      "postgres://example.invalid/emsim",
		"API_LISTEN_ADDR":   "127.0.0.1:8080",
		"ADMIN_LISTEN_ADDR": "127.0.0.1:8081",
	}
	lookup := func(name string) string { return values[name] }

	config, err := APIFromEnvironment(lookup)
	if err != nil {
		t.Fatalf("APIFromEnvironment() error = %v", err)
	}
	if config.SessionTTL != 12*time.Hour {
		t.Fatalf("SessionTTL = %v, want 12h (ADR-008 default)", config.SessionTTL)
	}
	if !config.CookieSecure {
		t.Fatal("CookieSecure = false, want true by default")
	}
}

func TestAPIFromEnvironmentSessionAndCookieOverrides(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL":      "postgres://example.invalid/emsim",
		"API_LISTEN_ADDR":   "127.0.0.1:8080",
		"ADMIN_LISTEN_ADDR": "127.0.0.1:8081",
		"SESSION_TTL":       "30m",
		"COOKIE_SECURE":     "false",
	}
	lookup := func(name string) string { return values[name] }

	config, err := APIFromEnvironment(lookup)
	if err != nil {
		t.Fatalf("APIFromEnvironment() error = %v", err)
	}
	if config.SessionTTL != 30*time.Minute {
		t.Fatalf("SessionTTL = %v, want 30m", config.SessionTTL)
	}
	if config.CookieSecure {
		t.Fatal("CookieSecure = true, want false (compose demo profile override)")
	}
}

// TestAPIFromEnvironmentAssessmentJudgeDefaultsToLLM is ADR-029's api
// half of the worker's own default: nothing set freezes operator112/
// rubric-v3 into new lessons, ASSESSMENT_JUDGE=off keeps rubric-v2, and
// an unknown value is rejected up front.
func TestAPIFromEnvironmentAssessmentJudgeDefaultsToLLM(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL":      "postgres://example.invalid/emsim",
		"API_LISTEN_ADDR":   "127.0.0.1:8080",
		"ADMIN_LISTEN_ADDR": "127.0.0.1:8081",
	}
	lookup := func(name string) string { return values[name] }
	config, err := APIFromEnvironment(lookup)
	if err != nil {
		t.Fatalf("APIFromEnvironment() error = %v", err)
	}
	if config.AssessmentJudge != AssessmentJudgeLLM {
		t.Fatalf("AssessmentJudge default = %q, want %q", config.AssessmentJudge, AssessmentJudgeLLM)
	}
	values["ASSESSMENT_JUDGE"] = "off"
	if config, err := APIFromEnvironment(lookup); err != nil || config.AssessmentJudge != AssessmentJudgeOff {
		t.Fatalf("APIFromEnvironment() with ASSESSMENT_JUDGE=off = %+v, %v", config, err)
	}
	values["ASSESSMENT_JUDGE"] = "gpt5"
	if _, err := APIFromEnvironment(lookup); !errors.Is(err, ErrInvalidAPIConfiguration) {
		t.Fatalf("unknown ASSESSMENT_JUDGE error = %v", err)
	}
}

func TestAPIFromEnvironmentRejectsNonPositiveSessionTTL(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL":      "postgres://example.invalid/emsim",
		"API_LISTEN_ADDR":   "127.0.0.1:8080",
		"ADMIN_LISTEN_ADDR": "127.0.0.1:8081",
		"SESSION_TTL":       "0s",
	}
	lookup := func(name string) string { return values[name] }
	if _, err := APIFromEnvironment(lookup); !errors.Is(err, ErrInvalidAPIConfiguration) {
		t.Fatalf("APIFromEnvironment() with SESSION_TTL=0s error = %v, want ErrInvalidAPIConfiguration", err)
	}
}
