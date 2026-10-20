package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Replays remembers accepted hook signatures per connector until they expire (HOOK-05).
// The store-backed implementation keeps them across connector and server restarts.
type Replays interface {
	// Seen reports whether the signature was accepted for the connector and has not expired.
	Seen(ctx context.Context, connectorID, signature string, now time.Time) (bool, error)
	// Remember records the signature until expires (inclusive); false means it was already
	// recorded and has not expired (a replay).
	Remember(ctx context.Context, connectorID, signature string, now, expires time.Time) (bool, error)
}

// signatureHash is what is stored: the signature itself never is.
func signatureHash(signature string) string {
	sum := sha256.Sum256([]byte(signature))
	return hex.EncodeToString(sum[:])
}

// StoreReplays keeps accepted signatures in the webhook_replays table. Expired rows are
// deleted whenever a new signature is recorded, which bounds the table.
type StoreReplays struct {
	st *store.Store
}

// NewReplays returns the store-backed Replays.
func NewReplays(st *store.Store) *StoreReplays { return &StoreReplays{st: st} }

// Seen implements Replays.
func (r *StoreReplays) Seen(ctx context.Context, connectorID, signature string, now time.Time) (bool, error) {
	n, err := r.st.R().WebhookReplaySeen(ctx, db.WebhookReplaySeenParams{
		ConnectorID: connectorID, SigHash: signatureHash(signature), Now: now.UnixMilli(),
	})
	return n > 0, err
}

// Remember implements Replays.
func (r *StoreReplays) Remember(ctx context.Context, connectorID, signature string, now, expires time.Time) (bool, error) {
	var added int64
	err := r.st.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteExpiredWebhookReplays(ctx, now.UnixMilli()); err != nil {
			return err
		}
		var err error
		added, err = q.RecordWebhookReplay(ctx, db.RecordWebhookReplayParams{
			ConnectorID: connectorID, SigHash: signatureHash(signature), ExpiresAt: expires.UnixMilli(),
		})
		return err
	})
	return added > 0, err
}

// MemoryReplays keeps accepted signatures in memory only; for tests of a single connector
// instance.
type MemoryReplays struct {
	mu   sync.Mutex
	seen map[string]time.Time // connector id + hash → expiry
}

// NewMemoryReplays returns an empty in-memory Replays.
func NewMemoryReplays() *MemoryReplays { return &MemoryReplays{seen: map[string]time.Time{}} }

// Seen implements Replays.
func (m *MemoryReplays) Seen(_ context.Context, connectorID, signature string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	until, ok := m.seen[connectorID+"|"+signatureHash(signature)]
	return ok && !now.After(until), nil
}

// Remember implements Replays.
func (m *MemoryReplays) Remember(_ context.Context, connectorID, signature string, now, expires time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, until := range m.seen {
		if now.After(until) {
			delete(m.seen, k)
		}
	}
	key := connectorID + "|" + signatureHash(signature)
	if _, ok := m.seen[key]; ok {
		return false, nil
	}
	m.seen[key] = expires
	return true, nil
}
