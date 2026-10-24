// Package identities links chat accounts (a WhatsApp number, a Telegram or Discord user) to
// Partyline users after they prove control with a pairing code (IDN-01..05). Messages from a
// linked account, received through the connector it was linked through, resolve to its user,
// which makes them trusted for that user's agents (AD-006).
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
	"github.com/wnexous/partyline/internal/orgs"
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

// CreatePairing returns a new code for the user, valid for CodeTTL and only on the given
// connector (IDN-01). The caller checks that the user may use the connector. Only the
// code's hash is stored.
func (s *Service) CreatePairing(ctx context.Context, userID, connectorID string) (string, time.Time, error) {
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
		return q.CreatePairingCode(ctx, db.CreatePairingCodeParams{CodeHash: hashCode(code), UserID: userID, ConnectorID: connectorID, ExpiresAt: expires.UnixMilli()})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// Account is a chat account as one connector reports it.
type Account struct {
	// ConnectorID is the connector the message came through; the identity belongs to it.
	ConnectorID string
	// ConnectorKind is the connector's kind, kept for display.
	ConnectorKind string
	SenderID      string
	AltID         string
	Display       string
}

// errWrongUser rolls back a Link whose code belongs to a user the connection does not take.
var errWrongUser = errors.New("pairing code of a user the connection does not take")

// Link consumes a valid code of the account's connector and links the account (sender id
// and alternate id) to the code's user, on that connector only, replacing an earlier link
// of that account there (IDN-02). ok is false for unknown or expired codes (IDN-03) and for
// codes of another connector, which are not consumed (IDN-02). When adminsOf is set (an
// organization id) the code must belong to an admin of that organization at this moment: a code of
// anyone else links nothing and is not consumed (IDN-05, AD-016: connectors whose callers choose
// sender ids freely link only the codes of the admins of the connection's organization).
func (s *Service) Link(ctx context.Context, code string, acct Account, adminsOf string) (userID string, ok bool, err error) {
	if acct.SenderID == "" || acct.ConnectorID == "" {
		return "", false, nil
	}
	now := s.d.Now().UnixMilli()
	id := store.NewID()
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		row, err := q.TakePairingCode(ctx, db.TakePairingCodeParams{CodeHash: hashCode(code), ConnectorID: acct.ConnectorID})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.ExpiresAt <= now {
			return nil
		}
		if adminsOf != "" {
			m, err := q.GetOrgMember(ctx, db.GetOrgMemberParams{OrgID: adminsOf, Iid: row.UserID})
			if errors.Is(err, sql.ErrNoRows) || (err == nil && m.Permission != string(orgs.PermissionAdmin)) {
				return errWrongUser
			}
			if err != nil {
				return err
			}
		}
		if err := q.DeleteIdentitiesForSender(ctx, db.DeleteIdentitiesForSenderParams{ConnectorID: acct.ConnectorID, SenderID: acct.SenderID, AltID: acct.AltID}); err != nil {
			return err
		}
		if err := q.CreateIdentity(ctx, db.CreateIdentityParams{
			ID: id, UserID: row.UserID, ConnectorID: acct.ConnectorID, ConnectorKind: acct.ConnectorKind,
			ExternalID: acct.SenderID, AltExternalID: acct.AltID, Display: acct.Display, CreatedAt: now,
		}); err != nil {
			return err
		}
		userID, ok = row.UserID, true
		return nil
	})
	if errors.Is(err, errWrongUser) {
		return "", false, nil
	}
	if err != nil || !ok {
		return "", false, err
	}
	s.d.Bus.Publish(events.IdentityLinked{UserID: userID, IdentityID: id})
	return userID, true, nil
}

// ResolveSender returns the active user linked to the sender (by sender id or alternate id)
// through this connector, or "" (IDN-04, DLV-06). Links made through other connectors, even
// of the same kind, never match.
func (s *Service) ResolveSender(ctx context.Context, connectorID, senderID, altID string) (string, error) {
	if connectorID == "" || (senderID == "" && altID == "") {
		return "", nil
	}
	if senderID == "" {
		senderID = altID
	}
	if altID == "" {
		altID = senderID
	}
	u, err := s.d.Store.R().ResolveIdentityUser(ctx, db.ResolveIdentityUserParams{ConnectorID: connectorID, SenderID: senderID, AltID: altID})
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
