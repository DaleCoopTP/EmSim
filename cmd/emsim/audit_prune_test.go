package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"emsim/internal/platform/backup"
)

func writeCopy(t *testing.T, dir, name string, created time.Time) {
	t.Helper()
	copyDir := filepath.Join(dir, name)
	if err := os.MkdirAll(copyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(backup.Manifest{Format: backup.ManifestFormat, CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "manifest.json"), raw, 0o640); err != nil {
		t.Fatal(err)
	}
}

// TestRecentBackupExists (ADR-038): audit.prune may delete only while a
// complete copy no older than a day exists.
func TestRecentBackupExists(t *testing.T) {
	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	if recentBackupExists(dir, now) {
		t.Fatal("an empty directory counts as having a recent copy")
	}
	if recentBackupExists(filepath.Join(dir, "missing"), now) {
		t.Fatal("a missing directory counts as having a recent copy")
	}
	writeCopy(t, dir, "emsim-20260927-030000Z", now.Add(-49*time.Hour))
	if recentBackupExists(dir, now) {
		t.Fatal("a two-day-old copy counts as recent")
	}
	writeCopy(t, dir, "emsim-20260929-030000Z", now.Add(-time.Hour))
	if !recentBackupExists(dir, now) {
		t.Fatal("a copy made an hour ago does not count as recent")
	}
}
