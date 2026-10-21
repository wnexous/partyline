package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// versionOf runs scripts/version.sh in dir with only the given variables (and PATH, HOME) set,
// the way the Dockerfile's build stage runs it, and returns its output.
func versionOf(t *testing.T, dir string, vars ...string) string {
	t.Helper()
	script, err := filepath.Abs("version.sh")
	require.NoError(t, err)
	cmd := exec.Command("sh", script)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, vars...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return string(out)
}

// git runs git in dir with a fixed identity and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// repo creates a git repository with one commit and returns its directory.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644))
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
}

func skipWithoutShellAndGit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// ONB-11: a VERSION build argument wins over git and SOURCE_COMMIT; "dev" counts as unset.
func TestVersionArgumentWins(t *testing.T) {
	skipWithoutShellAndGit(t)
	dir := repo(t)
	git(t, dir, "tag", "v1.2.3")
	require.Equal(t, "v9.9.9\n", versionOf(t, dir, "VERSION=v9.9.9", "SOURCE_COMMIT=0123456789abcdef"))
	require.Equal(t, "v1.2.3\n", versionOf(t, dir, "VERSION=dev"))
}

// ONB-11: without VERSION the build context's .git gives `git describe --tags`: the tag on
// the commit, or tag-<commits since>-g<hash> after it.
func TestVersionFromGitTags(t *testing.T) {
	skipWithoutShellAndGit(t)
	dir := repo(t)
	git(t, dir, "tag", "v1.2.3")
	require.Equal(t, "v1.2.3\n", versionOf(t, dir))

	commit(t, dir, "second")
	short := git(t, dir, "rev-parse", "--short", "HEAD")
	require.Equal(t, "v1.2.3-1-g"+short+"\n", versionOf(t, dir, "SOURCE_COMMIT=0123456789abcdef"))
}

// ONB-11: without a reachable tag (an untagged repository, or a shallow clone that misses
// the tagged commit, as PaaS builders make) the version is dev-<12 characters of HEAD>.
func TestVersionFromGitWithoutTag(t *testing.T) {
	skipWithoutShellAndGit(t)
	dir := repo(t)
	head := git(t, dir, "rev-parse", "HEAD")
	require.Equal(t, "dev-"+head[:12]+"\n", versionOf(t, dir, "SOURCE_COMMIT=0123456789abcdef"))

	git(t, dir, "tag", "v1.2.3")
	commit(t, dir, "second")
	shallow := filepath.Join(t.TempDir(), "clone")
	git(t, dir, "clone", "-q", "--depth", "1", "--no-tags", "file://"+dir, shallow)
	head = git(t, shallow, "rev-parse", "HEAD")
	require.Equal(t, "dev-"+head[:12]+"\n", versionOf(t, shallow))
}

// ONB-11: without .git in the context SOURCE_COMMIT gives dev-<12 characters>; with nothing
// at all the version is dev.
func TestVersionWithoutGit(t *testing.T) {
	skipWithoutShellAndGit(t)
	dir := t.TempDir()
	require.Equal(t, "dev-0123456789ab\n", versionOf(t, dir, "SOURCE_COMMIT=0123456789abcdef"))
	require.Equal(t, "dev\n", versionOf(t, dir))
	require.Equal(t, "dev\n", versionOf(t, dir, "VERSION=dev", "SOURCE_COMMIT="))
}

// ONB-11: the image build runs version.sh in the build context, stamps its output, and the
// context keeps .git (no .dockerignore line drops it).
func TestDockerBuildUsesVersionScript(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "Dockerfile"))
	require.NoError(t, err)
	require.Contains(t, string(dockerfile), `RUN version="$(sh scripts/version.sh)"`)
	require.Contains(t, string(dockerfile), `-X github.com/wnexous/partyline/internal/cli.Version=${version}`)
	require.Contains(t, string(dockerfile), "ARG VERSION=\"\"\nARG SOURCE_COMMIT=\"\"\n")

	ignore, err := os.ReadFile(filepath.Join("..", ".dockerignore"))
	require.NoError(t, err)
	for _, line := range strings.Split(string(ignore), "\n") {
		line = strings.TrimSpace(line)
		require.NotContains(t, []string{".git", ".git/", "/.git", ".git*", "**/.git"}, line, ".dockerignore must keep .git in the build context")
	}
}
