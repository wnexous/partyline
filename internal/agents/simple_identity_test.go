package agents

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
)

// siHome is the home folder the sessions of these tests send.
const siHome = "/home/alice"

// resolveIn resolves a live session of the folder on "devbox" with siHome as its home folder.
func (f *fixture) resolveIn(t *testing.T, owner authn.Principal, id FolderID) db.Agent {
	t.Helper()
	if id.Machine == "" {
		id.Machine = "devbox"
	}
	if id.Home == "" {
		id.Home = siHome
	}
	id.Harness = "claude"
	a, err := f.reg.ResolveFolder(context.Background(), owner.UserID, id, true)
	require.NoError(t, err)
	return a
}

// SI-04, SI-06: a session in a folder without an AI of its own, inside the folder of an AI of the same owner and
// machine, resolves that AI and creates nothing; the nearest folder above wins, and an AI whose folder matches
// exactly always wins over one above it. A linked worktree whose main folder has no AI looks above the main
// folder the same way.
func TestResolveFolderJoinsTheAIOfTheNearestFolderAbove(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice")
	atlas := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas"})

	sub := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/server/cmd"})
	require.Equal(t, atlas.ID, sub.ID, "SI-04: a subfolder is a session of the AI of the folder above it")
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents"), "SI-04: nothing is created")
	require.Equal(t, "/home/alice/projects/atlas", f.storedAgent(t, atlas.ID).Folder, "the AI keeps its folder")

	// A linked worktree whose main folder is a subfolder without an AI joins the AI above the main folder.
	wt := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/wt/server-fix", MainFolder: "/home/alice/projects/atlas/server"})
	require.Equal(t, atlas.ID, wt.ID, "SI-04: the main folder is what is looked above")

	// An AI of its own in a subfolder (--new-ai) keeps its identity, and the folders below it are its own.
	server := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/server", NewAI: true})
	require.NotEqual(t, atlas.ID, server.ID)
	require.Equal(t, server.ID, f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/server"}).ID,
		"SI-06: an exact match wins over the folder above")
	require.Equal(t, server.ID, f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/server/cmd"}).ID,
		"SI-04: the nearest folder above wins")
	require.Equal(t, atlas.ID, f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/web"}).ID)

	// Only the owner's AIs on the same machine count.
	laptop := f.resolveIn(t, alice, FolderID{Machine: "laptop", Folder: "/home/alice/projects/atlas/web"})
	require.NotEqual(t, atlas.ID, laptop.ID, "another machine is another AI")
	bob := f.user(t, "bob")
	bobs := f.resolveIn(t, bob, FolderID{Folder: "/home/alice/projects/atlas/web"})
	require.NotEqual(t, atlas.ID, bobs.ID, "another owner's session never joins alice's AI")
	require.Equal(t, 4, f.count(t, "SELECT COUNT(*) FROM agents"))

	// A folder whose name only starts like an AI's folder is not inside it.
	sibling := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas-docs"})
	require.NotEqual(t, atlas.ID, sibling.ID)
}

// SI-05: the search never considers the home folder, a folder above it or the root: an AI in any of them does not
// take the sessions of the folders below. A session that sends no home folder has no search at all.
func TestResolveFolderNeverJoinsTheHomeFolderOrAbove(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice")
	ctx := context.Background()
	// AIs of the home folder, of the folder above it and of the root; a session without a home folder creates them.
	for _, folder := range []string{"/home/alice", "/home", "/"} {
		_, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: folder, Harness: "claude"}, true)
		require.NoError(t, err)
	}
	homeAI := f.resolveIn(t, alice, FolderID{Folder: "/home/alice"})

	notes := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/notes"})
	require.NotEqual(t, homeAI.ID, notes.ID, "SI-05: the home folder's AI does not take its subfolders")
	require.Equal(t, "/home/alice/notes", notes.Folder)
	other := f.resolveIn(t, alice, FolderID{Folder: "/srv/data/x"})
	require.Equal(t, "/srv/data/x", other.Folder, "SI-05: the root's AI does not take the folders outside home")
	other2 := f.resolveIn(t, alice, FolderID{Folder: "/home/bob/x", Home: "/home/alice"})
	require.Equal(t, "/home/bob/x", other2.Folder, "SI-05: the folder above home does not take a folder below it")

	// An AI below home takes its subfolders, but a session that sent no home folder does not look.
	a, err := f.reg.ResolveFolder(ctx, alice.UserID, FolderID{Machine: "devbox", Folder: "/home/alice/notes/today", Harness: "claude"}, true)
	require.NoError(t, err)
	require.Equal(t, "/home/alice/notes/today", a.Folder, "without a home folder there is no search")
	require.Equal(t, notes.ID, f.resolveIn(t, alice, FolderID{Folder: "/home/alice/notes/later"}).ID)
}

