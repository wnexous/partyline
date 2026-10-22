package agents

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// MaxFolderLength is the longest folder path a session may send, in bytes (AID-05).
const MaxFolderLength = 1024

// MachineTouchInterval is how often a machine's last sighting is written: sessions and tool
// calls resolve their folder constantly, the machine row only needs a coarse "last seen".
const MachineTouchInterval = time.Minute

// FolderID is what a session tells the server about itself on every connection and tool call:
// where it works (machine and canonical folder) and, for the app and the adoption of a v0.3
// identity, the harness and the name it was started with (AID-07, MIG-02).
type FolderID struct {
	// Machine is the hostname the CLI reports.
	Machine string
	// Folder is the canonical folder: absolute, symlinks resolved, no trailing separator.
	Folder string
	// Home is the canonical home folder of the machine's user; the tree shows "~" for it
	// (TREE-02). Optional: an unusable one is ignored.
	Home string
	// Harness is the harness id (claude, opencode, ...). Empty leaves the app of an existing
	// identity alone.
	Harness string
	// Adopt is a handle or a v0.3 agent name (--as, PARTYLINE_AGENT): the owner's unlinked
	// identity to link to this folder when the folder has none yet (MIG-02).
	Adopt string
}

// folderError is a 422 for a session whose machine or folder the server cannot use.
func folderError(code dto.ErrorCode, field, message string) *httpx.Error {
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: code, Message: message, Fields: map[string]string{field: message}}
}

// ValidateFolder checks the machine and folder a session sent: both must be there
// (422 folder_required; an old CLI sends neither) and the folder must be canonical: valid UTF-8
// without a NUL, at most MaxFolderLength bytes, absolute (a "/" path or a "C:\" or "C:/" path),
// without a trailing separator (except the root) and without "." or ".." segments
// (422 folder_invalid) (AID-05).
func ValidateFolder(machine, folder string) error {
	if cleanText(machine, MaxMachineLength) == "" {
		return folderError(dto.ErrorCodeFolderRequired, "machine", "the session did not say which machine it runs on: update partyline")
	}
	if folder == "" {
		return folderError(dto.ErrorCodeFolderRequired, "folder", "the session did not say which folder it works in: update partyline")
	}
	invalid := func(why string) error { return folderError(dto.ErrorCodeFolderInvalid, "folder", "the folder "+why) }
	switch {
	case !utf8.ValidString(folder):
		return invalid("must be valid UTF-8")
	case strings.ContainsRune(folder, 0):
		return invalid("must not contain a NUL character")
	case len(folder) > MaxFolderLength:
		return invalid("must have at most 1024 bytes")
	}
	root, seps, ok := folderRoot(folder)
	if !ok {
		return invalid("must be an absolute path")
	}
	rest := folder[len(root):]
	if rest == "" {
		return nil // the root itself
	}
	if strings.ContainsRune(seps, rune(rest[len(rest)-1])) {
		return invalid("must not end with a separator")
	}
	for _, seg := range strings.FieldsFunc(rest, func(r rune) bool { return strings.ContainsRune(seps, r) }) {
		if seg == "." || seg == ".." {
			return invalid(`must not have "." or ".." segments`)
		}
	}
	return nil
}

