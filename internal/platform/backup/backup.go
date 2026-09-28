// Package backup makes and restores a full copy of an EmSim installation
// (ADR-033): the PostgreSQL database (pg_dump custom format) and the
// content-addressed blob store (BLOB_ROOT), with a manifest of checksums.
//
// A copy is a directory "emsim-YYYYMMDD-HHMMSSZ" (UTC) under the backup
// directory holding db.dump, blobs.tar.gz and manifest.json. It is built
// in a ".tmp-*" directory and renamed into place only when complete, so a
// directory without that prefix and with a manifest is always a whole
// copy. Keep bounds how many copies are retained.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ManifestFormat = "emsim-backup/v1"
	manifestName   = "manifest.json"
	dumpName       = "db.dump"
	blobsName      = "blobs.tar.gz"
	namePrefix     = "emsim-"
	tmpPrefix      = ".tmp-"
	nameLayout     = "20060102-150405Z"
	// staleTmpAge: a ".tmp-*" directory this old belongs to a run that
	// died; only one backup runs at a time (the single report slot).
	staleTmpAge = time.Hour
)

var (
	ErrNotConfigured = errors.New("backup is not configured")
	ErrDump          = errors.New("database dump failed")
	ErrBlobs         = errors.New("blob archive failed")
	ErrWrite         = errors.New("backup write failed")
	ErrManifest      = errors.New("backup manifest mismatch")
	ErrRestore       = errors.New("database restore failed")
	ErrSchemaNewer   = errors.New("backup schema is newer than this build")
)

// Config says where copies go and what they contain. PgDump/PgRestore
// default to the tools on PATH.
type Config struct {
	Dir         string
	BlobRoot    string
	DatabaseURL string
	Keep        int
	PgDump      string
	PgRestore   string
}

// File is one manifest entry.
type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest describes one complete copy.
type Manifest struct {
	Format        string    `json:"format"`
	CreatedAt     time.Time `json:"created_at"`
	SchemaVersion int64     `json:"schema_version"`
	BlobCount     int       `json:"blob_count"`
	Files         []File    `json:"files"`
}

// Copy is a complete copy found in the backup directory.
type Copy struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}

// Run makes one copy, then drops the oldest copies beyond cfg.Keep.
func Run(ctx context.Context, cfg Config, schemaVersion int64, now time.Time) (Copy, error) {
	if cfg.Dir == "" || cfg.BlobRoot == "" || cfg.DatabaseURL == "" || cfg.Keep < 1 {
		return Copy{}, ErrNotConfigured
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return Copy{}, ErrWrite
	}
	removeStaleTmp(cfg.Dir, now)
	tmp, err := os.MkdirTemp(cfg.Dir, tmpPrefix)
	if err != nil {
		return Copy{}, ErrWrite
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o750); err != nil {
		return Copy{}, ErrWrite
	}

	if err := dump(ctx, cfg, filepath.Join(tmp, dumpName)); err != nil {
		return Copy{}, err
	}
	blobCount, err := archiveBlobs(ctx, cfg.BlobRoot, filepath.Join(tmp, blobsName))
	if err != nil {
		return Copy{}, ErrBlobs
	}
	manifest := Manifest{Format: ManifestFormat, CreatedAt: now.UTC(), SchemaVersion: schemaVersion, BlobCount: blobCount}
	var total int64
	for _, name := range []string{dumpName, blobsName} {
		file, err := describe(filepath.Join(tmp, name))
		if err != nil {
			return Copy{}, ErrWrite
		}
		manifest.Files = append(manifest.Files, file)
		total += file.Size
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Copy{}, ErrWrite
	}
	if err := os.WriteFile(filepath.Join(tmp, manifestName), encoded, 0o640); err != nil {
		return Copy{}, ErrWrite
	}
	name := namePrefix + now.UTC().Format(nameLayout)
	// Two copies within one second (a scheduled and a manual one) get a
	// suffix instead of failing.
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(cfg.Dir, name)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s%s-%d", namePrefix, now.UTC().Format(nameLayout), n)
	}
	if err := os.Rename(tmp, filepath.Join(cfg.Dir, name)); err != nil {
		return Copy{}, ErrWrite
	}
	if err := Rotate(cfg.Dir, cfg.Keep); err != nil {
		return Copy{}, err
	}
	return Copy{Name: name, CreatedAt: manifest.CreatedAt, SizeBytes: total + int64(len(encoded))}, nil
}

