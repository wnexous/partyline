package agents

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// resolveLive resolves the folder as a live Claude Code session on the machine "devbox".
func (f *fixture) resolveLive(t *testing.T, owner authn.Principal, folder string) db.Agent {
	t.Helper()
	a, err := f.reg.ResolveFolder(context.Background(), owner.UserID, FolderID{Machine: "devbox", Folder: folder, Harness: "claude"}, true)
	require.NoError(t, err)
	return a
}

func (f *fixture) intelligence(t *testing.T, id string) db.Intelligence {
	t.Helper()
	i, err := f.st.R().GetIntelligence(context.Background(), id)
	require.NoError(t, err)
	return i
}

func (f *fixture) storedAgent(t *testing.T, id string) db.Agent {
	t.Helper()
	a, err := f.st.R().GetAgent(context.Background(), id)
	require.NoError(t, err)
	return a
}

// legacy inserts an agent the way the v0.3 upgrade leaves it: with its intelligence and no
// folder (unlinked, MIG-02).
func (f *fixture) legacy(t *testing.T, owner authn.Principal, name, handle string) string {
	t.Helper()
	id := store.NewID()
	f.exec(t, "INSERT INTO agents (id, owner_id, name, created_at, harness, machine) VALUES (?, ?, ?, ?, 'claude', 'old-host')",
		id, owner.UserID, name, t0.UnixMilli())
	f.exec(t, "INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at) VALUES (?, 'ai', ?, ?, ?, ?)",
		id, name, handle, t0.UnixMilli(), t0.UnixMilli())
	return id
}

func (f *fixture) adoptions(t *testing.T, agentID string) int {
	t.Helper()
	return f.count(t, "SELECT COUNT(*) FROM audit_log WHERE action = 'agent.adopt' AND target_kind = 'agent' AND target_id = ?", agentID)
}

func (f *fixture) machine(t *testing.T, owner authn.Principal, hostname string) db.Machine {
	t.Helper()
	m, err := f.st.R().GetMachine(context.Background(), db.GetMachineParams{OwnerID: owner.UserID, Hostname: hostname})
	require.NoError(t, err)
	return m
}

// parallel runs fn(i) for i in [0, n) at the same moment and returns the errors.
func parallel(n int, fn func(i int) error) []error {
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// AID-01, AID-02: (owner, machine, folder) creates one identity on first sight; every later
// connection from any harness resolves the same id and the app follows the harness.
func TestResolveFolderCreatesTheIdentityOnceForAnyHarness(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	id := FolderID{Machine: "devbox", Folder: "/home/alice/acme", Harness: "claude"}

	first, err := f.reg.ResolveFolder(ctx, alice.UserID, id, true)
	require.NoError(t, err)
	require.Equal(t, alice.UserID, first.OwnerID)
	require.Equal(t, "devbox", first.Machine)
	require.Equal(t, "/home/alice/acme", first.Folder)
	require.Equal(t, "claude", first.Harness)
	require.Equal(t, first.ID, first.Name, "a folder identity has no v0.3 name: the row stores its id")
	i := f.intelligence(t, first.ID)
	require.Equal(t, "ai", i.Kind)
	require.Equal(t, "Acme", i.DisplayName)
	require.Equal(t, "acme", i.Handle)
	require.Equal(t, "Acme", f.storedAgent(t, first.ID).DisplayName, "the v0.3 display name column mirrors the profile")
	require.Equal(t, "Acme", DisplayName(first), "so the name people read is not the row's id")

	for _, harness := range []string{"opencode", "pi", "claude", "omp"} {
		id.Harness = harness
		again, err := f.reg.ResolveFolder(ctx, alice.UserID, id, true)
		require.NoError(t, err)
		require.Equal(t, first.ID, again.ID, harness)
		require.Equal(t, harness, again.Harness, "the app becomes the harness that connected")
		require.Equal(t, harness, f.storedAgent(t, first.ID).Harness)
	}
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents"))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM intelligences WHERE kind = 'ai'"))
	require.Equal(t, "Acme", f.intelligence(t, first.ID).DisplayName, "reconnecting keeps the profile")
}

