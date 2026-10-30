package agents

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// MaxFolderLength is the longest folder path a session may send, in bytes (AID-05).
const MaxFolderLength = 1024

// MachineTouchInterval is how often a machine's last sighting is written: sessions and tool
// calls resolve their folder constantly, the machine row only needs a coarse "last seen".
const MachineTouchInterval = time.Minute

// SeenTouchInterval is how often a tools-only call stamps its AI's last_seen_at (SESS-50): an AI used only
// through a tools call alone (no WebSocket) holds no socket, so its calls are the only sign of life it gives.
const SeenTouchInterval = time.Minute

// FolderID is what a session tells the server about itself on every connection and tool call:
// where it works (machine and canonical folder) and, for the app and the adoption of a v0.3
// identity, the harness and the name it was started with (AID-07, MIG-02).
type FolderID struct {
	// Machine is the hostname the CLI reports.
	Machine string
	// Folder is the canonical folder: absolute, symlinks resolved, no trailing separator.
	Folder string
	// MainFolder is the canonical folder of the main worktree when the session runs in a linked git
	// worktree: it identifies the AI, while Folder stays the session's own folder (SESS-01). Empty
	// outside a linked worktree, and a main folder equal to Folder counts as absent.
	MainFolder string
	// Home is the canonical home folder of the machine's user; the tree shows "~" for it
	// (TREE-02). Optional: an unusable one is ignored.
	Home string
	// Harness is the harness id (claude, opencode, ...). Empty leaves the app of an existing
	// identity alone.
	Harness string
	// Adopt is a handle or a v0.3 agent name (--as, PARTYLINE_AGENT): the owner's unlinked
	// identity to link to this folder when the folder has none yet (MIG-02).
	Adopt string
	// Org is the organization a new AI joins (--org, PARTYLINE_ORG): a slug or an id. Empty means the
	// creator's default organization (ORG-04).
	Org string
	// Position is the name of the position a new AI holds (--position, PARTYLINE_POSITION). Empty
	// means none (POS-07).
	Position string
	// Parent is the @handle or id of the AI that starts this one (--parent, PARTYLINE_PARENT). It counts
	// when the token holder may act for it (POS-08).
	Parent string
}

// FolderIDFromHeader reads the folder headers of a session (AID-07): the folder, the home
// folder and the position are percent-encoded (url.PathEscape), the machine, harness, the name to
// adopt, the organization and the parent are plain. A folder that is not valid percent-encoding is
// 422 folder_invalid, and a position that is not is 422 position_unknown; a home folder that is not
// is ignored. A harness that is no longer supported (codex, gemini, antigravity) is 410 harness_removed,
// on the WebSocket and on every agent route (AD-035). A missing header reads as empty, and so does a blank one. The result is not validated:
// ResolveFolder and LookupFolder do that (an empty folder is 422 folder_required).
func FolderIDFromHeader(h http.Header) (FolderID, error) {
	folder, err := url.PathUnescape(h.Get(dto.HeaderFolder))
	if err != nil {
		return FolderID{}, folderError(dto.ErrorCodeFolderInvalid, "folder", "the folder is not valid percent-encoding")
	}
	mainFolder, err := url.PathUnescape(h.Get(dto.HeaderMainFolder))
	if err != nil {
		return FolderID{}, folderError(dto.ErrorCodeFolderInvalid, "main_folder", "the main folder is not valid percent-encoding")
	}
	home, err := url.PathUnescape(h.Get(dto.HeaderHome))
	if err != nil {
		home = ""
	}
	position, err := url.PathUnescape(h.Get(dto.HeaderPosition))
	if err != nil {
		return FolderID{}, folderError(dto.ErrorCodePositionUnknown, "position", "the position is not valid percent-encoding")
	}
	if label, removed := RemovedHarness(h.Get(dto.HeaderHarness)); removed {
		return FolderID{}, &httpx.Error{
			Status: http.StatusGone, Code: dto.ErrorCodeHarnessRemoved,
			Message: label + " is no longer supported by Partyline; use Claude Code, pi, OpenCode or omp",
		}
	}
	return FolderID{
		Machine: h.Get(dto.HeaderMachine), Folder: folder, MainFolder: mainFolder, Home: home,
		Harness: h.Get(dto.HeaderHarness), Adopt: h.Get(dto.HeaderAdopt),
		Org: strings.TrimSpace(h.Get(dto.HeaderOrg)), Position: strings.TrimSpace(position),
		Parent: strings.TrimSpace(h.Get(dto.HeaderParent)),
	}, nil
}

