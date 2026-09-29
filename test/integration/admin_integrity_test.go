//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type integrityStatus struct {
	Integrity *struct {
		CheckedAt time.Time `json:"checked_at"`
		OK        bool      `json:"ok"`
		Sections  []struct {
			Name     string   `json:"name"`
			Checked  int      `json:"checked"`
			Problems int      `json:"problems"`
			Examples []string `json:"examples"`
			Error    string   `json:"error"`
		} `json:"sections"`
	} `json:"integrity"`
}

func TestAdminIntegrityCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	f := newAdminFixture(t, ctx)
	admin := f.admin(t)
	f.createUser(t, admin, "int-instr", "instructor-password-1", "instructor")
	instructor := f.login(t, "int-instr", "instructor-password-1")

	seed := exec.CommandContext(ctx, f.binary, "import", "seed", "--actor", adminFixtureLogin, "../../seed")
	seed.Env = append(os.Environ(), "DATABASE_URL="+f.databaseURL, "BLOB_ROOT="+f.blobRoot)
	if output, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("import seed: %v\n%s", err, output)
	}
	good := []byte("integrity-fixture-blob")
	sum := sha256.Sum256(good)
	hexSum := hex.EncodeToString(sum[:])
	path := filepath.Join(f.blobRoot, hexSum[:2], hexSum[2:])
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, good, 0o640); err != nil {
		t.Fatal(err)
	}
	var blobID string
	if err := f.pool.QueryRow(ctx, `INSERT INTO blobs (id, sha256, mime, size) VALUES (gen_random_uuid(), $1, 'text/plain', $2) RETURNING id::text`, sum[:], len(good)).Scan(&blobID); err != nil {
		t.Fatal(err)
	}

	worker := startWorkerProcess(t, f.binary, f.databaseURL, "all", "int-worker", f.blobRoot)
	t.Cleanup(func() { worker.stop(t, false) })

	if response := jsonRequest(t, ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/admin/integrity", nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("instructor POST integrity = %d, want 403", response.StatusCode)
	}

	run := func() integrityStatus {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		var after time.Time
		for {
			after = time.Now().UTC()
			response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/integrity", nil, nil)
			if response.StatusCode == http.StatusAccepted {
				break
			}
			if response.StatusCode != http.StatusConflict || time.Now().After(deadline) {
				t.Fatalf("POST integrity = %d, want 202", response.StatusCode)
			}
			time.Sleep(100 * time.Millisecond)
		}
		for {
			var body integrityStatus
			jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/status", nil, &body)
			if body.Integrity != nil && body.Integrity.CheckedAt.After(after) {
				return body
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for the integrity report: %+v", body)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	section := func(s integrityStatus, name string) (checked, problems int, examples []string) {
		t.Helper()
		for _, sec := range s.Integrity.Sections {
			if sec.Name == name {
				if sec.Error != "" {
					t.Fatalf("section %s failed to run: %s", name, sec.Error)
				}
				return sec.Checked, sec.Problems, sec.Examples
			}
		}
		t.Fatalf("no section %s in %+v", name, s.Integrity.Sections)
		return
	}

	clean := run()
	if !clean.Integrity.OK {
		t.Fatalf("clean installation reported problems: %+v", clean.Integrity.Sections)
	}
	if checked, _, _ := section(clean, "blobs"); checked < 1 {
		t.Fatalf("blobs checked = %d, want at least the fixture", checked)
	}
	if checked, _, _ := section(clean, "scenario_versions"); checked < 1 {
		t.Fatalf("scenario_versions checked = %d, want the seed's", checked)
	}

	if err := os.WriteFile(path, []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	var versionID string
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE scenario_versions DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `UPDATE scenario_versions SET body = body || '{"tampered": true}'::jsonb
		WHERE id = (SELECT id FROM scenario_versions ORDER BY id LIMIT 1) RETURNING id::text`).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE scenario_versions ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	broken := run()
	if broken.Integrity.OK {
		t.Fatal("tampered data reported ok")
	}
	if _, problems, examples := section(broken, "blobs"); problems != 1 || len(examples) != 1 || examples[0] != blobID {
		t.Fatalf("blobs problems = %d %v, want only %s", problems, examples, blobID)
	}
	if _, problems, examples := section(broken, "scenario_versions"); problems != 1 || len(examples) != 1 || examples[0] != versionID {
		t.Fatalf("scenario_versions problems = %d %v, want only %s", problems, examples, versionID)
	}
	if _, problems, _ := section(broken, "evidence"); problems != 0 {
		t.Fatalf("evidence problems = %d, want 0", problems)
	}

	var errorRows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'integrity.check' AND outcome = 'error'`).Scan(&errorRows); err != nil {
		t.Fatal(err)
	}
	if errorRows != 1 {
		t.Fatalf("integrity.check error audit rows = %d, want 1", errorRows)
	}
}