// folderRoot splits the root off an absolute folder: "/" for Unix paths ("/" is the only
// separator there; a backslash is an ordinary file name character) and "C:\" or "C:/" for
// Windows drive paths (both separators). ok is false for anything else, including UNC paths and
// drive-relative paths such as "C:acme".
func folderRoot(folder string) (root, seps string, ok bool) {
	switch {
	case strings.HasPrefix(folder, "/"):
		return "/", "/", true
	case len(folder) >= 3 && isLetter(folder[0]) && folder[1] == ':' && (folder[2] == '\\' || folder[2] == '/'):
		return folder[:3], `/\`, true
	}
	return "", "", false
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// folderBase is the last element of a valid folder, the name an identity's default display
// name and handle derive from. The root "/" is its own name; a drive root reads "C:".
func folderBase(folder string) string {
	_, seps, _ := folderRoot(folder)
	trimmed := strings.TrimRight(folder, seps)
	if trimmed == "" {
		return folder
	}
	return trimmed[strings.LastIndexAny(trimmed, seps)+1:]
}

// machineKey identifies a machine of an owner in the throttle of machine sightings.
type machineKey struct{ owner, hostname string }

// machineSeen is the last sighting written for a machine by this process.
type machineSeen struct {
	at   time.Time
	home string
}

// touchMachine records a sighting of the owner's machine (TREE-02, TREE-04): its home folder
// when the session sent a usable one and the time, at most once per MachineTouchInterval per
// machine unless the home folder changed. The owner's label is never touched.
func (r *Registry) touchMachine(ctx context.Context, ownerID, hostname, home string) error {
	if home != "" && ValidateFolder(hostname, home) != nil {
		home = "" // an unusable home folder is not worth failing the session over
	}
	now := r.now()
	key := machineKey{ownerID, hostname}
	r.machinesMu.Lock()
	seen, known := r.machines[key]
	if known && now.Sub(seen.at) < MachineTouchInterval && (home == "" || home == seen.home) {
		r.machinesMu.Unlock()
		return nil
	}
	next := machineSeen{at: now, home: seen.home}
	if home != "" {
		next.home = home
	}
	r.machines[key] = next // reserved before the write: concurrent calls do not all write
	r.machinesMu.Unlock()
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.UpsertMachine(ctx, db.UpsertMachineParams{OwnerID: ownerID, Hostname: hostname, Home: home, LastSeenAt: nullMillis(now)})
	})
	if err != nil {
		r.machinesMu.Lock()
		delete(r.machines, key) // the next call tries again
		r.machinesMu.Unlock()
	}
	return err
}

// ResolveFolder returns the owner's AI for the machine and folder a session sent, creating it
// on first sight with the folder's name (AID-01, AID-02, AID-03). Any harness opened in that
// folder resolves the same identity, and its app follows the harness: always for a live
// session (live), and for a tools-only call (Codex, the tools half of pi and OpenCode) only
// while nobody is connected, so a tool call never relabels a live session (AID-04).
//
// When the folder has no identity yet, the owner's unlinked identity (one of v0.3, without a
// folder) is linked to it instead of creating one: the one named by FolderID.Adopt, else the
// one whose handle is the folder's handle; a name that is missing or already linked is ignored,
// and an identity is linked once (MIG-02). Creation stops at MaxAgentsPerOwner (ErrAgentLimit).
// An invalid machine or folder is a 422 (ValidateFolder) and changes nothing.
func (r *Registry) ResolveFolder(ctx context.Context, ownerID string, id FolderID, live bool) (db.Agent, error) {
	id.Machine = cleanText(id.Machine, MaxMachineLength)
	if err := ValidateFolder(id.Machine, id.Folder); err != nil {
		return db.Agent{}, err
	}
	if h := strings.TrimSpace(id.Harness); h != "" {
		id.Harness = HarnessID(h)
	} else {
		id.Harness = ""
	}
	id.Adopt = strings.ToLower(strings.TrimSpace(id.Adopt))
	if err := r.touchMachine(ctx, ownerID, id.Machine, id.Home); err != nil {
		return db.Agent{}, err
	}
	a, err := r.store.R().GetAgentByFolder(ctx, db.GetAgentByFolderParams{OwnerID: ownerID, Machine: id.Machine, Folder: id.Folder})
	if errors.Is(err, sql.ErrNoRows) {
		a, err = r.linkOrCreate(ctx, ownerID, id)
	}
	if err != nil {
		return db.Agent{}, err
	}
	return r.relabel(ctx, a, id.Harness, live)
}

// linkOrCreate gives a folder without an identity one: the owner's unlinked identity it adopts,
// else a new one. allocMu serializes it with every other resolution, so concurrent sessions of
// one new folder end up with the same identity; the unique index on (owner, machine, folder)
// is the last guard.
func (r *Registry) linkOrCreate(ctx context.Context, ownerID string, id FolderID) (db.Agent, error) {
	r.allocMu.Lock()
	defer r.allocMu.Unlock()
	var out db.Agent
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		a, err := q.GetAgentByFolder(ctx, db.GetAgentByFolderParams{OwnerID: ownerID, Machine: id.Machine, Folder: id.Folder})
		if !errors.Is(err, sql.ErrNoRows) { // found (created while this call waited), or a failure
			out = a
			return err
		}
		cand, by, found, err := adoptionCandidate(ctx, q, ownerID, id)
		if err != nil {
			return err
		}
		if found {
			out, err = r.adopt(ctx, q, cand, by, id)
			return err
		}
		out, err = r.createFolderAgent(ctx, q, ownerID, id)
		return err
	})
	return out, err
}

// adoptionCandidate finds the unlinked identity of the owner a session of the folder adopts
// (MIG-02): the one id.Adopt names by handle or v0.3 name, else the one whose handle is the
// folder's default handle. by tells which rule matched.
func adoptionCandidate(ctx context.Context, q *db.Queries, ownerID string, id FolderID) (a db.Agent, by string, found bool, err error) {
	orNone := func(a db.Agent, err error) (db.Agent, bool, error) {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Agent{}, false, nil
		}
		return a, err == nil, err
	}
	byHandle := func(handle string) (db.Agent, bool, error) {
		return orNone(q.GetUnlinkedAgentByHandle(ctx, db.GetUnlinkedAgentByHandleParams{OwnerID: ownerID, Handle: handle}))
	}
	byName := func(name string) (db.Agent, bool, error) {
		return orNone(q.GetUnlinkedAgentByName(ctx, db.GetUnlinkedAgentByNameParams{OwnerID: ownerID, Name: name}))
	}
	if id.Adopt != "" {
		for _, find := range []func(string) (db.Agent, bool, error){byHandle, byName} {
			if a, found, err = find(id.Adopt); found || err != nil {
				return a, "as", found, err
			}
		}
	}
	a, found, err = byHandle(handles.Default(handles.AI, folderBase(id.Folder)))
	return a, "folder", found, err
}

// adopt links the unlinked identity to the session's machine and folder; it keeps its id,
// history, groups and profile. The link is audited.
func (r *Registry) adopt(ctx context.Context, q *db.Queries, a db.Agent, by string, id FolderID) (db.Agent, error) {
	harness := id.Harness
	if harness == "" {
		harness = a.Harness
	}
	n, err := q.AdoptAgent(ctx, db.AdoptAgentParams{Machine: id.Machine, Folder: id.Folder, Harness: harness, ID: a.ID})
	if err != nil {
		return db.Agent{}, err
	}
	if n != 1 { // linked meanwhile: impossible inside this write transaction
		return db.Agent{}, errors.New("agent is already linked to a folder")
	}
	a.Machine, a.Folder, a.Harness = id.Machine, id.Folder, harness
	if r.audit != nil {
		err = r.audit.Record(ctx, q, audit.Entry{
			ActorID: a.OwnerID, Action: audit.ActionAgentAdopt, TargetKind: audit.TargetAgent, TargetID: a.ID,
			Detail: map[string]any{"by": by},
		})
	}
	return a, err
}

// createFolderAgent inserts the owner's AI for a folder with its intelligence: the display name
// is the folder's name with an upper-case first letter and the handle the first free one derived
// from it (AID-01, AID-03).
func (r *Registry) createFolderAgent(ctx context.Context, q *db.Queries, ownerID string, id FolderID) (db.Agent, error) {
	n, err := q.CountAgentsByOwner(ctx, ownerID)
	if err != nil {
		return db.Agent{}, err
	}
	if n >= MaxAgentsPerOwner {
		return db.Agent{}, ErrAgentLimit
	}
	agentID := store.NewID()
	if err := q.InsertFolderAgent(ctx, db.InsertFolderAgentParams{
		ID: agentID, OwnerID: ownerID, Name: agentID, CreatedAt: r.now().UnixMilli(),
		Harness: HarnessID(id.Harness), Machine: id.Machine, Folder: id.Folder,
	}); err != nil {
		return db.Agent{}, err
	}
	base := folderBase(id.Folder)
	profile, err := intelligences.Create(ctx, q, intelligences.NewParams{
		ID: agentID, Kind: handles.AI, DisplayName: handles.DisplayName(base), HandleBase: base, Now: r.now(),
	})
	if err != nil {
		return db.Agent{}, err
	}
	// The v0.3 display name column mirrors the profile until it is dropped (T7), and the row's
	// own name is its id: without the mirror the name people read would be that id.
	if err := q.SetAgentDisplayName(ctx, db.SetAgentDisplayNameParams{DisplayName: profile.DisplayName, ID: agentID}); err != nil {
		return db.Agent{}, err
	}
	return q.GetAgent(ctx, agentID)
}

// relabel makes harness the agent's app (AID-02). An empty harness changes nothing, and a
// tools-only call (live false) leaves the app of an agent that has a live session.
func (r *Registry) relabel(ctx context.Context, a db.Agent, harness string, live bool) (db.Agent, error) {
	if harness == "" || harness == a.Harness || (!live && r.presence.Online(a.ID)) {
		return a, nil
	}
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetAgentHarness(ctx, db.SetAgentHarnessParams{Harness: harness, ID: a.ID})
	})
	if err != nil {
		return db.Agent{}, err
	}
	a.Harness = harness
	return a, nil
}
