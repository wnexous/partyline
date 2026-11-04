// Package authn holds credential primitives: argon2id passwords, API and session tokens,
// and the authenticated Principal.
package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

// PrincipalKind tells whether a request acts as a user or as one of the user's agents.
type PrincipalKind string

// Principal kinds.
const (
	KindUser  PrincipalKind = "user"
	KindAgent PrincipalKind = "agent"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	Kind      PrincipalKind
	UserID    string
	Role      string
	AgentID   string // set for agent principals on the routes that act as an AI
	TokenID   string // set when authenticated by an API token
	SessionID string // set when authenticated by a session cookie
}

// IID is the intelligence the principal acts as (AD-016): the AI for an agent principal and the user
// otherwise. An AI acts as itself, never with its host user's rights, so an agent principal that did
// not resolve to an AI acts as nobody ("").
func (p Principal) IID() string {
	if p.Kind == KindAgent {
		return p.AgentID
	}
	return p.UserID
}

// argon2id parameters: production uses the OWASP baseline (19 MiB, 2 passes, 1 lane); test
// binaries use a minimal cost so suites that create many accounts do not spend most of their
// time hashing. VerifyPassword reads the cost from each hash, so both stay compatible.
const (
	argonKeyLen  = 32
	argonSaltLen = 16
)

// kdfParams is the argon2id cost of a new password hash.
type kdfParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

var (
	productionParams = kdfParams{memory: 19 * 1024, time: 2, threads: 1}
	testParams       = kdfParams{memory: 64, time: 1, threads: 1}
)

// hashingParams picks the cost of a new password hash: minimal under `go test`, the production
// baseline everywhere else. This is the only test-only switch; production code cannot reach it.
func hashingParams() kdfParams {
	if testing.Testing() {
		return testParams
	}
	return productionParams
}

var b64 = base64.RawStdEncoding

// HashPassword returns an argon2id PHC string for password.
func HashPassword(password string) (string, error) {
	return hashPassword(password, hashingParams())
}

// hashPassword returns an argon2id PHC string using p. Tests call it with productionParams to
// assert the baseline even though they run in a test binary.
func hashPassword(password string, p kdfParams) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC hash, using the hash's own parameters.
func VerifyPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var version int
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil {
		return false, errors.New("malformed argon2 parameters")
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("malformed argon2 salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errors.New("malformed argon2 key")
	}
	got := argon2.IDKey([]byte(password), salt, passes, memory, threads, uint32(len(want))) // #nosec G115 -- key length is small
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// TokenKind is the kind of an API token.
type TokenKind string

// API token kinds.
const (
	TokenAgent TokenKind = "agent"
	TokenUser  TokenKind = "user"
)

// TokenPrefixLen is the number of leading secret characters kept for display.
const TokenPrefixLen = 12

// NewToken returns a new API token secret "pl_<kind>_<base62>".
func NewToken(kind TokenKind) (string, error) {
	body, err := randomBase62()
	if err != nil {
		return "", err
	}
	return "pl_" + string(kind) + "_" + body, nil
}

// ParseTokenKind reports the kind of a well-formed API token secret.
func ParseTokenKind(secret string) (TokenKind, bool) {
	for _, kind := range []TokenKind{TokenAgent, TokenUser} {
		body, ok := strings.CutPrefix(secret, "pl_"+string(kind)+"_")
		if ok && body != "" && isBase62(body) {
			return kind, true
		}
	}
	return "", false
}

// TokenPrefix returns the displayable prefix of a token secret.
func TokenPrefix(secret string) string {
	if len(secret) < TokenPrefixLen {
		return secret
	}
	return secret[:TokenPrefixLen]
}

// HashToken returns the hex SHA-256 of a token or session secret; only this is stored.
func HashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// NewSessionToken returns a new session cookie value.
func NewSessionToken() (string, error) {
	return randomBase62()
}

const base62Alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// base62Len is the length of 32 random bytes in base62: ceil(256 / log2(62)).
const base62Len = 43

func randomBase62() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	s := new(big.Int).SetBytes(buf).Text(62)
	return strings.Repeat("0", base62Len-len(s)) + s, nil
}

func isBase62(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune(base62Alphabet, r) {
			return false
		}
	}
	return true
}

type principalKey struct{}

// WithPrincipal returns a context carrying the authenticated principal.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored by WithPrincipal, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