// List returns the complete copies in dir, newest first. A missing dir
// has no copies.
func List(dir string) ([]Copy, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrWrite
	}
	var copies []Copy
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), namePrefix) {
			continue
		}
		manifest, err := ReadManifest(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		size := int64(0)
		for _, file := range manifest.Files {
			size += file.Size
		}
		copies = append(copies, Copy{Name: entry.Name(), CreatedAt: manifest.CreatedAt, SizeBytes: size})
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].Name > copies[j].Name })
	return copies, nil
}

// Rotate removes the oldest complete copies beyond keep.
func Rotate(dir string, keep int) error {
	copies, err := List(dir)
	if err != nil {
		return err
	}
	for i := keep; i < len(copies); i++ {
		if err := os.RemoveAll(filepath.Join(dir, copies[i].Name)); err != nil {
			return ErrWrite
		}
	}
	return nil
}

// ReadManifest reads a copy's manifest.
func ReadManifest(copyDir string) (Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(copyDir, manifestName))
	if err != nil {
		return Manifest{}, ErrManifest
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Format != ManifestFormat {
		return Manifest{}, ErrManifest
	}
	return manifest, nil
}

// Verify checks every file of a copy against its manifest.
func Verify(copyDir string) (Manifest, error) {
	manifest, err := ReadManifest(copyDir)
	if err != nil {
		return Manifest{}, err
	}
	names := map[string]bool{}
	for _, want := range manifest.Files {
		if want.Name != filepath.Base(want.Name) {
			return Manifest{}, ErrManifest
		}
		got, err := describe(filepath.Join(copyDir, want.Name))
		if err != nil || got != want {
			return Manifest{}, ErrManifest
		}
		names[want.Name] = true
	}
	if !names[dumpName] || !names[blobsName] {
		return Manifest{}, ErrManifest
	}
	return manifest, nil
}

func removeStaleTmp(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), tmpPrefix) {
			continue
		}
		info, err := entry.Info()
		if err == nil && now.Sub(info.ModTime()) > staleTmpAge {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}

func describe(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return File{}, err
	}
	return File{Name: filepath.Base(path), SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}

// pgEnvironment turns a postgres:// URL into libpq PG* variables, so the
// password never appears on a command line.
func pgEnvironment(databaseURL string) ([]string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return nil, ErrNotConfigured
	}
	env := []string{"PGHOST=" + parsed.Hostname(), "PGDATABASE=" + strings.TrimPrefix(parsed.Path, "/")}
	if port := parsed.Port(); port != "" {
		env = append(env, "PGPORT="+port)
	}
	if parsed.User != nil {
		env = append(env, "PGUSER="+parsed.User.Username())
		if password, ok := parsed.User.Password(); ok {
			env = append(env, "PGPASSWORD="+password)
		}
	}
	if mode := parsed.Query().Get("sslmode"); mode != "" {
		env = append(env, "PGSSLMODE="+mode)
	}
	return env, nil
}

func toolCommand(ctx context.Context, tool, fallback, databaseURL string, args ...string) (*exec.Cmd, error) {
	env, err := pgEnvironment(databaseURL)
	if err != nil {
		return nil, err
	}
	if tool == "" {
		tool = fallback
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd, nil
}

func dump(ctx context.Context, cfg Config, path string) error {
	cmd, err := toolCommand(ctx, cfg.PgDump, "pg_dump", cfg.DatabaseURL, "--format=custom", "--no-owner", "--no-privileges", "--file="+path)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", ErrDump, firstLine(stderr.String()))
	}
	return nil
}

// archiveBlobs writes every published blob (not .staging) into a gzip'd
// tar with paths relative to the blob root, in a stable order.
func archiveBlobs(ctx context.Context, root, path string) (int, error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	count := 0
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) && current == root {
				return filepath.SkipAll
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".staging" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o640, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, file)
		_ = file.Close()
		if err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	return count, out.Sync()
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}
