package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCopy(t *testing.T, dir, name string, created time.Time) {
	t.Helper()
	copyDir := filepath.Join(dir, name)
	if err := os.MkdirAll(copyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Format: ManifestFormat, CreatedAt: created}
	for _, file := range []string{dumpName, blobsName} {
		if err := os.WriteFile(filepath.Join(copyDir, file), []byte(file+name), 0o640); err != nil {
			t.Fatal(err)
		}
		described, err := describe(filepath.Join(copyDir, file))
		if err != nil {
			t.Fatal(err)
		}
		manifest.Files = append(manifest.Files, described)
	}
	manifest.SchemaVersion = 20
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, manifestName), raw, 0o640); err != nil {
		t.Fatal(err)
	}
}

// TestListAndRotateKeepNewestCompleteCopies: only directories with the
// copy prefix and a valid manifest count; an unfinished ".tmp-*" and a
// directory without a manifest are never taken for a copy, and rotation
// keeps the newest ones.
func TestListAndRotateKeepNewestCompleteCopies(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for day := 0; day < 5; day++ {
		created := base.AddDate(0, 0, day)
		writeCopy(t, dir, namePrefix+created.Format(nameLayout), created)
	}
	if err := os.MkdirAll(filepath.Join(dir, tmpPrefix+"unfinished"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, namePrefix+"broken"), 0o750); err != nil {
		t.Fatal(err)
	}
	copies, err := List(dir)
	if err != nil || len(copies) != 5 || copies[0].Name != namePrefix+"20260924-000000Z" {
		t.Fatalf("List = %+v, %v", copies, err)
	}
	if err := Rotate(dir, 3); err != nil {
		t.Fatal(err)
	}
	copies, _ = List(dir)
	if len(copies) != 3 || copies[2].Name != namePrefix+"20260922-000000Z" {
		t.Fatalf("after rotate: %+v", copies)
	}
	if _, err := os.Stat(filepath.Join(dir, tmpPrefix+"unfinished")); err != nil {
		t.Fatal("rotation must not touch an unfinished copy")
	}
}

// TestVerifyRejectsATamperedCopy: a changed byte in a copy's file fails
// the manifest check before anything is restored.
func TestVerifyRejectsATamperedCopy(t *testing.T) {
	dir := t.TempDir()
	name := namePrefix + "20260928-030000Z"
	writeCopy(t, dir, name, time.Now())
	if _, err := Verify(filepath.Join(dir, name)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, dumpName), []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(filepath.Join(dir, name)); err != ErrManifest {
		t.Fatalf("Verify tampered = %v", err)
	}
}

// TestArchiveBlobsRoundTripSkipsStaging: published blobs survive the
// archive/extract round trip byte for byte; staging files are left out.
func TestArchiveBlobsRoundTripSkipsStaging(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{"ab/cdef": "voice", "12/3456": "recording", ".staging/blob-1": "partial"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), blobsName)
	count, err := archiveBlobs(t.Context(), root, archive)
	if err != nil || count != 2 {
		t.Fatalf("archiveBlobs = %d, %v", count, err)
	}
	target := t.TempDir()
	if err := extractBlobs(archive, target); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"ab/cdef": "voice", "12/3456": "recording"} {
		got, err := os.ReadFile(filepath.Join(target, path))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, ".staging")); err == nil {
		t.Fatal("staging must not be archived")
	}
}

func TestPgEnvironmentKeepsPasswordOffTheCommandLine(t *testing.T) {
	env, err := pgEnvironment("postgres://emsim:s3cret@postgres:5432/emsim?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, " ")
	for _, want := range []string{"PGHOST=postgres", "PGPORT=5432", "PGUSER=emsim", "PGPASSWORD=s3cret", "PGDATABASE=emsim", "PGSSLMODE=disable"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, env)
		}
	}
	if _, err := Run(t.Context(), Config{}, 20, time.Now()); err != ErrNotConfigured {
		t.Fatalf("Run without config = %v", err)
	}
}
