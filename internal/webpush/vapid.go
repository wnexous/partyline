// Package webpush delivers Web Push notifications to the browsers of the users of a room.
package webpush

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver for the subscription reset below

	"github.com/wnexous/partyline/internal/secrets"
)

// VAPIDKeyFile is the name of the sealed VAPID key file inside the data directory.
const VAPIDKeyFile = "vapid.key"

// databaseFile is the SQLite file inside the data directory. The loader drops the stored push
// subscriptions when it has to replace a corrupt key, and the design gives it only the data dir
// (not the store), so it opens the file itself.
const databaseFile = "partyline.db"

// VAPID is the instance's Web Push application server key (RFC 8292).
type VAPID struct {
	// Private signs the VAPID JWTs.
	Private *ecdsa.PrivateKey
	// Public is the 65-byte uncompressed P-256 point, base64url without padding.
	Public string
	// Replaced is true when the key file was invalid and a new pair was generated, so that the
	// app can warn and the stored subscriptions are dropped.
	Replaced bool
}

// LoadOrCreateVAPID returns the instance's VAPID key, creating and sealing it on first start.
//
// The private key is PKCS#8 DER sealed with the instance box and written base64 with mode 0600.
// An existing valid file is opened and reused. A corrupt, wrong-curve or unreadable file is
// replaced with a new pair and every stored push subscription is deleted (they were bound to the
// old key); the caller never fails startup on a bad file, only on an I/O error writing the new
// one (PUSH-26).
func LoadOrCreateVAPID(dataDir string, box *secrets.Box) (*VAPID, error) {
	if box == nil {
		return nil, errors.New("webpush: nil secrets box")
	}
	path := filepath.Join(dataDir, VAPIDKeyFile)
	data, err := os.ReadFile(path) // #nosec G304 -- path is built from the configured data dir
	switch {
	case err == nil:
		v, parseErr := openVAPID(data, box)
		if parseErr == nil {
			return v, nil
		}
		return replaceVAPID(path, dataDir, box)
	case errors.Is(err, os.ErrNotExist):
		return createVAPID(path, dataDir, box)
	default:
		return replaceVAPID(path, dataDir, box)
	}
}

// createVAPID generates a new pair and writes it with O_EXCL. When another process created the
// file first, that file is read and used instead.
func createVAPID(path, dataDir string, box *secrets.Box) (*VAPID, error) {
	v, data, err := newVAPID(box)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("webpush: create data dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- see LoadOrCreateVAPID
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path) // #nosec G304 -- see LoadOrCreateVAPID
		if readErr != nil {
			return nil, fmt.Errorf("webpush: read %s: %w", VAPIDKeyFile, readErr)
		}
		return openVAPID(existing, box)
	}
	if err != nil {
		return nil, fmt.Errorf("webpush: create %s: %w", VAPIDKeyFile, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("webpush: write %s: %w", VAPIDKeyFile, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("webpush: write %s: %w", VAPIDKeyFile, err)
	}
	return v, nil
}

// replaceVAPID removes an invalid key file, creates a new pair and drops the stored subscriptions.
func replaceVAPID(path, dataDir string, box *secrets.Box) (*VAPID, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("webpush: replace %s: %w", VAPIDKeyFile, err)
	}
	v, err := createVAPID(path, dataDir, box)
	if err != nil {
		return nil, err
	}
	v.Replaced = true
	if err := dropSubscriptions(context.Background(), dataDir); err != nil {
		slog.Default().Warn("webpush: replaced an invalid VAPID key but could not drop the stored subscriptions", "err", err)
	}
	return v, nil
}

// newVAPID generates a P-256 key pair and its sealed file content.
func newVAPID(box *secrets.Box) (*VAPID, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: generate VAPID key: %w", err)
	}
	public, err := publicPoint(priv)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: marshal VAPID key: %w", err)
	}
	sealed, err := box.Seal(der)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: seal VAPID key: %w", err)
	}
	return &VAPID{Private: priv, Public: public}, []byte(base64.StdEncoding.EncodeToString(sealed) + "\n"), nil
}

// openVAPID opens and validates the sealed key file content.
func openVAPID(data []byte, box *secrets.Box) (*VAPID, error) {
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, errors.New("webpush: VAPID key file is not valid base64")
	}
	der, err := box.Open(sealed)
	if err != nil {
		return nil, errors.New("webpush: VAPID key file cannot be opened")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("webpush: VAPID key file is not a PKCS#8 private key")
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return nil, errors.New("webpush: VAPID key file does not hold a P-256 key")
	}
	public, err := publicPoint(priv)
	if err != nil {
		return nil, err
	}
	return &VAPID{Private: priv, Public: public}, nil
}

// publicPoint returns the base64url uncompressed point of a P-256 private key.
func publicPoint(priv *ecdsa.PrivateKey) (string, error) {
	point, err := priv.PublicKey.ECDH()
	if err != nil {
		return "", errors.New("webpush: VAPID key file does not hold a P-256 key")
	}
	return base64.RawURLEncoding.EncodeToString(point.Bytes()), nil
}

// dropSubscriptions deletes every push subscription of the database in dataDir. A database or
// table that does not exist yet (a fresh install, or a schema older than migration 0025) has
// nothing to drop.
func dropSubscriptions(ctx context.Context, dataDir string) error {
	path := filepath.Join(dataDir, databaseFile)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)") // #nosec G304 -- path is built from the configured data dir
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'push_subscriptions'`).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		return nil
	}
	_, err = db.ExecContext(ctx, `DELETE FROM push_subscriptions`)
	return err
}
