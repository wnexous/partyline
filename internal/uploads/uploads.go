// Package uploads receives files larger than one request in chunks (LF-03): an upload session is
// created with the file's name, type and size, its chunks are appended in order to a temporary file
// under <data dir>/uploads, hashed as they arrive, and the last one moves the file into the blob
// store. A message then uses the finished upload as an attachment (upload_ids). An upload belongs to
// the user or the AI that created it, and to no one else: another caller gets ErrNotFound. Unfinished
// and unused uploads go after 24 hours (LF-06), and a session that would leave the data disk with
// less than the reserve free is refused (LF-07).
package uploads

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/diskspace"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

const (
	// MaxChunkBytes is the largest chunk: under the 100 MB a proxy such as Cloudflare's free plan
	// lets through in one request.
	MaxChunkBytes = 50 << 20
	// TTL is how long an upload lives, finished or not, until a message uses it (LF-06).
	TTL = 24 * time.Hour
	// SweepInterval is how often expired uploads are removed.
	SweepInterval = time.Hour
	// MaxOpen is how many unfinished uploads one owner may hold at once.
	MaxOpen = 16
	// maxNameBytes bounds the file name and the type of an upload.
	maxNameBytes = 255
)

var (
	// ErrNotFound is an upload that does not exist, expired or belongs to someone else.
	ErrNotFound = errors.New("upload not found")
	// ErrBusy is a chunk sent while another chunk of the same upload is being written.
	ErrBusy = errors.New("another chunk of the upload is being written")
	// ErrIncomplete is an upload used in a message before its last chunk arrived.
	ErrIncomplete = errors.New("upload not complete")
	// ErrTooMany is a new upload of an owner who already holds MaxOpen unfinished ones.
	ErrTooMany = errors.New("too many unfinished uploads")
	// ErrEmptyChunk is a chunk without bytes for an upload that still needs some.
	ErrEmptyChunk = errors.New("empty chunk")
	// ErrInvalidSize is an upload of zero bytes or fewer.
	ErrInvalidSize = errors.New("size must be a positive number of bytes")
)

// ChunkTooLargeError is a chunk longer than the chunk size or than what the file still needs.
type ChunkTooLargeError struct{ Max int64 }

func (e *ChunkTooLargeError) Error() string {
	return fmt.Sprintf("chunk too large: at most %d bytes", e.Max)
}

// FileTooLargeError is an upload larger than the largest file (PARTYLINE_MAX_FILE_MB).
type FileTooLargeError struct{ Max int64 }

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("file too large: at most %d bytes", e.Max)
}

// InsufficientStorageError refuses an upload that would leave the data disk with less than the
// reserve free (LF-07).
type InsufficientStorageError struct {
	Free, Need int64
}

func (e *InsufficientStorageError) Error() string {
	return fmt.Sprintf("not enough free disk space: %d bytes free, the upload needs %d", e.Free, e.Need)
}

// OffsetError is a chunk that does not start where the upload is: the client resumes at Received.
type OffsetError struct {
	Received int64
	Complete bool
}

func (e *OffsetError) Error() string {
	if e.Complete {
		return "the upload is already complete"
	}
	return fmt.Sprintf("the chunk must start at offset %d", e.Received)
}

// OwnerKind is who created an upload.
type OwnerKind string

// Owner kinds.
const (
	OwnerUser  OwnerKind = "user"
	OwnerAgent OwnerKind = "agent"
)

// Owner is the user or the AI an upload belongs to.
type Owner struct {
	Kind OwnerKind
	ID   string
}

