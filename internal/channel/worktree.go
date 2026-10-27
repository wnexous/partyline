package channel

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// worktreeTimeout bounds the whole detection of the main worktree (SESS-02): git has one second,
// and a slow or missing git only means the session resolves by its own folder.
const worktreeTimeout = time.Second

// gitRunner runs one git command in dir and returns its standard output; ctx bounds it.
type gitRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// MainWorktreeFolder returns the canonical folder of the main worktree when dir is a session in a
// linked git worktree, and "" when it is not one (the main worktree itself, a submodule, a bare
// repository), when git is missing or fails, or when it takes longer than worktreeTimeout
// (SESS-02, SESS-03). The session then reports no main folder and keeps resolving by its own
// folder. Nothing is logged with the path: a session whose folder cannot be folded starts anyway.
func MainWorktreeFolder(dir string) string { return mainWorktreeFolder(runGit, worktreeTimeout, dir) }

// mainWorktreeFolder is MainWorktreeFolder with the runner and the bound a test passes.
func mainWorktreeFolder(run gitRunner, bound time.Duration, dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	common, ok := gitPath(ctx, run, dir, "--git-common-dir")
	if !ok {
		return ""
	}
	gitDir, ok := gitPath(ctx, run, dir, "--git-dir")
	if !ok {
		return ""
	}
	if common == gitDir {
		return "" // the main worktree or a submodule: one git dir, so no linked worktree
	}
	var main string
	switch folder, bare, listed := listedWorktree(ctx, run, dir); {
	case bare:
		return "" // a bare repository has no working tree to be the identity
	case listed:
		main = folder
	default:
		// The list failed: the parent of the common dir is the main worktree only when it is a .git.
		if filepath.Base(common) != ".git" {
			return ""
		}
		main = filepath.Dir(common)
	}
	folder, err := CanonicalFolder(main)
	if err != nil {
		return ""
	}
	return folder
}

// gitPath is one git rev-parse output made absolute against dir, because git prints the common
// dir relative to the worktree in some layouts. ok is false when git failed or printed nothing.
func gitPath(ctx context.Context, run gitRunner, dir, option string) (string, bool) {
	out, err := run(ctx, dir, "rev-parse", option)
	if err != nil {
		return "", false
	}
	return absoluteAgainst(dir, string(out))
}

// absoluteAgainst makes p (a git output, possibly relative) an absolute clean path against dir.
func absoluteAgainst(dir, p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

// listedWorktree is the first entry of `git worktree list --porcelain`: the main worktree's folder,
// or bare for a bare repository. ok is false when the command failed or printed no entry.
func listedWorktree(ctx context.Context, run gitRunner, dir string) (folder string, bare, ok bool) {
	out, err := run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, false
	}
	first, _, _ := strings.Cut(string(out), "\n\n")
	for _, line := range strings.Split(first, "\n") {
		switch {
		case line == "bare":
			return "", true, false
		case strings.HasPrefix(line, "worktree "):
			folder = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		}
	}
	return folder, false, folder != ""
}

// runGit runs `git -C dir args…` and returns its standard output. The context kills a git that
// outlives the bound, and nothing of the command is logged.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- git with the fixed arguments of the detection, in the session's own folder
	cmd.Dir = dir
	return cmd.Output()
}