// AID-02: a tools-only call (Codex, the tools half of pi/OpenCode) must not relabel a live
// session, but names the app while nobody is connected; a call that names no harness leaves it.
func TestResolveFolderHarnessRules(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	id := FolderID{Machine: "devbox", Folder: "/work/acme", Harness: "claude"}
	live, err := f.reg.ResolveFolder(ctx, alice.UserID, id, true)
	require.NoError(t, err)
	require.Equal(t, "claude", live.Harness)

	session := &fakeSession{}
	require.NoError(t, f.presence.Attach(ctx, live, session))
	id.Harness = "codex"
	tools, err := f.reg.ResolveFolder(ctx, alice.UserID, id, false)
	require.NoError(t, err)
	require.Equal(t, live.ID, tools.ID)
	require.Equal(t, "claude", tools.Harness, "a tools-only call does not relabel a live session")
	require.Equal(t, "claude", f.storedAgent(t, live.ID).Harness)

	require.True(t, f.presence.Detach(ctx, live.ID, session))
	tools, err = f.reg.ResolveFolder(ctx, alice.UserID, id, false)
	require.NoError(t, err)
	require.Equal(t, "codex", tools.Harness, "with nobody connected a tools-only call names the app")
	require.Equal(t, "codex", f.storedAgent(t, live.ID).Harness)

	for _, isLive := range []bool{false, true} {
		id.Harness = ""
		same, err := f.reg.ResolveFolder(ctx, alice.UserID, id, isLive)
		require.NoError(t, err)
		require.Equal(t, "codex", same.Harness, "no harness named (live=%v): the app stays", isLive)
	}

	id.Harness = "claude-code"
	alias, err := f.reg.ResolveFolder(ctx, alice.UserID, id, true)
	require.NoError(t, err)
	require.Equal(t, "claude", alias.Harness, "launcher aliases name the harness")

	id.Folder, id.Harness = "/work/other", "something-new"
	unknown, err := f.reg.ResolveFolder(ctx, alice.UserID, id, true)
	require.NoError(t, err)
	require.Equal(t, DefaultHarness, unknown.Harness, "an unknown harness is the generic agent app")
}

// AID-01: the same folder on two machines, or for two owners, is two identities.
func TestResolveFolderIdentityIsOwnerMachineAndFolder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.user(t, "alice"), f.user(t, "bobby")
	resolve := func(owner authn.Principal, machine, folder string) db.Agent {
		a, err := f.reg.ResolveFolder(ctx, owner.UserID, FolderID{Machine: machine, Folder: folder}, true)
		require.NoError(t, err)
		return a
	}
	devbox, notebook := resolve(alice, "devbox", "/work/acme"), resolve(alice, "notebook", "/work/acme")
	other := resolve(bob, "devbox", "/work/acme")
	require.Len(t, map[string]bool{devbox.ID: true, notebook.ID: true, other.ID: true}, 3)
	require.Equal(t, devbox.ID, resolve(alice, "devbox", "/work/acme").ID)
	require.Equal(t, notebook.ID, resolve(alice, "notebook", "/work/acme").ID)
	require.Equal(t, other.ID, resolve(bob, "devbox", "/work/acme").ID)

	// The same name everywhere: handles tell them apart, the display name is the same (AID-03).
	handles := []string{f.intelligence(t, devbox.ID).Handle, f.intelligence(t, notebook.ID).Handle, f.intelligence(t, other.ID).Handle}
	require.Equal(t, []string{"acme", "acme-2", "acme-3"}, handles)
	for _, a := range []db.Agent{devbox, notebook, other} {
		require.Equal(t, "Acme", f.intelligence(t, a.ID).DisplayName)
	}
}

// AID-03: two folders with the same base name get distinct handles and the same display name.
func TestResolveFolderSameBaseNameGetsDistinctHandles(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice")
	var got []string
	for _, folder := range []string{"/a/notes", "/b/notes", "/c/Notes"} {
		i := f.intelligence(t, f.resolveLive(t, alice, folder).ID)
		require.Equal(t, "Notes", i.DisplayName, folder)
		got = append(got, i.Handle)
	}
	require.Equal(t, []string{"notes", "notes-2", "notes-3"}, got)
}

