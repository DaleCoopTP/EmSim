package config

import (
	"errors"
	"testing"
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
