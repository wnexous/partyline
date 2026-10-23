package platform

import (
	"context"
	"log/slog"
	"time"

	"github.com/wnexous/partyline/internal/store/db"
)

// Lifetimes of queue items.
const (
	// UnackedTTL is how long an item waits for its AI's session before it is deleted and counted
	// as dropped (CID-10).
	UnackedTTL = 7 * 24 * time.Hour
	// AckedTTL is how long an acknowledged item and its files stay after the ack, so that the AI
	// can still download the files and reply (CID-09).
	AckedTTL = 24 * time.Hour
	// PurgeInterval is how often Run purges: often enough that an item goes within minutes of
	// its lifetime, which a daily job would stretch to twice the 24 hours of CID-09.
	PurgeInterval = 10 * time.Minute

	// purgeBatch bounds each delete so the single writer is never held for long.
	purgeBatch = 1000
)

// PurgeResult counts what one purge removed.
type PurgeResult struct {
	// Expired is the unacknowledged items older than UnackedTTL, deleted and counted as dropped.
	Expired int64
	// Acked is the acknowledged items older than AckedTTL, deleted with their files.
	Acked int64
	// Files are the blobs (sha256) the deleted items' files used. The rows are gone, but a blob
	// another attachment still uses must stay: the caller releases the unreferenced ones.
	Files []string
}

// Purge deletes the unacknowledged items received UnackedTTL or more before now and adds them to
// their AI's dropped count (CID-10), and the items acknowledged AckedTTL or more before now
// together with their file rows (CID-09). Blobs are not touched: the result lists them.
func (q *Queue) Purge(ctx context.Context, now time.Time) (PurgeResult, error) {
	var res PurgeResult
	seen := map[string]bool{}
	collect := func(shas []string) {
		for _, sha := range shas {
			if !seen[sha] {
				seen[sha] = true
				res.Files = append(res.Files, sha)
			}
		}
	}
	for {
		var n int
		err := q.d.Store.WithTx(ctx, func(tx *db.Queries) error {
			rows, err := tx.ListExpiredUnackedPlatformItems(ctx, db.ListExpiredUnackedPlatformItemsParams{
				Cutoff: now.Add(-UnackedTTL).UnixMilli(), Limit: purgeBatch,
			})
			if err != nil || len(rows) == 0 {
				return err
			}
			ids := make([]string, len(rows))
			perAgent := map[string]int64{}
			for i, r := range rows {
				ids[i] = r.ID
				perAgent[r.AgentID]++
			}
			for agent, dropped := range perAgent {
				if err := tx.AddPlatformDropped(ctx, db.AddPlatformDroppedParams{N: dropped, AgentID: agent}); err != nil {
					return err
				}
			}
			shas, err := remove(ctx, tx, ids)
			collect(shas)
			n = len(ids)
			return err
		})
		if err != nil {
			return res, err
		}
		res.Expired += int64(n)
		if n < purgeBatch {
			break
		}
	}
	for {
		var n int
		err := q.d.Store.WithTx(ctx, func(tx *db.Queries) error {
			ids, err := tx.ListExpiredAckedPlatformItems(ctx, db.ListExpiredAckedPlatformItemsParams{
				Cutoff: now.Add(-AckedTTL).UnixMilli(), Limit: purgeBatch,
			})
			if err != nil || len(ids) == 0 {
				return err
			}
			shas, err := remove(ctx, tx, ids)
			collect(shas)
			n = len(ids)
			return err
		})
		if err != nil {
			return res, err
		}
		res.Acked += int64(n)
		if n < purgeBatch {
			break
		}
	}
	return res, nil
}

// remove deletes the items with their file rows and returns the blobs those files used.
func remove(ctx context.Context, tx *db.Queries, ids []string) ([]string, error) {
	shas, err := tx.ListPlatformAttachmentSHAs(ctx, ids)
	if err != nil {
		return nil, err
	}
	_, err = tx.DeletePlatformItems(ctx, ids)
	return shas, err
}

// Run purges now and then every interval until ctx ends. release receives the blobs of the items
// each pass deleted, to delete those nothing else references (CID-09); nil skips it. Failures
// are logged without any message content.
func (q *Queue) Run(ctx context.Context, interval time.Duration, release func(ctx context.Context, shas []string), log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		res, err := q.Purge(ctx, q.d.Now())
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("platform queue purge failed", "err", err)
		case err == nil:
			if res.Expired > 0 || res.Acked > 0 {
				log.Info("platform queue purge", "expired", res.Expired, "acked", res.Acked)
			}
			if release != nil && len(res.Files) > 0 {
				release(ctx, res.Files)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
