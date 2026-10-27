package channel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gitAnswer is what the fake git answers to one command.
type gitAnswer struct {
	out string
	err error
}

// fakeGit answers the commands of the worktree detection from answers, keyed by the arguments
// ("rev-parse --git-dir"), and records the folder of every command. A command the test did not
// give fails the test, so a helper that runs a different git than the design says is caught.
type fakeGit struct {
	t       *testing.T
	answers map[string]gitAnswer
	dirs    []string
}

func (f *fakeGit) run(_ context.Context, dir string, args ...string) ([]byte, error) {
	f.t.Helper()
	f.dirs = append(f.dirs, dir)
	cmd := strings.Join(args, " ")
	a, ok := f.answers[cmd]
	require.True(f.t, ok, "unexpected git command: git -C %s %s", dir, cmd)
	return []byte(a.out), a.err
}

// porcelain is the `git worktree list --porcelain` output of a main worktree and one linked
// worktree below it.
func porcelain(main, wt string) string {
	return "worktree " + main + "\nHEAD 1111111111111111111111111111111111111111\nbranch refs/heads/main\n\n" +
		"worktree " + wt + "\nHEAD 1111111111111111111111111111111111111111\nbranch refs/heads/wt\n"
}

// SESS-02: the detection is bounded, so a slow git never holds a launch up.
func TestWorktreeTimeoutIsOneSecond(t *testing.T) {
	require.Equal(t, time.Second, worktreeTimeout)
}

// SESS-01, SESS-03: a session whose two git dirs differ is in a linked worktree, and the main folder
// is the first `worktree` entry of the list. Git prints the common dir relative to the worktree in
// some layouts, so the two outputs are made absolute against the session's folder before they are
// compared.
func TestMainWorktreeFolderFindsTheMainWorktree(t *testing.T) {
	main := realDir(t, "repo")
	wt := filepath.Join(filepath.Dir(main), "wt")
	relative := &fakeGit{t: t, answers: map[string]gitAnswer{
		"rev-parse --git-common-dir": {out: ".git\n"},
		"rev-parse --git-dir":        {out: ".git/worktrees/wt\n"},
		"worktree list --porcelain":  {out: porcelain(main, wt)},
	}}
	require.Equal(t, main, mainWorktreeFolder(relative.run, worktreeTimeout, wt))
	require.Equal(t, []string{wt, wt, wt}, relative.dirs, "every git command runs in the session's folder")

	absolute := &fakeGit{t: t, answers: map[string]gitAnswer{
		"rev-parse --git-common-dir": {out: main + "/.git\n"},
		"rev-parse --git-dir":        {out: main + "/.git/worktrees/wt\n"},
		"worktree list --porcelain":  {out: porcelain(main, wt)},
	}}
	require.Equal(t, main, mainWorktreeFolder(absolute.run, worktreeTimeout, wt))
}

// SESS-02, SESS-03: the main worktree, a submodule and every failure are no main folder: the session
// starts and resolves by its own folder as before.
func TestMainWorktreeFolderIsEmptyOffALinkedWorktree(t *testing.T) {
	main := realDir(t, "repo")
	wt := filepath.Join(filepath.Dir(main), "wt")
	cases := []struct {
		name    string
		answers map[string]gitAnswer
	}{
		{"the main worktree", map[string]gitAnswer{
			"rev-parse --git-common-dir": {out: main + "/.git"},
			"rev-parse --git-dir":        {out: main + "/.git"},
		}},
		{"a submodule", map[string]gitAnswer{
			"rev-parse --git-common-dir": {out: main + "/.git/modules/sub"},
			"rev-parse --git-dir":        {out: main + "/.git/modules/sub"},
		}},
		{"a bare repository", map[string]gitAnswer{
			"rev-parse --git-common-dir": {out: "/bare.git"},
			"rev-parse --git-dir":        {out: "/bare.git/worktrees/wt"},
			"worktree list --porcelain":  {out: "worktree /bare.git\nbare\n"},
		}},
		{"a failed rev-parse", map[string]gitAnswer{
			"rev-parse --git-common-dir": {err: exec.ErrNotFound},
		}},
		{"a failed common dir", map[string]gitAnswer{
			"rev-parse --git-common-dir": {out: ".git"},
			"rev-parse --git-dir":        {err: exec.ErrNotFound},
		}},
		{"no output", map[string]gitAnswer{
			"rev-parse --git-common-dir": {out: "\n"},
			"rev-parse --git-dir":        {out: main + "/.git/worktrees/wt"},
		}},
		{"a failed list and a common dir that is no .git", map[string]gitAnswer{
			"rev-parse --git-common-dir": {out: "/repo/.git-worktrees/wt"},
			"rev-parse --git-dir":        {out: "/repo/.git-worktrees/wt/x"},
			"worktree list --porcelain":  {err: exec.ErrNotFound},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			git := &fakeGit{t: t, answers: c.answers}
			require.Empty(t, mainWorktreeFolder(git.run, worktreeTimeout, wt))
		})
	}

	// A missing git is an error like any other: the session starts without a main folder.
	missing := func(context.Context, string, ...string) ([]byte, error) {
		return nil, &exec.Error{Name: "git", Err: exec.ErrNotFound}
	}
	require.Empty(t, mainWorktreeFolder(missing, worktreeTimeout, wt))

	// A git that does not answer within the bound is no main folder either.
	slow := func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	require.Empty(t, mainWorktreeFolder(slow, 20*time.Millisecond, wt))
	require.Less(t, time.Since(start), 5*time.Second, "the slow git is cut off")
}

// SESS-03: the main folder is canonical like every folder the CLI sends, and a failed list falls back
// to the parent of a `.git` common dir.
func TestMainWorktreeFolderIsCanonical(t *testing.T) {
	main := realDir(t, "repo")
	link := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.Symlink(main, link))
	wt := filepath.Join(filepath.Dir(main), "wt")
	git := &fakeGit{t: t, answers: map[string]gitAnswer{
		"rev-parse --git-common-dir": {out: main + "/.git"},
		"rev-parse --git-dir":        {out: main + "/.git/worktrees/wt"},
		"worktree list --porcelain":  {out: porcelain(link, wt)},
	}}
	require.Equal(t, main, mainWorktreeFolder(git.run, worktreeTimeout, wt), "the symlink is resolved")

	failed := &fakeGit{t: t, answers: map[string]gitAnswer{
		"rev-parse --git-common-dir": {out: main + "/.git"},
		"rev-parse --git-dir":        {out: main + "/.git/worktrees/wt"},
		"worktree list --porcelain":  {err: exec.ErrNotFound},
	}}
	require.Equal(t, main, mainWorktreeFolder(failed.run, worktreeTimeout, wt), "a failed list falls back to the .git's parent")
}

// SESS-01, SESS-03: the detection runs the real git the CLI runs, with `-C` and the fixed arguments:
// a real repository and a real linked worktree give the main worktree and the main worktree itself
// gives nothing. It skips where git is not installed, like the harness CLI tests.
func TestMainWorktreeFolderWithRealGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	root := t.TempDir()
	main := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(main, 0o755))
	run(main, "-c", "init.defaultBranch=main", "init", "-q")
	run(main, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false",
		"commit", "-q", "--allow-empty", "-m", "one")
	wt := filepath.Join(root, "wt")
	run(main, "worktree", "add", "-q", wt)

	canonical, err := filepath.EvalSymlinks(main) // the temp dir itself may be reached through a symlink
	require.NoError(t, err)
	require.Equal(t, canonical, MainWorktreeFolder(wt))
	require.Empty(t, MainWorktreeFolder(canonical), "the main worktree is no linked worktree")
}