// SI-07: a launch with --parent or --new-ai gets an AI of its own folder, as before; so does a linked worktree
// launched with either.
func TestResolveFolderOwnAIWithParentOrNewAI(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice")
	atlas := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas"})

	child := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/child", Parent: "@atlas"})
	require.NotEqual(t, atlas.ID, child.ID)
	require.Equal(t, "/home/alice/projects/atlas/child", child.Folder, "SI-07: --parent gives the folder its own AI")
	require.Equal(t, atlas.ID, f.storedAgent(t, child.ID).CreatorIid.String, "and the parent created it")

	fresh := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/tools", NewAI: true})
	require.NotEqual(t, atlas.ID, fresh.ID)
	require.Equal(t, "/home/alice/projects/atlas/tools", fresh.Folder, "SI-07: --new-ai gives the folder its own AI")
	require.Equal(t, alice.UserID, f.storedAgent(t, fresh.ID).CreatorIid.String)

	// Once the folder has its own AI, later sessions without the flag resolve it (SI-06).
	require.Equal(t, fresh.ID, f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas/tools"}).ID)
}

// SI-08: GET /agent/session's lookup resolves a subfolder to the AI above it like a session would, and writes
// nothing; with --new-ai or --parent it finds no AI for a folder that has none. CheckStart judges the AI it finds.
func TestLookupFolderFindsTheAIOfTheFolderAbove(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	atlas := f.resolveIn(t, alice, FolderID{Folder: "/home/alice/projects/atlas"})
	machines := f.count(t, "SELECT COUNT(*) FROM machines")

	sub := FolderID{Machine: "devbox", Folder: "/home/alice/projects/atlas/server", Home: siHome}
	got, ok, err := f.reg.LookupFolder(ctx, alice.UserID, sub)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, atlas.ID, got.ID, "SI-08: the lookup resolves like the session")

	for _, own := range []FolderID{
		{Machine: "devbox", Folder: "/home/alice/projects/atlas/server", Home: siHome, NewAI: true},
		{Machine: "devbox", Folder: "/home/alice/projects/atlas/server", Home: siHome, Parent: "@atlas"},
	} {
		_, ok, err = f.reg.LookupFolder(ctx, alice.UserID, own)
		require.NoError(t, err)
		require.False(t, ok, "SI-07: a session that asks for its own AI has none yet")
	}
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM agents"), "the lookup creates nothing")
	require.Equal(t, machines, f.count(t, "SELECT COUNT(*) FROM machines"), "and records no machine")

	// The start rules of the AI above apply: --org may only name its home organization.
	sub.Org = "no-such-org"
	require.Error(t, f.reg.CheckStart(ctx, alice.UserID, sub))
}

func TestAncestorFolders(t *testing.T) {
	for _, tc := range []struct {
		folder, home string
		want         []string
	}{
		{"/home/a/p/q/r", "/home/a", []string{"/home/a/p/q", "/home/a/p"}},
		{"/home/a/p", "/home/a", nil},
		{"/home/a", "/home/a", nil},
		{"/srv/x/y", "/home/a", []string{"/srv/x", "/srv"}},
		{"/home/ab/x", "/home/a", []string{"/home/ab"}},
		{"/home/a/p/q", "", nil},
		{"/home/a/p/q", "home/a", nil},
		{"/", "/home/a", nil},
		{`C:\Users\a\p\q`, `C:\Users\a`, []string{`C:\Users\a\p`}},
		{`C:\data\x`, `C:\Users\a`, []string{`C:\data`}},
	} {
		require.Equal(t, tc.want, ancestorFolders(tc.folder, tc.home), "%s in %s", tc.folder, tc.home)
	}
}