// Upload is the state of an upload session.
type Upload struct {
	ID       string
	Name     string
	Mime     string
	Size     int64
	Received int64
	// SHA256 is the hash of the finished file, empty until the last chunk arrived.
	SHA256    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Complete reports whether every byte arrived and the file is in the blob store.
func (u Upload) Complete() bool { return u.SHA256 != "" }

// CreateInput describes the file of a new upload.
type CreateInput struct {
	Name string
	Mime string
	Size int64
}

// Deps are the service's dependencies.
type Deps struct {
	Store *store.Store
	Blobs *attachments.Store
	// Dir holds the temporary files; it is created when missing.
	Dir string
	// MaxFileBytes bounds the size of one upload (PARTYLINE_MAX_FILE_MB).
	MaxFileBytes int64
	// MaxRequestBytes is the single-request cap (PARTYLINE_MAX_UPLOAD_MB): a chunk never exceeds it.
	MaxRequestBytes int64
	// ReserveBytes is the free space an upload must leave on the disk (PARTYLINE_DISK_RESERVE_MB).
	ReserveBytes int64
	// FreeSpace reports the free bytes of the filesystem of a directory and whether they are known;
	// diskspace.Free by default. Tests replace it.
	FreeSpace func(dir string) (int64, bool)
	Now       func() time.Time
}

// Service manages upload sessions.
type Service struct {
	d     Deps
	chunk int64
	// createMu orders the creates, so each one checks the limits against the rows of the others.
	createMu sync.Mutex
	// busy holds the ids of the uploads a chunk is being written to.
	busy sync.Map
}

// New returns the service, creating its directory.
func New(d Deps) (*Service, error) {
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create uploads dir: %w", err)
	}
	if d.FreeSpace == nil {
		d.FreeSpace = diskspace.Free
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	chunk := int64(MaxChunkBytes)
	if d.MaxRequestBytes > 0 && d.MaxRequestBytes < chunk {
		chunk = d.MaxRequestBytes
	}
	return &Service{d: d, chunk: chunk}, nil
}

// ChunkSize is the largest chunk a client may send.
func (s *Service) ChunkSize() int64 { return s.chunk }

// MaxFileBytes is the largest file an upload may hold.
func (s *Service) MaxFileBytes() int64 { return s.d.MaxFileBytes }

func (s *Service) path(id string) string { return filepath.Join(s.d.Dir, id) }

// Create starts an upload of in for owner, after checking that the file is allowed and fits on the
// disk with the reserve to spare, counting what the other unfinished uploads may still write (LF-07).
func (s *Service) Create(ctx context.Context, o Owner, in CreateInput) (Upload, error) {
	name := strings.TrimSpace(in.Name)
	if name != "" {
		name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	}
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	mimeType := strings.TrimSpace(in.Mime)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	check := validation.Check{}
	if len(name) > maxNameBytes {
		check.Add("name", fmt.Sprintf("must be at most %d bytes", maxNameBytes))
	}
	if len(mimeType) > maxNameBytes {
		check.Add("mime", fmt.Sprintf("must be at most %d bytes", maxNameBytes))
	}
	if err := check.Err(); err != nil {
		return Upload{}, err
	}
	if in.Size <= 0 {
		return Upload{}, ErrInvalidSize
	}
	if in.Size > s.d.MaxFileBytes {
		return Upload{}, &FileTooLargeError{Max: s.d.MaxFileBytes}
	}
	// The checks and the insert are one step: the limits hold against concurrent creates, which the
	// mutex orders and the write transaction makes see each other's rows.
	s.createMu.Lock()
	defer s.createMu.Unlock()
	now := s.d.Now()
	row := db.Upload{
		ID: store.NewID(), OwnerKind: string(o.Kind), OwnerID: o.ID, Name: name, Mime: mimeType, Size: in.Size,
		ShaState: []byte{}, CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(TTL).UnixMilli(),
	}
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		open, err := q.CountOpenUploads(ctx, db.CountOpenUploadsParams{OwnerKind: string(o.Kind), OwnerID: o.ID, Now: now.UnixMilli()})
		if err != nil {
			return err
		}
		if open >= MaxOpen {
			return ErrTooMany
		}
		if free, known := s.d.FreeSpace(s.d.Dir); known {
			pending, err := q.PendingUploadBytes(ctx, now.UnixMilli())
			if err != nil {
				return err
			}
			if need := in.Size + s.d.ReserveBytes + pending; free < need {
				return &InsufficientStorageError{Free: free, Need: need}
			}
		}
		return q.CreateUpload(ctx, db.CreateUploadParams{
			ID: row.ID, OwnerKind: row.OwnerKind, OwnerID: row.OwnerID, Name: row.Name, Mime: row.Mime, Size: row.Size,
			ShaState: row.ShaState, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
		})
	})
	if err != nil {
		return Upload{}, err
	}
	return toUpload(row), nil
}

