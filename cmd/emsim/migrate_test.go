// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/migrate/main_test.go; adapted: run() renamed migrateRun() to avoid
// colliding with the dispatcher's own run() in main.go/main_test.go.
package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	pgstore "emsim/internal/platform/postgres"
)

func TestMigrateRunRejectsInvalidInvocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		databaseURL string
		want        error
	}{
		{name: "missing command", want: errMigrateCommandRequired},
		{name: "too many commands", args: []string{"up", "down"}, want: errMigrateUnknownCommand},
		{name: "unknown command", args: []string{"status"}, want: errMigrateUnknownCommand},
		{name: "missing URL", args: []string{"up"}, want: pgstore.ErrDatabaseURLRequired},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := migrateRun(context.Background(), test.args, test.databaseURL, &bytes.Buffer{})
			if !errors.Is(err, test.want) {
				t.Fatalf("migrateRun() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMigrateRunDoesNotExposeDatabaseURL(t *testing.T) {
	t.Parallel()

	credential := "private-value"
	databaseURL := "postgres://user:" + credential + "@%gh:5432/db"
	err := migrateRun(context.Background(), []string{"version"}, databaseURL, &bytes.Buffer{})
	if err == nil {
		t.Fatal("migrateRun() error = nil, want invalid configuration")
	}
	if strings.Contains(err.Error(), databaseURL) || strings.Contains(err.Error(), credential) {
		t.Fatalf("migrateRun() exposed database URL: %v", err)
	}
}