// folderError is a 422 for a session whose machine, folder, organization or position the server cannot use.
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
	return checkFolder(folder, "folder", "the folder")
}

// ValidateMainFolder checks the main worktree folder a session sent (SESS-04) with the rules of the
// folder, naming main_folder in the 422. An empty one is fine: the session is not in a linked
// worktree, or it is an old CLI that sends none.
func ValidateMainFolder(mainFolder string) error {
	if mainFolder == "" {
		return nil
	}
	return checkFolder(mainFolder, "main_folder", "the main folder")
}

// checkFolder reports whether folder can identify an AI (AID-05, SESS-04); field and noun name it
// in the 422 folder_invalid.
func checkFolder(folder, field, noun string) error {
	invalid := func(why string) error { return folderError(dto.ErrorCodeFolderInvalid, field, noun+" "+why) }
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

// key is the folder that identifies the AI of a session (SESS-01): the main worktree's folder when
// the session runs in a linked worktree, else its own. A main folder equal to the folder is absent.
func (id FolderID) key() string {
	if id.MainFolder != "" && id.MainFolder != id.Folder {
		return id.MainFolder
	}
	return id.Folder
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
// on first sight with the folder's name (AID-01, AID-02, AID-03). A session in a linked git
// worktree resolves the AI of the main worktree instead (FolderID.MainFolder, SESS-01, SESS-06).
// Any harness opened in that folder resolves the same identity, and its app follows the harness:
// always for a live session (live), and for a tools-only call (the tools half of pi and
// OpenCode) only while nobody is connected, so a tool call never relabels a live session (AID-04).
//
// When the folder has no identity yet, the owner's unlinked identity (one of v0.3, without a
// folder) is linked to it instead of creating one: the one named by FolderID.Adopt, else the
// one whose handle is the folder's handle; a name that is missing or already linked is ignored,
// and an identity is linked once (MIG-02). Creation stops at MaxAgentsPerOwner (ErrAgentLimit).
// An invalid machine or folder is a 422 (ValidateFolder) and changes nothing.
//
// What the session asked for with --org, --position and --parent places a new AI (place, ORG-04); an AI
// that already exists keeps its home organization and its creator, and only a later --position can move
// it (restart, POS-07).
func (r *Registry) ResolveFolder(ctx context.Context, ownerID string, id FolderID, live bool) (db.Agent, error) {
	id.Machine = cleanText(id.Machine, MaxMachineLength)
	if err := ValidateFolder(id.Machine, id.Folder); err != nil {
		return db.Agent{}, err
	}
	if err := ValidateMainFolder(id.MainFolder); err != nil {
		return db.Agent{}, err
	}
	id.Harness = cleanHarness(id.Harness)
	id.Adopt = strings.ToLower(strings.TrimSpace(id.Adopt))
	if err := r.touchMachine(ctx, ownerID, id.Machine, id.Home); err != nil {
		return db.Agent{}, err
	}
	a, err := r.store.R().GetAgentByFolder(ctx, db.GetAgentByFolderParams{OwnerID: ownerID, Machine: id.Machine, Folder: id.key()})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if r.afterFolderMiss != nil {
			r.afterFolderMiss()
		}
		a, err = r.linkOrCreate(ctx, ownerID, id)
	case err == nil:
		err = r.restart(ctx, ownerID, a, id)
	}
	if err != nil {
		return db.Agent{}, err
	}
	return r.relabel(ctx, a, id.Harness, live)
}

// TouchSeen stamps last_seen_at of an AI whose request came without a socket (the HTTP calls of a tools-only
// process: a tool call or a session read), at most once per SeenTouchInterval per AI and only while the
// AI has no live session: a live session stamps it when it connects and when it ends. It is not presence: the
// AI stays offline, so the pushes and the "also open elsewhere" notice are unchanged, and `partyline agents`
// shows "Nm ago" (SESS-50). A failed write is not worth failing the call over.
func (r *Registry) TouchSeen(ctx context.Context, agentID string) {
	if r.presence.Online(agentID) {
		return
	}
	now := r.now()
	r.seenMu.Lock()
	if at, ok := r.seen[agentID]; ok && now.Sub(at) < SeenTouchInterval {
		r.seenMu.Unlock()
		return
	}
	r.seen[agentID] = now // reserved before the write: concurrent calls do not all write
	r.seenMu.Unlock()
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.TouchAgent(ctx, db.TouchAgentParams{LastSeenAt: nullMillis(now), ID: agentID})
	})
	if err != nil {
		r.seenMu.Lock()
		delete(r.seen, agentID) // the next call tries again
		r.seenMu.Unlock()
	}
}

