// Package attachments stores file contents once per SHA-256 under <data>/blobs/<aa>/<sha256>.
// Message rows reference blobs by hash; a blob is deleted only when no row references it.
package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrTooLarge is returned by Put when the content exceeds the limit (SEC-03).
var ErrTooLarge = errors.New("attachment too large")

// ErrNotFound is returned when no blob has the hash.
var ErrNotFound = errors.New("blob not found")

// Store is a content-addressed blob directory. Directories are 0700, files 0600.
type Store struct {
	dir string
}

// Open returns a Store rooted at dir, creating it when missing.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create blob dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// validHash reports whether sha is a lowercase hex SHA-256, so it is safe in a path.
func validHash(sha string) bool {
	if len(sha) != sha256.Size*2 {
		return false
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) path(sha string) string { return filepath.Join(s.dir, sha[:2], sha) }

// Put stores r's content and returns its SHA-256 (hex) and size. Content larger than limit
// bytes returns ErrTooLarge and leaves no file. Identical content is stored once.
func (s *Store) Put(r io.Reader, limit int64) (sha string, size int64, err error) {
	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return "", 0, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name()) // no-op after a successful rename
	}()
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit+1))
	if err != nil {
		return "", 0, err
	}
	if size > limit {
		return "", 0, ErrTooLarge
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	final := s.path(sha)
	if _, err := os.Stat(final); err == nil {
		// Refresh the time so the orphan sweep's grace period covers this new reference.
		now := time.Now()
		if err := os.Chtimes(final, now, now); err != nil {
			return "", 0, err
		}
		return sha, size, nil
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", 0, err
	}
	return sha, size, nil
}

// Open returns the blob's content; ErrNotFound when it does not exist.
func (s *Store) Open(sha string) (*os.File, error) {
	if !validHash(sha) {
		return nil, ErrNotFound
	}
	f, err := os.Open(s.path(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Delete removes a blob. Callers delete only hashes no attachment row references any more.
// Deleting a missing blob is not an error.
func (s *Store) Delete(sha string) error {
	if !validHash(sha) {
		return nil
	}
	err := os.Remove(s.path(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// DeleteIfOlder removes a blob when it was last written before cutoff and reports whether it
// did. Put refreshes the time of existing content, so a recent upload of the same bytes keeps
// the blob. Callers delete only hashes no attachment row references any more.
func (s *Store) DeleteIfOlder(sha string, cutoff time.Time) (bool, error) {
	if !validHash(sha) {
		return false, nil
	}
	info, err := os.Stat(s.path(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.ModTime().Before(cutoff) {
		return false, err
	}
	return true, s.Delete(sha)
}

// Sweep deletes every blob last written before cutoff that referenced reports as unused, and
// abandoned upload temp files older than cutoff. Blobs newer than cutoff are kept, since a
// message referencing them may still be committing. It returns the number of blobs removed.
func (s *Store) Sweep(cutoff time.Time, referenced func(sha string) (bool, error)) (int, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	removed := 0
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".upload-") {
			return root.Remove(p)
		}
		if !validHash(name) {
			return nil
		}
		used, err := referenced(name)
		if err != nil || used {
			return err
		}
		if err := s.Delete(name); err != nil {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}