// AID-01, AID-03: the default display name and handle derive from the folder's last element.
func TestResolveFolderDefaultNames(t *testing.T) {
	cases := []struct{ folder, display, handle string }{
		{"/work/acme", "Acme", "acme"},
		{"/work/my project", "My project", "my-project"},
		{"/work/2024", "2024", "ai-2024"},
		{"/work/Café Órdenes", "Café Órdenes", "cafe-ordenes"},
		{"/work/日本語", "日本語", "ai"},
		{`C:\Users\me\Acme Corp`, "Acme Corp", "acme-corp"},
		{"C:/Users/me/acme", "Acme", "acme"},
		{"/", "/", "ai"},
		{"/" + strings.Repeat("a", 60), strings.ToUpper("a") + strings.Repeat("a", 59), strings.Repeat("a", 36)},
	}
	for _, c := range cases {
		f := newFixture(t) // handles are unique across the instance: start empty for each row
		a := f.resolveLive(t, f.user(t, "alice"), c.folder)
		i := f.intelligence(t, a.ID)
		require.Equal(t, c.display, i.DisplayName, c.folder)
		require.Equal(t, c.handle, i.Handle, c.folder)
	}
}

// requireAPIError checks that err is an API error with the status and code and returns its
// fields.
func requireAPIError(t *testing.T, err error, status int, code dto.ErrorCode) map[string]string {
	t.Helper()
	var he *httpx.Error
	require.True(t, errors.As(err, &he), "not an API error: %v", err)
	require.Equal(t, status, he.Status, "%v", err)
	require.Equal(t, code, he.Code, "%v", err)
	return he.Fields
}

