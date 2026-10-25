package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

const (
	// recordSize is the RFC 8188 record size advertised in every body (4096 octets).
	recordSize = 4096
	// maxPayload is the largest plaintext accepted for one notification.
	maxPayload = 4096
	// saltSize and authSecretSize are the RFC 8291 sizes of the injected salt and the
	// subscription's auth secret; publicKeySize is the uncompressed P-256 point.
	saltSize       = 16
	authSecretSize = 16
	publicKeySize  = 65
)

var (
	errInvalidSubscriptionKey = errors.New("invalid subscription key")
	errCannotEncryptPayload   = errors.New("cannot encrypt payload")
)

// Encrypt seals payload for a browser subscription as RFC 8291 defines, with the caller's
// ephemeral private key and salt. The user agent's public key is the 65-byte uncompressed P-256
// point of the subscription's p256dh value and authSecret its 16-byte auth value. The returned
// body starts with the RFC 8188 header and carries one final record.
func Encrypt(payload, uaPublic, authSecret []byte, asPrivate *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(payload) > maxPayload || len(authSecret) != authSecretSize || len(salt) != saltSize ||
		asPrivate == nil || asPrivate.Curve() != ecdh.P256() {
		return nil, errCannotEncryptPayload
	}
	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, errInvalidSubscriptionKey
	}
	asPublic := asPrivate.PublicKey().Bytes()
	if len(asPublic) != publicKeySize {
		return nil, errCannotEncryptPayload
	}

	shared, err := asPrivate.ECDH(uaKey)
	if err != nil {
		return nil, errInvalidSubscriptionKey
	}

	keyInfo := make([]byte, 0, len("WebPush: info\x00")+len(uaPublic)+len(asPublic))
	keyInfo = append(keyInfo, "WebPush: info\x00"...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, errCannotEncryptPayload
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, errCannotEncryptPayload
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, errCannotEncryptPayload
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, errCannotEncryptPayload
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, errCannotEncryptPayload
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, errCannotEncryptPayload
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errCannotEncryptPayload
	}
	record := make([]byte, 0, len(payload)+1)
	record = append(record, payload...)
	record = append(record, 0x02) // final record, no padding (RFC 8188)
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	body := make([]byte, 0, saltSize+4+1+len(asPublic)+len(ciphertext))
	body = append(body, salt...)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], recordSize)
	body = append(body, size[:]...)
	body = append(body, publicKeySize)
	body = append(body, asPublic...)
	body = append(body, ciphertext...)
	return body, nil
}

// EncryptRandom seals payload with a fresh ephemeral P-256 key pair and a random salt, as every
// production send does.
func EncryptRandom(payload, uaPublic, authSecret []byte) ([]byte, error) {
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, errCannotEncryptPayload
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, errCannotEncryptPayload
	}
	return Encrypt(payload, uaPublic, authSecret, private, salt)
}