// Get returns an upload of owner.
func (s *Service) Get(ctx context.Context, o Owner, id string) (Upload, error) {
	row, err := s.row(ctx, o, id)
	if err != nil {
		return Upload{}, err
	}
	return toUpload(row), nil
}

func (s *Service) row(ctx context.Context, o Owner, id string) (db.Upload, error) {
	row, err := s.d.Store.R().GetUpload(ctx, db.GetUploadParams{ID: id, OwnerKind: string(o.Kind), OwnerID: o.ID, Now: s.d.Now().UnixMilli()})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Upload{}, ErrNotFound
	}
	return row, err
}

// Write appends one chunk, read from body, to the upload at offset, which must be the number of bytes
// the upload received so far (else an *OffsetError says where to resume). The chunk is streamed to the
// temporary file and the hash, never held in memory, and is kept whole or not at all: a chunk that
// fails midway leaves the upload as it was. The chunk that brings the last byte moves the file into the
// blob store and completes the upload.
func (s *Service) Write(ctx context.Context, o Owner, id string, offset int64, body io.Reader) (Upload, error) {
	if _, held := s.busy.LoadOrStore(id, struct{}{}); held {
		return Upload{}, ErrBusy
	}
	defer s.busy.Delete(id)
	row, err := s.row(ctx, o, id)
	if err != nil {
		return Upload{}, err
	}
	if row.Sha256 != "" {
		return toUpload(row), &OffsetError{Received: row.Received, Complete: true}
	}
	if offset != row.Received {
		return toUpload(row), &OffsetError{Received: row.Received}
	}
	h, err := resumeHash(row.ShaState)
	if err != nil {
		return Upload{}, err
	}
	// Once the chunk is on disk it is recorded, and the file finished, also when the client hung up.
	ctx = context.WithoutCancel(ctx)
	if row.Received < row.Size {
		n, err := s.append(row, h, body)
		if err != nil {
			return toUpload(row), err
		}
		state, err := h.(encoding.BinaryMarshaler).MarshalBinary()
		if err != nil {
			return Upload{}, err
		}
		var advanced int64
		err = s.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
			advanced, err = q.AdvanceUpload(ctx, db.AdvanceUploadParams{Received: row.Received + n, ShaState: state, ID: id, Offset: row.Received})
			return err
		})
		if err != nil {
			return Upload{}, err
		}
		if advanced == 0 {
			return Upload{}, ErrNotFound // expired and swept while the chunk arrived
		}
		row.Received += n
		row.ShaState = state
	}
	if row.Received == row.Size {
		// Also reached again by a retry when an earlier last chunk was stored and the move failed.
		sha := hex.EncodeToString(h.Sum(nil))
		if err := s.finish(ctx, row, sha); err != nil {
			return Upload{}, err
		}
		row.Sha256 = sha
	}
	return toUpload(row), nil
}

// append writes the chunk at the end of the bytes the upload received and returns its length. On any
// failure the file is cut back to where it was.
func (s *Service) append(row db.Upload, h hash.Hash, body io.Reader) (n int64, err error) {
	f, err := os.OpenFile(s.path(row.ID), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = f.Truncate(row.Received)
		}
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() < row.Received {
		return 0, fmt.Errorf("upload %s: the temporary file lost bytes (%d of %d)", row.ID, info.Size(), row.Received)
	}
	if err := f.Truncate(row.Received); err != nil {
		return 0, err
	}
	if _, err := f.Seek(row.Received, io.SeekStart); err != nil {
		return 0, err
	}
	limit := min(s.chunk, row.Size-row.Received)
	n, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(body, limit+1))
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		return 0, &ChunkTooLargeError{Max: limit}
	case err != nil:
		return 0, err
	case n > limit:
		return 0, &ChunkTooLargeError{Max: limit}
	case n == 0:
		return 0, ErrEmptyChunk
	}
	return n, nil
}