// RegisterFolder resolves the AI of the machine and folder like a tools-only call would
// (ResolveFolder without a live session) and returns it with its owner; the AI stays offline.
// `partyline channel --tools-only` registers this way (PUSH-02).
func (r *Registry) RegisterFolder(ctx context.Context, ownerID string, id FolderID) (db.GetAgentWithOwnerRow, error) {
	a, err := r.ResolveFolder(ctx, ownerID, id, false)
	if err != nil {
		return db.GetAgentWithOwnerRow{}, err
	}
	return r.store.R().GetAgentWithOwner(ctx, a.ID)
}

// LookupFolder returns the owner's AI of the machine and folder (of the main worktree, for a
// session in a linked worktree), if there is one, and changes nothing: no identity is created, no
// unlinked one adopted, no machine recorded and no app relabeled (SES-01). An invalid machine or
// folder is a 422 like for ResolveFolder.
func (r *Registry) LookupFolder(ctx context.Context, ownerID string, id FolderID) (db.Agent, bool, error) {
	machine := cleanText(id.Machine, MaxMachineLength)
	if err := ValidateFolder(machine, id.Folder); err != nil {
		return db.Agent{}, false, err
	}
	if err := ValidateMainFolder(id.MainFolder); err != nil {
		return db.Agent{}, false, err
	}
	a, err := r.store.R().GetAgentByFolder(ctx, db.GetAgentByFolderParams{OwnerID: ownerID, Machine: machine, Folder: id.key()})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Agent{}, false, nil
	}
	return a, err == nil, err
}

// linkOrCreate gives a folder without an identity one: the owner's unlinked identity it adopts,
// else a new one. allocMu serializes it with every other resolution, so concurrent sessions of
// one new folder end up with the same identity; the unique index on (owner, machine, folder)
// is the last guard.
func (r *Registry) linkOrCreate(ctx context.Context, ownerID string, id FolderID) (db.Agent, error) {
	r.allocMu.Lock()
	defer r.allocMu.Unlock()
	var out db.Agent
	var changed string // the organization whose chart this call changed
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		changed = ""
		a, err := q.GetAgentByFolder(ctx, db.GetAgentByFolderParams{OwnerID: ownerID, Machine: id.Machine, Folder: id.key()})
		if !errors.Is(err, sql.ErrNoRows) { // found (created while this call waited), or a failure
			out = a
			if err == nil {
				changed, err = r.startExisting(ctx, q, ownerID, a, id)
			}
			return err
		}
		cand, by, found, err := adoptionCandidate(ctx, q, ownerID, id)
		if err != nil {
			return err
		}
		if found { // the start rules refuse before anything is linked: the same transaction
			if out, err = r.adopt(ctx, q, cand, by, id); err == nil {
				changed, err = r.startExisting(ctx, q, ownerID, out, id)
			}
			return err
		}
		out, err = r.createFolderAgent(ctx, q, ownerID, id)
		changed = out.OrgID.String // the organization has a new member
		return err
	})
	if err == nil && changed != "" && r.bus != nil {
		r.bus.Publish(events.OrgChanged{OrgID: changed})
	}
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
		// v0.3 agent names were slugs of what the user typed (--name "Billing API!").
		for _, find := range []func() (db.Agent, bool, error){
			func() (db.Agent, bool, error) { return byHandle(id.Adopt) },
			func() (db.Agent, bool, error) { return byName(Slug(id.Adopt)) },
		} {
			if a, found, err = find(); found || err != nil {
				return a, "as", found, err
			}
		}
	}
	a, found, err = byHandle(handles.Default(handles.AI, folderBase(id.key())))
	return a, "folder", found, err
}

