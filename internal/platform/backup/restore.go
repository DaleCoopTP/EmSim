package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Restore replaces the database and the blob store with a verified copy
// (ADR-033). The api and worker must be stopped: this is not an online
// operation. resetDatabase empties the target database (the caller owns
// the connection) so a copy made on an older schema restores cleanly and
// `emsim migrate up` then brings it forward. A copy from a newer schema
// than maxSchema is refused: this build could not run on it.
func Restore(ctx context.Context, cfg Config, copyDir string, maxSchema int64, resetDatabase func(context.Context) error) (Manifest, error) {
	if cfg.BlobRoot == "" || cfg.DatabaseURL == "" || resetDatabase == nil {
		return Manifest{}, ErrNotConfigured
	}
	manifest, err := Verify(copyDir)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.SchemaVersion > maxSchema {
		return Manifest{}, ErrSchemaNewer
	}
	if err := resetDatabase(ctx); err != nil {
		return Manifest{}, ErrRestore
	}
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		return Manifest{}, ErrNotConfigured
	}
	cmd, err := toolCommand(ctx, cfg.PgRestore, "pg_restore", cfg.DatabaseURL,
		"--no-owner", "--no-privileges", "--exit-on-error", "--single-transaction",
		"--dbname="+strings.TrimPrefix(parsed.Path, "/"), filepath.Join(copyDir, dumpName))
	if err != nil {
		return Manifest{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s", ErrRestore, firstLine(stderr.String()))
	}
	if err := extractBlobs(filepath.Join(copyDir, blobsName), cfg.BlobRoot); err != nil {
		return Manifest{}, ErrBlobs
	}
	return manifest, nil
}

// extractBlobs unpacks a blob archive into root. Blobs are content-
// addressed, so a file already present is the same bytes; entries that
// would escape root are refused.
func extractBlobs(archive, root string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.FromSlash(header.Name)
		if filepath.IsAbs(name) || name != filepath.Clean(name) || strings.HasPrefix(name, "..") {
			return errors.New("unsafe blob path")
		}
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
}
