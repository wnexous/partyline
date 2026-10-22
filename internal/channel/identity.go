package channel

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/wnexous/partyline/internal/api/dto"
)

// UnknownMachine is the machine name sent when the hostname cannot be read.
const UnknownMachine = "unknown"

// Identity is what a process of an AI session tells the server about itself with every
// connection and call (AID-07): where it works and which harness it is. The server resolves the
// AI from the token's owner, the machine and the folder; processes of one session started in
// one folder are therefore the same AI, whatever their harness or process.
type Identity struct {
	// Machine is the hostname; an empty one is sent as UnknownMachine.
	Machine string
	// Folder is the canonical folder (CanonicalFolder): absolute, symlinks resolved, no trailing
	// separator.
	Folder string
	// Home is the canonical home folder of the user; the tree shows folders under it as "~".
	Home string
	// Harness is the harness id (claude, opencode, pi, omp, codex, gemini); empty is unknown.
	Harness string
	// Adopt is the handle or v0.3 agent name of an unlinked AI to link to the folder when it has
	// none yet (--as, PARTYLINE_AGENT) (MIG-02).
	Adopt string
}

// setHeaders sets the identity headers. The folder and the home folder are percent-encoded
// (spaces, non-ASCII characters and separators), so a header carries any path; the folder
// header is always sent, so a process that could not tell its folder is refused with
// folder_required instead of being taken for a client from before folders.
func (id Identity) setHeaders(h http.Header) {
	machine := id.Machine
	if machine == "" {
		machine = UnknownMachine
	}
	h.Set(dto.HeaderMachine, machine)
	h.Set(dto.HeaderFolder, url.PathEscape(id.Folder))
	for k, v := range map[string]string{dto.HeaderHarness: id.Harness, dto.HeaderAdopt: id.Adopt} {
		if v != "" {
			h.Set(k, v)
		}
	}
	if id.Home != "" {
		h.Set(dto.HeaderHome, url.PathEscape(id.Home))
	}
}

// CanonicalFolder turns dir into the folder the server identifies an AI by (AID-07): absolute,
// symlinks resolved, cleaned (no trailing separator, no "." or ".." segments; "/" stays "/"). A
// folder that cannot be resolved, because it does not exist (any more), is returned absolute and
// cleaned.
func CanonicalFolder(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

// NewLiveID returns the random id of a process that holds the agent WebSocket: it tells a
// reconnecting process from another session of the same AI (SES-02). 22 characters of
// [A-Za-z0-9_-].
func NewLiveID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("random source: " + err.Error()) // crypto/rand does not fail on supported systems
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
