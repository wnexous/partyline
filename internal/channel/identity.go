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
	// MainFolder is the canonical folder of the main worktree when the session runs in a linked git
	// worktree; the server resolves the AI of the repository from it (SESS-01). Empty outside one.
	MainFolder string
	// Home is the canonical home folder of the user; the tree shows folders under it as "~".
	Home string
	// Harness is the harness id (claude, opencode, pi, omp); empty is unknown.
	Harness string
	// LiveID is the id of the process that holds the agent WebSocket, sent as X-Partyline-Live on
	// every call so the server can record the session a message came from (SESS-29). Empty for a
	// tools-only process, which holds no socket and posts as the AI of its folder.
	LiveID string
	// Adopt is the handle or v0.3 agent name of an unlinked AI to link to the folder when it has
	// none yet (--as, PARTYLINE_AGENT) (MIG-02).
	Adopt string
	// Org, Position and Parent are what the session asked for when it started (--org, --position, --parent,
	// handed on by the launcher as PARTYLINE_LAUNCH_ORG, PARTYLINE_LAUNCH_POSITION and
	// PARTYLINE_LAUNCH_PARENT): the organization (a slug or an id), the name of the position and the @handle or
	// id of the AI that starts this one. They place a new AI (ORG-04, POS-07, POS-08); each is sent only when
	// set, so a session that asks for nothing sends nothing and gets the server's defaults (AD-020).
	Org, Position, Parent string
}

// setHeaders sets the identity headers. The folder, the main worktree folder, the home folder and
// the position are percent-encoded (spaces, non-ASCII characters and separators), so a header
// carries any path or name; the folder header is always sent, so a process that could not tell its
// folder is refused with folder_required instead of being taken for a client from before folders.
func (id Identity) setHeaders(h http.Header) {
	machine := id.Machine
	if machine == "" {
		machine = UnknownMachine
	}
	h.Set(dto.HeaderMachine, machine)
	h.Set(dto.HeaderFolder, url.PathEscape(id.Folder))
	for k, v := range map[string]string{
		dto.HeaderHarness: id.Harness, dto.HeaderAdopt: id.Adopt, dto.HeaderLive: id.LiveID,
		dto.HeaderMainFolder: escapeHeader(id.MainFolder),
		dto.HeaderOrg:        id.Org, dto.HeaderPosition: escapeHeader(id.Position), dto.HeaderParent: id.Parent,
	} {
		if v != "" {
			h.Set(k, v)
		}
	}
	if id.Home != "" {
		h.Set(dto.HeaderHome, url.PathEscape(id.Home))
	}
}

// escapeHeader percent-encodes a value the way the folder is (url.PathEscape); empty stays empty.
func escapeHeader(v string) string {
	if v == "" {
		return ""
	}
	return url.PathEscape(v)
}

// asksPlacement is whether the session asked for an organization or a position, the two things the
// server can refuse a start for (403 or 409); a parent it cannot refuse, it ignores one it will not take.
func (id Identity) asksPlacement() bool { return id.Org != "" || id.Position != "" }

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

// isLinkedWorktree is whether the process runs in a linked git worktree: it sent a main folder
// different from its own (SESS-01). Only a tools-only process there, without a listen process, has no live
// channel (SESS-06).
func isLinkedWorktree(id Identity) bool { return id.MainFolder != "" && id.MainFolder != id.Folder }
