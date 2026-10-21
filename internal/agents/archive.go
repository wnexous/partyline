package agents

import (
	"context"
	"log/slog"
	"time"

	"github.com/wnexous/partyline/internal/store/db"
)

const (
	// ArchiveAfter is how long a temporary agent stays offline before it is archived (PROJ-02).
	ArchiveAfter = 24 * time.Hour
	// ArchiveInterval is how often the archive job runs.
	ArchiveInterval = 24 * time.Hour
)

// Archive marks archived every temporary agent that is offline and was last seen more than
// ArchiveAfter ago (PROJ-02); fixed agents are never archived. A reconnect unarchives the
// agent (Resolve, MarkConnected). It returns how many agents it archived.
func (r *Registry) Archive(ctx context.Context) (int, error) {
	now := r.now()
	ids, err := r.store.R().ListArchiveCandidates(ctx, nullMillis(now.Add(-ArchiveAfter)))
	if err != nil {
		return 0, err
	}
	n := 0
	err = r.store.WithTx(ctx, func(q *db.Queries) error {
		for _, id := range ids {
			if r.presence.Online(id) {
				continue // a live session is never archived, however old its last_seen_at
			}
			if err := q.ArchiveAgent(ctx, db.ArchiveAgentParams{ArchivedAt: nullMillis(now), ID: id}); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// RunArchive runs Archive now and then every interval until ctx is done (the daily job).
func (r *Registry) RunArchive(ctx context.Context, interval time.Duration, log *slog.Logger) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := r.Archive(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("agent archive failed", "err", err)
		case err == nil && n > 0:
			log.Info("agents archived", "agents", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