// finish moves the whole file into the blob store and marks the upload complete. A retry after the
// move succeeded and the mark did not finds the blob already there.
func (s *Service) finish(ctx context.Context, row db.Upload, sha string) error {
	if _, err := s.d.Blobs.AdoptHashed(s.path(row.ID), sha); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		f, oerr := s.d.Blobs.Open(sha)
		if oerr != nil {
			return err
		}
		_ = f.Close()
	}
	return s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.CompleteUpload(ctx, db.CompleteUploadParams{Sha256: sha, ID: row.ID})
		return err
	})
}

func resumeHash(state []byte) (hash.Hash, error) {
	h := sha256.New()
	if len(state) == 0 {
		return h, nil
	}
	if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
		return nil, fmt.Errorf("resume upload hash: %w", err)
	}
	return h, nil
}

// Finished returns the finished uploads of owner named by ids, in their order and without repeats,
// for a message that attaches them: an id that is not an upload of owner is ErrNotFound, and an
// upload still missing chunks is ErrIncomplete.
func (s *Service) Finished(ctx context.Context, o Owner, ids []string) ([]Upload, error) {
	ids = unique(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.d.Store.R().ListOwnUploads(ctx, db.ListOwnUploadsParams{OwnerKind: string(o.Kind), OwnerID: o.ID, Now: s.d.Now().UnixMilli(), Ids: ids})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]db.Upload, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	out := make([]Upload, 0, len(ids))
	for _, id := range ids {
		row, ok := byID[id]
		if !ok {
			return nil, ErrNotFound
		}
		if row.Sha256 == "" {
			return nil, ErrIncomplete
		}
		out = append(out, toUpload(row))
	}
	return out, nil
}

// Consume deletes the uploads a message used; their blobs stay with its attachments.
func (s *Service) Consume(ctx context.Context, o Owner, ids []string) error {
	ids = unique(ids)
	if len(ids) == 0 {
		return nil
	}
	return s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.DeleteOwnUploads(ctx, db.DeleteOwnUploadsParams{OwnerKind: string(o.Kind), OwnerID: o.ID, Ids: ids})
	})
}

// Sweep removes the uploads past their expiry and their temporary files (LF-06), and returns how many
// it removed. The blob of a finished upload no message used is left to the blob sweep, which keeps a
// blob only while something references it.
func (s *Service) Sweep(ctx context.Context) (int64, error) {
	var ids []string
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
		ids, err = q.DeleteExpiredUploads(ctx, s.d.Now().UnixMilli())
		return err
	})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return int64(len(ids)), err
		}
	}
	return int64(len(ids)), nil
}

// RemoveOrphans deletes the temporary files no upload owns, left by a crash or a database restored
// from a backup; the app runs it at start, before any chunk arrives. It returns how many it removed.
func (s *Service) RemoveOrphans(ctx context.Context) (int, error) {
	ids, err := s.d.Store.R().ListUploadIDs(ctx)
	if err != nil {
		return 0, err
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	entries, err := os.ReadDir(s.d.Dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || known[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(s.d.Dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Run sweeps expired uploads now and then every interval until ctx is done.
func (s *Service) Run(ctx context.Context, interval time.Duration, log *slog.Logger) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := s.Sweep(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("uploads: sweep failed", "err", err)
		case err == nil && n > 0:
			log.Info("uploads: expired uploads removed", "uploads", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func unique(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func toUpload(row db.Upload) Upload {
	return Upload{
		ID: row.ID, Name: row.Name, Mime: row.Mime, Size: row.Size, Received: row.Received, SHA256: row.Sha256,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), ExpiresAt: time.UnixMilli(row.ExpiresAt).UTC(),
	}
}
