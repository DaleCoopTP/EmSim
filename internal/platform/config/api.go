// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/config/api.go; adapted: no API_KEY — core's API was a single
// endpoint behind one shared key; emsim's API has no routes of its own yet
// (auth/content/training/assessment/reporting each add theirs later), and
// real authentication is cookie sessions with roles (ADR-008), not an API
// key. The pattern — fail fast at startup on invalid configuration,
// distinct public/admin listen addresses — is unchanged.
package config

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

var ErrInvalidAPIConfiguration = errors.New("invalid API configuration")

type API struct {
	DatabaseURL string
	PublicAddr  string
	AdminAddr   string
}

func APIFromEnvironment(lookup func(string) string) (API, error) {
	config := API{
		DatabaseURL: strings.TrimSpace(lookup("DATABASE_URL")),
		PublicAddr:  strings.TrimSpace(lookup("API_LISTEN_ADDR")),
		AdminAddr:   strings.TrimSpace(lookup("ADMIN_LISTEN_ADDR")),
	}
	if err := config.Validate(); err != nil {
		return API{}, err
	}
	return config, nil
}

func (c API) Validate() error {
	if c.DatabaseURL == "" || !validListenAddress(c.PublicAddr) || !validListenAddress(c.AdminAddr) || c.PublicAddr == c.AdminAddr {
		return ErrInvalidAPIConfiguration
	}
	return nil
}

func validListenAddress(address string) bool {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	value, err := strconv.ParseUint(port, 10, 16)
	return err == nil && value > 0
}
