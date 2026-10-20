// Package identities links chat accounts (a WhatsApp number, a Telegram or Discord user) to
// Partyline users after they prove control with a pairing code (IDN-01..04). Messages from a
// linked account resolve to its user, which makes them trusted for that user's agents (AD-006).
package identities

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Pairing code format (IDN-01).
const (
	// CodeAlphabet has the letters A-Z without I and O, and the digits 2-9.
	CodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// CodeLength is the number of characters of a pairing code.
	CodeLength = 6
	// CodeTTL is how long a pairing code is valid.
	CodeTTL = 10 * time.Minute
)

// Deps are the collaborators of the Service.
type Deps struct {
	Store *store.Store
	Bus   *events.Bus
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service manages pairing codes and verified identities.
type Service struct {
	d Deps
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

func newCode() (string, error) {
	b := make([]byte, CodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = CodeAlphabet[int(b[i])%len(CodeAlphabet)] // 256 is a multiple of 32: no bias
	}
	return string(b), nil
}

// CreatePairing returns a new code for the user, valid for CodeTTL (IDN-01). Only its hash
// is stored.
func (s *Service) CreatePairing(ctx context.Context, userID string) (string, time.Time, error) {
	now := s.d.Now()
	expires := now.Add(CodeTTL)
	code, err := newCode()
	if err != nil {
		return "", time.Time{}, err
	}
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteExpiredPairingCodes(ctx, now.UnixMilli()); err != nil {
			return err
		}
		return q.CreatePairingCode(ctx, db.CreatePairingCodeParams{CodeHash: hashCode(code), UserID: userID, ExpiresAt: expires.UnixMilli()})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// Link consumes a valid code and links the sender (sender id and alternate id) of the
// connector kind to the code's user, replacing an earlier link of that account (IDN-02).
// ok is false for unknown or expired codes (IDN-03).
func (s *Service) Link(ctx context.Context, code, connectorKind, senderID, altID, display string) (userID string, ok bool, err error) {
	if senderID == "" {
		return "", false, nil
	}
	now := s.d.Now().UnixMilli()
	id := store.NewID()
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		row, err := q.TakePairingCode(ctx, hashCode(code))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.ExpiresAt <= now {
			return nil
		}
		if err := q.DeleteIdentitiesForSender(ctx, db.DeleteIdentitiesForSenderParams{ConnectorKind: connectorKind, SenderID: senderID, AltID: altID}); err != nil {
			return err
		}
		if err := q.CreateIdentity(ctx, db.CreateIdentityParams{
			ID: id, UserID: row.UserID, ConnectorKind: connectorKind, ExternalID: senderID,
			AltExternalID: altID, Display: display, CreatedAt: now,
		}); err != nil {
			return err
		}
		userID, ok = row.UserID, true
		return nil
	})
	if err != nil || !ok {
		return "", false, err
	}
	s.d.Bus.Publish(events.IdentityLinked{UserID: userID, IdentityID: id})
	return userID, true, nil
}

// ResolveSender returns the active user linked to the sender (by sender id or alternate id,
// within the connector kind), or "" (IDN-04, DLV-06).
func (s *Service) ResolveSender(ctx context.Context, connectorKind, senderID, altID string) (string, error) {
	if senderID == "" && altID == "" {
		return "", nil
	}
	if senderID == "" {
		senderID = altID
	}
	if altID == "" {
		altID = senderID
	}
	u, err := s.d.Store.R().ResolveIdentityUser(ctx, db.ResolveIdentityUserParams{ConnectorKind: connectorKind, SenderID: senderID, AltID: altID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return u, err
}

// List returns the user's identities.
func (s *Service) List(ctx context.Context, userID string) ([]db.Identity, error) {
	return s.d.Store.R().ListIdentities(ctx, userID)
}

// Delete removes one of the user's identities; access.ErrNotFound when it is not theirs.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	return s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.DeleteIdentity(ctx, db.DeleteIdentityParams{ID: id, UserID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return access.ErrNotFound
		}
		return nil
	})
}
