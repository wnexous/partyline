package rooms

import (
	"context"
	"log/slog"
	"time"

	"github.com/wnexous/partyline/internal/store/db"
)

const (
	// RetentionInterval is how often the retention job runs (OPS-02).
	RetentionInterval = 24 * time.Hour
	// blobGrace protects blobs stored recently: a message referencing them may still be committing.
	blobGrace = time.Hour
	// deleteBlobGrace is the same guard when a room is deleted: blobs written within it are
	// left to the sweep.
	deleteBlobGrace = time.Minute
	// retentionBatch bounds each delete so the single writer is never held for long.
	retentionBatch = 1000
)

// CleanupResult counts what one retention pass removed.
type CleanupResult struct {
	Messages int64
	Blobs    int
}

// Cleanup deletes messages older than their room's retention days (0 keeps them forever),
// with their attachments rows and outbox entries, then removes blobs no attachment references
// any more (OPS-02).
func (s *Service) Cleanup(ctx context.Context) (CleanupResult, error) {
	var res CleanupResult
	now := s.now()
	for {
		var n int64
		err := s.store.WithTx(ctx, func(q *db.Queries) (err error) {
			n, err = q.DeleteExpiredMessages(ctx, db.DeleteExpiredMessagesParams{Now: now.UnixMilli(), Limit: retentionBatch})
			return err
		})
		if err != nil {
			return res, err
		}
		res.Messages += n
		if n < retentionBatch {
			break
		}
	}
	if s.blobs == nil {
		return res, nil
	}
	removed, err := s.blobs.Sweep(now.Add(-blobGrace), func(sha string) (bool, error) {
		n, err := s.store.R().CountAttachmentsBySHA(ctx, sha)
		return n > 0, err
	})
	res.Blobs = removed
	return res, err
}

// RetentionTask is another cleanup the retention job runs on every pass, such as purging
// tokens revoked long ago (TOKH-03). Run returns how many rows it removed.
type RetentionTask struct {
	Name string
	Run  func(context.Context) (int64, error)
}

// RunRetention runs Cleanup and the extra tasks now and then every interval until ctx is done.
func (s *Service) RunRetention(ctx context.Context, interval time.Duration, log *slog.Logger, tasks ...RetentionTask) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		res, err := s.Cleanup(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("retention cleanup failed", "err", err)
		case err == nil && (res.Messages > 0 || res.Blobs > 0):
			log.Info("retention cleanup", "messages", res.Messages, "blobs", res.Blobs)
		}
		for _, task := range tasks {
			n, err := task.Run(ctx)
			switch {
			case err != nil && ctx.Err() == nil:
				log.Error("retention cleanup failed", "task", task.Name, "err", err)
			case err == nil && n > 0:
				log.Info("retention cleanup", "task", task.Name, "removed", n)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// removeRoomBlobs deletes, best effort, the blobs of a deleted room that no attachment
// references any more (ROOM-02); the retention sweep remains the backstop.
func (s *Service) removeRoomBlobs(ctx context.Context, shas []string) {
	if s.blobs == nil {
		return
	}
	cutoff := s.now().Add(-deleteBlobGrace)
	for _, sha := range shas {
		n, err := s.store.R().CountAttachmentsBySHA(ctx, sha)
		if err != nil || n > 0 {
			continue
		}
		_, _ = s.blobs.DeleteIfOlder(sha, cutoff)
	}
}
