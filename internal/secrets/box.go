// Package secrets encrypts values at rest with AES-256-GCM under the instance secret key.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wnexous/partyline/internal/config"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// KeyFile is the name of the generated key file inside the data directory.
const KeyFile = "secret.key"

// Box seals and opens values. The sealed format is nonce || ciphertext+tag.
type Box struct {
	aead cipher.AEAD
}

// NewBox returns a Box for a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secret key must be %d bytes", KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext with a random nonce.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a value produced by Seal; it fails on tampering or a wrong key.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, errors.New("sealed value too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, errors.New("cannot decrypt sealed value")
	}
	return plain, nil
}

// LoadOrCreateKey returns the key from PARTYLINE_SECRET_KEY (base64, 32 bytes) or from
// <data dir>/secret.key, generating that file with mode 0600 when it does not exist.
func LoadOrCreateKey(cfg *config.Config) ([]byte, error) {
	if cfg.SecretKey != "" {
		key, err := decodeKey(cfg.SecretKey)
		if err != nil {
			return nil, fmt.Errorf("PARTYLINE_SECRET_KEY: %w", err)
		}
		return key, nil
	}

	path := filepath.Join(cfg.DataDir, KeyFile)
	data, err := os.ReadFile(path) // #nosec G304 -- path is built from the configured data dir
	if err == nil {
		key, err := decodeKey(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- see above
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		_ = f.Close()
		return nil, err
	}
	return key, f.Close()
}

func decodeKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("not valid base64")
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("must decode to %d bytes", KeySize)
	}
	return key, nil
}
