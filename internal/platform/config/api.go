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
