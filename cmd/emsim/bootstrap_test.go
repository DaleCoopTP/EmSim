package main

import (
	"context"
	"errors"
	"testing"

	pgstore "emsim/internal/platform/postgres"
)

// TestRunBootstrapAdminRejectsInvalidInvocation checks the validation
// steps that run before any network or database access, so this needs no
// PostgreSQL — the actual bootstrap effect is covered by
// test/integration/api_process_test.go against a real database. Every
// case clears the three env vars runBootstrapAdmin reads, since t.Setenv
// forbids t.Parallel() in this test and the ambient shell environment
// must not leak in.
func TestRunBootstrapAdminRejectsInvalidInvocation(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		login       string
		password    string
		databaseURL string
		want        error
	}{
		{name: "extra args", args: []string{"extra"}, want: errBootstrapTakesNoArgs},
		{name: "missing login and password", want: errBootstrapCredentialsRequired},
		{name: "missing password", login: "admin", want: errBootstrapCredentialsRequired},
		{name: "missing login", password: "correct-horse", want: errBootstrapCredentialsRequired},
		{name: "missing database URL", login: "admin", password: "correct-horse", want: pgstore.ErrDatabaseURLRequired},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BOOTSTRAP_ADMIN_LOGIN", test.login)
			t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", test.password)
			t.Setenv("DATABASE_URL", test.databaseURL)

			err := runBootstrapAdmin(context.Background(), test.args)
			if !errors.Is(err, test.want) {
				t.Fatalf("runBootstrapAdmin() error = %v, want %v", err, test.want)
			}
		})
	}
}
