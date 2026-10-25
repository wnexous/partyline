// Package webpush delivers Web Push notifications to the browsers of the users of a room.
package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wnexous/partyline/internal/secrets"
)

// VAPIDKeyFile is the name of the sealed VAPID key file inside the data directory.
const VAPIDKeyFile = "vapid.key"

// VAPID is the instance's Web Push application server key (RFC 8292).
type VAPID struct {
	// Private signs the VAPID JWTs.
	Private *ecdsa.PrivateKey
	// Public is the 65-byte uncompressed P-256 point, base64url without padding.
	Public string
	// New is true when this call generated a new pair because the file was missing or invalid,
	// so that the app drops the stored subscriptions (they were bound to the old key).
	New bool
}

// LoadOrCreateVAPID returns the instance's VAPID key, creating and sealing it on first start.
//
// The private key is PKCS#8 DER sealed with the instance box and written base64 with mode 0600.
// An existing valid file is opened and reused. A corrupt, wrong-curve or unreadable file is
// replaced with a new pair; a missing file creates one. Both set VAPID.New, and the app drops
// every stored push subscription at startup (PUSH-26). The loader fails only when the file can
// neither be loaded nor replaced, and app.New keeps running with push off in that case.
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
		return nil, fmt.Errorf("webpush: create data dir: %w", fileErrorKind(err))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- see LoadOrCreateVAPID
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path) // #nosec G304 -- see LoadOrCreateVAPID
		if readErr != nil {
			return nil, fmt.Errorf("webpush: read %s: %w", VAPIDKeyFile, fileErrorKind(readErr))
		}
		return openVAPID(existing, box)
	}
	if err != nil {
		return nil, fmt.Errorf("webpush: create %s: %w", VAPIDKeyFile, fileErrorKind(err))
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("webpush: write %s: %w", VAPIDKeyFile, fileErrorKind(err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("webpush: write %s: %w", VAPIDKeyFile, fileErrorKind(err))
	}
	v.New = true
	return v, nil
}

// replaceVAPID removes an invalid key file and creates a new pair. A directory at the path is not
// a key file the server may delete: the caller runs with push off until an operator fixes it.
func replaceVAPID(path, dataDir string, box *secrets.Box) (*VAPID, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return nil, fmt.Errorf("webpush: replace %s: is a directory", VAPIDKeyFile)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("webpush: replace %s: %w", VAPIDKeyFile, fileErrorKind(err))
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("webpush: replace %s: %w", VAPIDKeyFile, fileErrorKind(err))
	}
	return createVAPID(path, dataDir, box)
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

// fileErrorKind drops the path from an os error before it is wrapped, so a log line carries only
// the key file's name and the error kind (permission, directory, and so on), never a full path.
func fileErrorKind(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}