// adopt links the unlinked identity to the session's machine and folder (the main worktree's folder
// for a session in a linked worktree); it keeps its id, history, groups and profile. The link is
// audited.
func (r *Registry) adopt(ctx context.Context, q *db.Queries, a db.Agent, by string, id FolderID) (db.Agent, error) {
	harness := id.Harness
	if harness == "" {
		harness = a.Harness
	}
	n, err := q.AdoptAgent(ctx, db.AdoptAgentParams{Machine: id.Machine, Folder: id.key(), Harness: harness, ID: a.ID})
	if err != nil {
		return db.Agent{}, err
	}
	if n != 1 { // linked meanwhile: impossible inside this write transaction
		return db.Agent{}, errors.New("agent is already linked to a folder")
	}
	a.Machine, a.Folder, a.Harness = id.Machine, id.key(), harness
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
// from it (AID-01, AID-03). The AI is placed in the same transaction (ORG-02, ORG-04): place decides
// its home organization, creator, permission and position from what the session asked for, and the AI
// joins that organization with them. Nothing is created when the placement is refused.
func (r *Registry) createFolderAgent(ctx context.Context, q *db.Queries, ownerID string, id FolderID) (db.Agent, error) {
	n, err := q.CountAgentsByOwner(ctx, ownerID)
	if err != nil {
		return db.Agent{}, err
	}
	if n >= MaxAgentsPerOwner {
		return db.Agent{}, ErrAgentLimit
	}
	where, err := place(ctx, q, ownerID, id)
	if err != nil {
		return db.Agent{}, err
	}
	agentID := store.NewID()
	now := r.now()
	if err := q.InsertFolderAgent(ctx, db.InsertFolderAgentParams{
		ID: agentID, OwnerID: ownerID, Name: agentID, CreatedAt: now.UnixMilli(),
		Harness: HarnessID(id.Harness), Machine: id.Machine, Folder: id.key(),
		OrgID: sql.NullString{String: where.orgID, Valid: true}, CreatorIid: sql.NullString{String: where.creatorIID, Valid: true},
	}); err != nil {
		return db.Agent{}, err
	}
	base := folderBase(id.key())
	if _, err := intelligences.Create(ctx, q, intelligences.NewParams{
		ID: agentID, Kind: handles.AI, DisplayName: handles.DisplayName(base), HandleBase: base, Now: now,
	}); err != nil {
		return db.Agent{}, err
	}
	if err := orgs.AddMember(ctx, q, orgs.AddMemberInput{
		OrgID: where.orgID, IID: agentID, Permission: where.permission, PositionID: where.positionID,
		InviterIID: where.inviterIID, Now: now,
	}); err != nil {
		return db.Agent{}, err
	}
	if r.audit != nil {
		detail := map[string]any{"iid": agentID, "by": "start", "permission": string(where.permission)}
		if where.creatorIID != ownerID {
			detail["creator"] = where.creatorIID // an AI the token holder hosts started it (--parent)
		}
		if where.positionID != "" {
			detail["position_id"] = where.positionID
		}
		if err := r.audit.Record(ctx, q, audit.Entry{
			ActorID: ownerID, Action: audit.ActionOrgMemberJoin, TargetKind: audit.TargetOrganization, TargetID: where.orgID,
			Detail: detail,
		}); err != nil {
			return db.Agent{}, err
		}
	}
	return q.GetAgent(ctx, agentID)
}

// cleanHarness is the harness id a header value stands for; a missing one stays empty (it
// leaves the app alone), an unknown one is "agent".
func cleanHarness(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return HarnessID(v)
}

// relabel makes harness the agent's app (AID-02). An empty harness changes nothing, and a
// tools-only call (live false) leaves the app of an agent that has a live session: the check and
// the write cannot interleave with a session attaching (appMu).
func (r *Registry) relabel(ctx context.Context, a db.Agent, harness string, live bool) (db.Agent, error) {
	if harness == "" || harness == a.Harness {
		return a, nil
	}
	r.appMu.Lock()
	defer r.appMu.Unlock()
	if !live && r.presence.Online(a.ID) {
		return a, nil
	}
	if r.afterAppCheck != nil {
		r.afterAppCheck()
	}
	if err := r.setApp(ctx, a.ID, harness); err != nil {
		return db.Agent{}, err
	}
	a.Harness = harness
	return a, nil
}

func (r *Registry) setApp(ctx context.Context, agentID, harness string) error {
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetAgentHarness(ctx, db.SetAgentHarnessParams{Harness: harness, ID: agentID})
	})
}

// Attach is AttachSession for a connection that sends no harness session id.
func (r *Registry) Attach(ctx context.Context, a db.Agent, s Session, harness, liveID, preferred string, takeover bool) (Live, error) {
	live, _, err := r.AttachSession(ctx, a, s, AttachRequest{Harness: harness, LiveID: liveID, Preferred: preferred, Takeover: takeover})
	return live, err
}