// AID-05: a folder the server cannot use is refused with 422 and creates nothing.
func TestResolveFolderValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	required, invalid := dto.ErrorCodeFolderRequired, dto.ErrorCodeFolderInvalid
	rejected := []struct {
		name, machine, folder string
		code                  dto.ErrorCode
		field                 string
	}{
		{"empty folder", "devbox", "", required, "folder"},
		{"empty machine", "", "/work/acme", required, "machine"},
		{"blank machine", "  \t", "/work/acme", required, "machine"},
		{"NUL", "devbox", "/work/a\x00b", invalid, "folder"},
		{"1025 bytes", "devbox", "/" + strings.Repeat("a", 1024), invalid, "folder"},
		{"relative", "devbox", "work/acme", invalid, "folder"},
		{"relative with dot", "devbox", "./acme", invalid, "folder"},
		{"home shorthand", "devbox", "~/acme", invalid, "folder"},
		{"trailing slash", "devbox", "/work/acme/", invalid, "folder"},
		{"dot-dot segment", "devbox", "/work/../acme", invalid, "folder"},
		{"trailing dot-dot", "devbox", "/work/..", invalid, "folder"},
		{"dot segment", "devbox", "/work/./acme", invalid, "folder"},
		{"invalid UTF-8", "devbox", "/work/\xff", invalid, "folder"},
		{"drive without root", "devbox", `C:acme`, invalid, "folder"},
		{"windows trailing backslash", "devbox", `C:\Users\me\`, invalid, "folder"},
		{"windows trailing slash", "devbox", `C:/Users/me/`, invalid, "folder"},
		{"windows dot-dot", "devbox", `C:\Users\..\me`, invalid, "folder"},
		{"UNC path", "devbox", `\\srv\share\x`, invalid, "folder"},
	}
	for _, c := range rejected {
		_, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: c.machine, Folder: c.folder}, true)
		fields := requireAPIError(t, err, http.StatusUnprocessableEntity, c.code)
		require.Contains(t, fields, c.field, c.name)
		require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM agents"), "%s creates nothing", c.name)
		_, err = f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: c.machine, Folder: c.folder}, false)
		requireAPIError(t, err, http.StatusUnprocessableEntity, c.code)
	}
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM machines"), "a refused connection records no machine")

	accepted := []string{
		"/", "/work/acme", "/" + strings.Repeat("a", 1023), `C:\Users\me\acme`, "C:/Users/me/acme", `C:\`, "C:/",
		"/work/with space/ünï", "/work/a..b", "/work/..hidden", "/work/.dot",
	}
	for _, folder := range accepted {
		_, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: folder}, true)
		require.NoError(t, err, folder)
	}
}

// MIG-02: the first session of a folder links the owner's unlinked identity whose handle is the
// folder's handle; the identity keeps its id and profile, the link is audited and happens once.
func TestResolveFolderAdoptsTheUnlinkedAgentWithTheFolderHandle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	old := f.legacy(t, alice, "atlas-claude", "acme")
	f.exec(t, "UPDATE intelligences SET bio = 'Keeps the books' WHERE id = ?", old)

	for _, live := range []bool{true, false} {
		a, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/acme", Harness: "opencode"}, live)
		require.NoError(t, err)
		require.Equal(t, old, a.ID, "live=%v", live)
		require.Equal(t, "devbox", a.Machine)
		require.Equal(t, "/work/acme", a.Folder)
		require.Equal(t, "opencode", a.Harness)
		require.Equal(t, 1, f.adoptions(t, old), "the second call finds the linked identity and adopts nothing")
		stored := f.storedAgent(t, old)
		require.Equal(t, "/work/acme", stored.Folder)
		require.Equal(t, "atlas-claude", stored.Name, "the v0.3 name stays")
	}
	i := f.intelligence(t, old)
	require.Equal(t, "atlas-claude", i.DisplayName)
	require.Equal(t, "acme", i.Handle)
	require.Equal(t, "Keeps the books", i.Bio)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents"))
	var actor string
	require.NoError(t, f.raw.QueryRowContext(ctx, "SELECT actor_id FROM audit_log WHERE action = 'agent.adopt'").Scan(&actor))
	require.Equal(t, alice.UserID, actor)

	// Linking happens once: the identity is no candidate for another folder with the same handle.
	other, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/other/acme"}, true)
	require.NoError(t, err)
	require.NotEqual(t, old, other.ID)
	require.Equal(t, "acme-2", f.intelligence(t, other.ID).Handle)
	require.Equal(t, 1, f.adoptions(t, old))
	require.Equal(t, 0, f.adoptions(t, other.ID))
}

// MIG-02: --as / PARTYLINE_AGENT names the unlinked identity to adopt, by handle (any case) or
// by its v0.3 name.
func TestResolveFolderAdoptsTheIdentityNamedByAdopt(t *testing.T) {
	cases := []struct{ name, adopt string }{
		{"handle", "atlas-prime"},
		{"handle in another case", "ATLAS-Prime"},
		{"handle with spaces around", "  atlas-prime  "},
		{"v0.3 name", "builder"},
	}
	for _, c := range cases {
		f := newFixture(t)
		ctx := context.Background()
		alice := f.user(t, "alice")
		decoy := f.legacy(t, alice, "decoy", "proj")
		prime := f.legacy(t, alice, "builder", "atlas-prime")
		a, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/proj", Adopt: c.adopt}, true)
		require.NoError(t, err, c.name)
		require.Equal(t, prime, a.ID, "%s: the named identity wins over the folder's own handle", c.name)
		require.Equal(t, "/work/proj", f.storedAgent(t, prime).Folder, c.name)
		require.Equal(t, "", f.storedAgent(t, decoy).Folder, "%s: the folder-handle candidate stays unlinked", c.name)
		require.Equal(t, 1, f.adoptions(t, prime), c.name)
		require.Equal(t, 0, f.adoptions(t, decoy), c.name)
	}
}

// MIG-02: a name that is missing, already linked, another owner's or no handle at all is
// ignored; the folder gets its own identity and the call does not fail.
func TestResolveFolderIgnoresAStaleAdopt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.user(t, "alice"), f.user(t, "bobby")
	bobs := f.legacy(t, bob, "atlas", "atlas")
	linked := f.resolveLive(t, alice, "/work/first")
	linkedHandle := f.intelligence(t, linked.ID).Handle
	require.Equal(t, "first", linkedHandle)

	for i, adopt := range []string{"missing", linkedHandle, "atlas", "../x", "a b", strings.Repeat("a", 80)} {
		folder := fmt.Sprintf("/work/stale%d", i)
		a, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: folder, Adopt: adopt}, true)
		require.NoError(t, err, adopt)
		require.NotEqual(t, linked.ID, a.ID, adopt)
		require.NotEqual(t, bobs, a.ID, "%s: another owner's identity is never adopted", adopt)
		require.Equal(t, folder, a.Folder, adopt)
		require.Equal(t, 0, f.adoptions(t, a.ID), "%s: this was a creation", adopt)
	}
	require.Equal(t, "/work/first", f.storedAgent(t, linked.ID).Folder, "a linked identity stays where it is")
	require.Equal(t, "", f.storedAgent(t, bobs).Folder)
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM audit_log WHERE action = 'agent.adopt'"))
}

// MIG-02: adoption only happens when the folder has no identity yet.
func TestResolveFolderDoesNotAdoptWhenTheFolderAlreadyHasAnIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	own := f.resolveLive(t, alice, "/work/acme")
	old := f.legacy(t, alice, "old", "old-helper")
	a, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/acme", Adopt: "old-helper"}, true)
	require.NoError(t, err)
	require.Equal(t, own.ID, a.ID)
	require.Equal(t, "", f.storedAgent(t, old).Folder)
	require.Equal(t, 0, f.adoptions(t, old))
}

// Concurrent resolves of one new folder create one identity (alloc lock).
func TestResolveFolderConcurrentResolvesCreateOneIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	const n = 16
	ids := make([]string, n)
	errs := parallel(n, func(i int) error {
		harness := []string{"claude", "opencode", "codex", "pi"}[i%4]
		a, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/acme", Harness: harness}, i%2 == 0)
		ids[i] = a.ID
		return err
	})
	for _, err := range errs {
		require.NoError(t, err)
	}
	for _, id := range ids {
		require.Equal(t, ids[0], id)
	}
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents"))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM intelligences WHERE kind = 'ai'"))
}

// Concurrent resolves of different folders with the same base name get distinct handles.
func TestResolveFolderConcurrentFoldersGetDistinctHandles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.user(t, "alice"), f.user(t, "bobby")
	const n = 12
	ids := make([]string, n)
	errs := parallel(n, func(i int) error {
		owner := alice
		if i%3 == 0 {
			owner = bob
		}
		a, err := f.reg.ResolveFolder(ctx, owner.UserID, FolderID{Machine: "devbox", Folder: fmt.Sprintf("/p%d/notes", i)}, true)
		ids[i] = a.ID
		return err
	})
	for _, err := range errs {
		require.NoError(t, err)
	}
	var handles []string
	for _, id := range ids {
		i := f.intelligence(t, id)
		require.Equal(t, "Notes", i.DisplayName)
		handles = append(handles, i.Handle)
	}
	slices.Sort(handles)
	want := []string{"notes"}
	for i := 2; i <= n; i++ {
		want = append(want, fmt.Sprintf("notes-%d", i))
	}
	slices.Sort(want)
	require.Equal(t, want, handles)
}

// MIG-02 under concurrency: one unlinked identity is linked once, to one folder; the others
// get identities of their own.
func TestResolveFolderConcurrentAdoptionLinksOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	old := f.legacy(t, alice, "legacy", "legacy")
	const n = 8
	ids := make([]string, n)
	errs := parallel(n, func(i int) error {
		folder := []string{"/work/one", "/work/two"}[i%2]
		a, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: folder, Adopt: "legacy"}, true)
		ids[i] = a.ID
		return err
	})
	for _, err := range errs {
		require.NoError(t, err)
	}
	for i := range n {
		require.Equal(t, ids[i%2], ids[i], "one folder, one identity")
	}
	require.NotEqual(t, ids[0], ids[1])
	require.Contains(t, []string{ids[0], ids[1]}, old, "one of the folders linked the legacy identity")
	require.Equal(t, 1, f.adoptions(t, old))
	require.Equal(t, 2, f.count(t, "SELECT COUNT(*) FROM agents"))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM audit_log WHERE action = 'agent.adopt'"))
}

// Creation stops at 200 identities per owner (409 conflict "agent limit reached"); existing
// folders still resolve and other owners are not affected.
func TestResolveFolderCapsIdentitiesPerOwner(t *testing.T) {
	require.Equal(t, 200, MaxAgentsPerOwner, "the cap is 200 identities per owner")
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.user(t, "alice"), f.user(t, "bobby")
	tx, err := f.raw.BeginTx(ctx, nil)
	require.NoError(t, err)
	for i := range MaxAgentsPerOwner {
		id := store.NewID()
		_, err := tx.ExecContext(ctx, "INSERT INTO agents (id, owner_id, name, created_at, machine, folder) VALUES (?, ?, ?, ?, 'devbox', ?)",
			id, alice.UserID, id, t0.UnixMilli(), fmt.Sprintf("/seed/%d", i))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	for _, live := range []bool{true, false} {
		_, err = f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/one-too-many"}, live)
		requireAPIError(t, err, http.StatusConflict, dto.ErrorCodeConflict)
		require.ErrorIs(t, err, ErrAgentLimit)
	}
	require.Equal(t, MaxAgentsPerOwner, f.count(t, "SELECT COUNT(*) FROM agents WHERE owner_id = ?", alice.UserID))
	existing, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/seed/7", Harness: "claude"}, true)
	require.NoError(t, err)
	require.Equal(t, "/seed/7", existing.Folder)
	f.resolveLive(t, bob, "/work/one") // the cap is per owner
}

// TREE-02, TREE-04: the machine row records the home folder and the last sighting (written at
// most once a minute) and never loses its label.
func TestResolveFolderRecordsTheMachine(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := f.user(t, "alice"), f.user(t, "bobby")
	resolve := func(owner authn.Principal, folder, home string, live bool) {
		t.Helper()
		_, err := f.reg.ResolveFolder(ctx, owner.UserID, FolderID{Machine: "devbox", Folder: folder, Home: home}, live)
		require.NoError(t, err)
	}

	resolve(alice, "/home/alice/a", "/home/alice", true)
	m := f.machine(t, alice, "devbox")
	require.Equal(t, "/home/alice", m.Home)
	require.Equal(t, t0.UnixMilli(), m.LastSeenAt.Int64)
	require.Equal(t, "", m.Label)
	f.exec(t, "UPDATE machines SET label = 'Dev box' WHERE owner_id = ?", alice.UserID)

	f.clock.Advance(30 * time.Second)
	resolve(alice, "/home/alice/b", "/home/alice", false)
	require.Equal(t, t0.UnixMilli(), f.machine(t, alice, "devbox").LastSeenAt.Int64, "at most one write a minute")

	f.clock.Advance(31 * time.Second)
	resolve(alice, "/home/alice/b", "", false)
	m = f.machine(t, alice, "devbox")
	require.Equal(t, t0.Add(61*time.Second).UnixMilli(), m.LastSeenAt.Int64)
	require.Equal(t, "/home/alice", m.Home, "a call that sends no home keeps the stored one")
	require.Equal(t, "Dev box", m.Label, "the owner's label survives")

	f.clock.Advance(time.Second)
	resolve(alice, "/home/alice/b", "/Users/alice", true)
	m = f.machine(t, alice, "devbox")
	require.Equal(t, "/Users/alice", m.Home, "a new home folder is recorded at once")
	require.Equal(t, "Dev box", m.Label)

	resolve(bob, "/home/bob/a", "/home/bob", true)
	require.Equal(t, "/home/bob", f.machine(t, bob, "devbox").Home, "machines belong to their owner")
	require.Equal(t, "/Users/alice", f.machine(t, alice, "devbox").Home)

	long := strings.Repeat("m", 70)
	_, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: long, Folder: "/work/x"}, true)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("m", MaxMachineLength), f.machine(t, alice, long[:MaxMachineLength]).Hostname)
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents WHERE machine = ?", long[:MaxMachineLength]))
}

// AID-07: the folder and home headers are percent-encoded; the others are plain. A folder that
// is not valid percent-encoding is refused, a home that is not is ignored.
func TestFolderIDFromHeader(t *testing.T) {
	header := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	id, err := FolderIDFromHeader(header(
		dto.HeaderMachine, "dev box", dto.HeaderFolder, url.PathEscape("/home/alice/my project/Café"),
		dto.HeaderHome, url.PathEscape("/home/alice"), dto.HeaderHarness, "opencode", dto.HeaderAdopt, "Old-Helper",
	))
	require.NoError(t, err)
	require.Equal(t, FolderID{Machine: "dev box", Folder: "/home/alice/my project/Café", Home: "/home/alice", Harness: "opencode", Adopt: "Old-Helper"}, id)

	id, err = FolderIDFromHeader(header(dto.HeaderMachine, "devbox", dto.HeaderFolder, "/work/plain"))
	require.NoError(t, err)
	require.Equal(t, FolderID{Machine: "devbox", Folder: "/work/plain"}, id, "a folder with nothing to decode reads as it is")

	id, err = FolderIDFromHeader(header(dto.HeaderMachine, "devbox", dto.HeaderFolder, "%2Fwork%2F100%25", dto.HeaderHome, "%zz"))
	require.NoError(t, err)
	require.Equal(t, FolderID{Machine: "devbox", Folder: "/work/100%", Home: ""}, id, "a literal percent sign is escaped; a broken home is ignored")

	_, err = FolderIDFromHeader(header(dto.HeaderMachine, "devbox", dto.HeaderFolder, "%zz"))
	requireAPIError(t, err, http.StatusUnprocessableEntity, dto.ErrorCodeFolderInvalid)

	require.False(t, HasFolder(header(dto.HeaderMachine, "devbox")))
	require.True(t, HasFolder(header(dto.HeaderFolder, "")), "an empty folder header is still a folder header")
	require.True(t, HasFolder(header(dto.HeaderFolder, "/work")))
}

// SES-01: LookupFolder finds the AI of a folder and changes nothing: it creates no identity,
// adopts no unlinked one, records no machine and relabels nothing.
func TestLookupFolderChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	legacy := f.legacy(t, alice, "acme", "acme")
	id := FolderID{Machine: "devbox", Folder: "/work/acme", Home: "/home/alice", Harness: "opencode", Adopt: "acme"}

	_, ok, err := f.reg.LookupFolder(ctx, alice.UserID, id)
	require.NoError(t, err)
	require.False(t, ok, "an unlinked identity is not the AI of the folder until a session adopts it")
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents"))
	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM machines"))
	require.Equal(t, "", f.storedAgent(t, legacy).Folder)
	require.Equal(t, 0, f.adoptions(t, legacy))

	adopted, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/work/acme", Harness: "claude"}, true)
	require.NoError(t, err)
	require.Equal(t, legacy, adopted.ID)
	got, ok, err := f.reg.LookupFolder(ctx, alice.UserID, id)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, legacy, got.ID)
	require.Equal(t, "claude", f.storedAgent(t, legacy).Harness, "a lookup does not relabel")

	_, ok, err = f.reg.LookupFolder(ctx, f.user(t, "bobby").UserID, id)
	require.NoError(t, err)
	require.False(t, ok, "the lookup is scoped to the owner")
	_, _, err = f.reg.LookupFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "work/acme"})
	requireAPIError(t, err, http.StatusUnprocessableEntity, dto.ErrorCodeFolderInvalid)
	_, _, err = f.reg.LookupFolder(ctx, alice.UserID, FolderID{Folder: "/work/acme"})
	requireAPIError(t, err, http.StatusUnprocessableEntity, dto.ErrorCodeFolderRequired)
}
