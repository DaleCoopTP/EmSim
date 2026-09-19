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
}

func APIFromEnvironment(lookup func(string) string) (API, error) {
	sessionTTL, ttlErr := parseDurationOrDefault(lookup("SESSION_TTL"), defaultSessionTTL)
	cookieSecure, secureErr := parseBoolOrDefault(lookup("COOKIE_SECURE"), true)
	config := API{
		DatabaseURL:  strings.TrimSpace(lookup("DATABASE_URL")),
		PublicAddr:   strings.TrimSpace(lookup("API_LISTEN_ADDR")),
		AdminAddr:    strings.TrimSpace(lookup("ADMIN_LISTEN_ADDR")),
		SessionTTL:   sessionTTL,
		CookieSecure: cookieSecure,
	}
	if ttlErr != nil || secureErr != nil {
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
