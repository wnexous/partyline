package connectors

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// FileLinkTTL is how long a signed attachment link stays valid.
const FileLinkTTL = 10 * time.Minute

// FileLinkPath is the path prefix of signed attachment links.
const FileLinkPath = "/api/v1/hooks/files/"

// ErrLinkInvalid means a signed link is malformed, forged, expired or points at no stored file.
var ErrLinkInvalid = errors.New("invalid or expired link")

// FileLinks issues and resolves signed, expiring attachment links for platforms that fetch
// files by URL. A token is the attachment id and expiry sealed with the instance key, so it
// cannot be forged or extended.
type FileLinks struct {
	box   *secrets.Box
	store *store.Store
	base  string
	now   func() time.Time
}

// NewFileLinks returns FileLinks issuing links under publicURL; now defaults to time.Now.
func NewFileLinks(box *secrets.Box, st *store.Store, publicURL *url.URL, now func() time.Time) *FileLinks {
	if now == nil {
		now = time.Now
	}
	return &FileLinks{box: box, store: st, base: strings.TrimRight(publicURL.String(), "/"), now: now}
}

type linkClaims struct {
	A string `json:"a"`
	E int64  `json:"e"`
}

// URL returns a link to the attachment valid for FileLinkTTL ("" if sealing fails).
func (l *FileLinks) URL(attachmentID string) string {
	plain, _ := json.Marshal(linkClaims{A: attachmentID, E: l.now().Add(FileLinkTTL).Unix()})
	sealed, err := l.box.Seal(plain)
	if err != nil {
		return ""
	}
	return l.base + FileLinkPath + base64.RawURLEncoding.EncodeToString(sealed)
}

// Resolve returns the stored attachment a token points at, or ErrLinkInvalid.
func (l *FileLinks) Resolve(ctx context.Context, token string) (db.Attachment, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return db.Attachment{}, ErrLinkInvalid
	}
	plain, err := l.box.Open(sealed)
	if err != nil {
		return db.Attachment{}, ErrLinkInvalid
	}
	var c linkClaims
	if json.Unmarshal(plain, &c) != nil || l.now().Unix() > c.E {
		return db.Attachment{}, ErrLinkInvalid
	}
	row, err := l.store.R().GetAttachmentRoom(ctx, c.A)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Attachment{}, ErrLinkInvalid
	}
	if err != nil {
		return db.Attachment{}, err
	}
	if row.Attachment.Status != string(dto.AttachmentStatusStored) {
		return db.Attachment{}, ErrLinkInvalid
	}
	return row.Attachment, nil
}