// AttachSession makes s the live session the connection prefers (Presence.Attach) and returns the session it
// became and the key its per-room state is stored under (SessionKey, SN-01). The AI's pinned principal session
// is read here, under the same lock that serializes every attach, and written back when the connection changes
// it (the first session with an id, --principal, --take-over; SESS-52, SESS-57). Only the principal session
// ("") stamps the AI's connect time and makes the session's harness its app (AID-02): a parallel or worktree
// session shares the AI without relabeling it (SESS-17). A session that replaces an older socket of the same
// process is not a new session: it keeps the label, the app and the connect time.
func (r *Registry) AttachSession(ctx context.Context, a db.Agent, s Session, req AttachRequest) (Live, string, error) {
	r.appMu.Lock()
	defer r.appMu.Unlock()
	req.Harness = cleanHarness(req.Harness)
	current, err := r.store.R().GetAgent(ctx, a.ID)
	if err != nil {
		return Live{}, "", err
	}
	req.Pinned = current.PrincipalSession
	if req.Pinned == "" {
		// With no pin, a remembered parallel label still proves the AI has had an identified session:
		// a client that sends no id must not take the principal role from it (SESS-64).
		had, err := r.store.R().HasSessionLabels(ctx, a.ID)
		if err != nil {
			return Live{}, "", err
		}
		req.HadSessionID = had
	} else {
		req.HadSessionID = true
	}
	if req.SessionID != "" {
		// A parallel session's label by harness session id outlives the server (SESS-54).
		label, err := r.store.R().GetSessionLabel(ctx, db.GetSessionLabelParams{
			AgentID: a.ID, HarnessSession: req.SessionID, UpdatedAt: r.now().Add(-sessionLabelRetention).UnixMilli(),
		})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Live{}, "", err
		}
		req.StoredLabel = label
	}
	out := r.presence.Attach(ctx, a, s, req)
	// The key of the principal is its pinned id, or "principal" when nothing is pinned (SN-01); the role this
	// attach pins is the one the session has now, not the one the AI had when the attach started.
	pinned := req.Pinned
	if out.Pin != nil {
		pinned = *out.Pin
	}
	key := SessionKey(req.SessionID, out.Live.Label, pinned)
	if req.SessionID != "" && out.Fresh {
		if err := r.rememberLabel(ctx, a.ID, req.SessionID, out.Live.Label); err != nil {
			return out.Live, key, err
		}
	}
	if out.Pin != nil {
		if err := r.setPin(ctx, a.ID, *out.Pin); err != nil {
			return out.Live, key, err
		}
	}
	if !out.Fresh || out.Live.Label != "" {
		return out.Live, key, nil
	}
	if req.Harness != "" {
		if err := r.setApp(ctx, a.ID, req.Harness); err != nil {
			return out.Live, key, err
		}
	}
	return out.Live, key, r.MarkConnected(ctx, a.ID)
}

// sessionLabelRetention is how long the database remembers the label of a parallel session that did not come back.
const sessionLabelRetention = 30 * 24 * time.Hour

// rememberLabel records the label a session with a harness session id attached with, and drops what is
// older than sessionLabelRetention; the principal ("") has none to remember (SESS-54).
func (r *Registry) rememberLabel(ctx context.Context, agentID, sessionID, label string) error {
	now := r.now()
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		if label == "" {
			return q.DeleteSessionLabel(ctx, db.DeleteSessionLabelParams{AgentID: agentID, HarnessSession: sessionID})
		}
		if err := q.UpsertSessionLabel(ctx, db.UpsertSessionLabelParams{
			AgentID: agentID, HarnessSession: sessionID, Label: label, UpdatedAt: now.UnixMilli(),
		}); err != nil {
			return err
		}
		return q.PruneSessionLabels(ctx, db.PruneSessionLabelsParams{AgentID: agentID, UpdatedAt: now.Add(-sessionLabelRetention).UnixMilli()})
	})
}

func (r *Registry) setPin(ctx context.Context, agentID, sessionID string) error {
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetAgentPrincipalSession(ctx, db.SetAgentPrincipalSessionParams{PrincipalSession: sessionID, ID: agentID})
	})
}

// PrincipalGone is when the AI's principal session left while another session is live (SESS-61).
func (r *Registry) PrincipalGone(agentID string) (time.Time, bool) {
	return r.presence.PrincipalGone(agentID)
}

// Sessions returns the agent's live sessions, the principal first (SESS-32); nil when it is offline.
func (r *Registry) Sessions(agentID string) []Live { return r.presence.Sessions(agentID) }

// Live returns the agent's live session, if any.
func (r *Registry) Live(agentID string) (Live, bool) { return r.presence.Live(agentID) }
