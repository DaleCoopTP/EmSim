// Package media owns content-addressed files. Database metadata remains in
// training's ports; this adapter never accepts a client-provided filename as a
// storage path.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var (
	ErrTooLarge = errors.New("media too large")
	ErrDigest   = errors.New("media digest mismatch")
	ErrSize     = errors.New("media size mismatch")
)

type FileStore struct{ root string }

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, errors.New("BLOB_ROOT is required")
	}
	if err := os.MkdirAll(filepath.Join(root, ".staging"), 0o750); err != nil {
		return nil, fmt.Errorf("create blob root: %w", err)
	}
	return &FileStore{root: root}, nil
}

func (s *FileStore) Path(sha [32]byte) string {
	h := hex.EncodeToString(sha[:])
	return filepath.Join(s.root, h[:2], h[2:])
}

// Put streams into a private staging file then atomically publishes by hash.
// A concurrent writer of the same bytes is harmless: the already-published
// file wins and no client path ever enters this calculation.
func (s *FileStore) Put(ctx context.Context, r io.Reader, want [32]byte, size, max int64) error {
	if size < 0 || size > max {
		return ErrTooLarge
	}
	tmp, err := os.CreateTemp(filepath.Join(s.root, ".staging"), "blob-")
	if err != nil {
		return fmt.Errorf("stage blob: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha256.New()
	buf := make([]byte, 32*1024)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			_ = tmp.Close()
			return err
		}
		read, er := r.Read(buf)
		if read > 0 {
			n += int64(read)
			if n > max {
				_ = tmp.Close()
				return ErrTooLarge
			}
			if _, err := h.Write(buf[:read]); err != nil {
				_ = tmp.Close()
				return err
			}
			if _, err := tmp.Write(buf[:read]); err != nil {
				_ = tmp.Close()
				return err
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			_ = tmp.Close()
			return er
		}
	}
	if n != size {
		_ = tmp.Close()
		return ErrSize
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	if got != want {
		_ = tmp.Close()
		return ErrDigest
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	final := s.Path(want)
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		if _, statErr := os.Stat(final); statErr == nil {
			return nil
		}
		return fmt.Errorf("publish blob: %w", err)
	}
	return os.Chmod(final, 0o640)
}

func (s *FileStore) Open(sha [32]byte) (*os.File, error) { return os.Open(s.Path(sha)) }
