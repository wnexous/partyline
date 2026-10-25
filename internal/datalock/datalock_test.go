package datalock

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// helperDir, in the environment, turns the test binary into a process that takes the lock of that
// directory (see helper), so that the tests meet a real second process.
const helperDir = "PARTYLINE_DATALOCK_HELPER_DIR"

// Exit codes of the helper process.
const (
	exitLocked = 3 // the directory was in use
	exitFailed = 4 // anything else
)

func TestMain(m *testing.M) {
	if dir := os.Getenv(helperDir); dir != "" {
		os.Exit(helper(dir))
	}
	os.Exit(m.Run())
}

// helper takes the lock of dir. When it gets it, it says "locked" and holds it until its stdin closes
// or it is killed; otherwise it prints why and exits with a code that tells the reason.
func helper(dir string) int {
	l, err := Acquire(dir)
	if err != nil {
		fmt.Println(err)
		if errors.Is(err, ErrLocked) {
			return exitLocked
		}
		return exitFailed
	}
	defer func() { _ = l.Release() }()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
	return 0
}

// waitFor bounds how long a test waits for another process.
const waitFor = 2 * time.Minute

// startHolder starts a process that holds dir and returns it once it does. It is killed when the test ends.
func startHolder(t *testing.T, dir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperDir+"="+dir)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		s.Scan()
		line <- s.Text()
	}()
	select {
	case got := <-line:
		require.Equal(t, "locked", got, "the helper process took the directory")
	case <-time.After(waitFor):
		t.Fatal("the helper process did not take the directory")
	}
	return cmd
}

// acquireNow calls Acquire and fails the test, instead of hanging, when it waits for the lock.
func acquireNow(t *testing.T, dir string) (*Lock, error) {
	t.Helper()
	type result struct {
		l   *Lock
		err error
	}
	got := make(chan result, 1)
	go func() {
		l, err := Acquire(dir)
		got <- result{l, err}
	}()
	select {
	case r := <-got:
		return r.l, r.err
	case <-time.After(waitFor):
		t.Fatal("Acquire waited for the lock")
		return nil, nil
	}
}

// OPS-05: a process that starts on a free data directory takes it, creating the directory and the
// lock file, which tells who holds the directory.
func TestAcquireTakesAFreeDataDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "yet", "there")

	l, err := acquireNow(t, dir)

	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Release() })
	path := filepath.Join(dir, FileName)
	require.FileExists(t, path)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(data), fmt.Sprintf("%d ", os.Getpid())), "the file names the holder: %q", data)
	}
}

// OPS-05: a second process on a data directory that is taken fails at once, with an error that names
// the directory, who holds it and the way out; it does not wait for the first one.
func TestASecondAcquireOfATakenDataDirectoryFailsAtOnce(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireNow(t, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Release() })

	second, err := acquireNow(t, dir)

	require.Nil(t, second)
	require.ErrorIs(t, err, ErrLocked)
	var locked *LockedError
	require.ErrorAs(t, err, &locked)
	require.Equal(t, dir, locked.Dir)
	require.Equal(t, filepath.Join(dir, FileName), locked.Path)
	require.Contains(t, err.Error(), dir)
	require.Contains(t, err.Error(), "already in use by another partyline process")
	require.Contains(t, err.Error(), "stop it first")
	if runtime.GOOS != "windows" { // there the holder's byte is locked, so its note cannot be read
		require.Equal(t, fmt.Sprintf("pid %d on %s", os.Getpid(), hostname(t)), locked.Holder)
	}
}

// hostname is the name Acquire writes, as safeHost lets it through.
func hostname(t *testing.T) string {
	t.Helper()
	host, err := os.Hostname()
	require.NoError(t, err)
	return safeHost(host)
}

// OPS-05: releasing the directory lets the next process take it, however often Release is called.
func TestReleaseGivesTheDataDirectoryUp(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireNow(t, dir)
	require.NoError(t, err)

	require.NoError(t, first.Release())
	require.NoError(t, first.Release(), "a second Release does nothing")
	require.NoError(t, (*Lock)(nil).Release())

	second, err := acquireNow(t, dir)
	require.NoError(t, err, "the directory is free again")
	require.NoError(t, second.Release())
}

// OPS-05: the lock is per data directory.
func TestOtherDataDirectoriesAreNotAffected(t *testing.T) {
	a, err := acquireNow(t, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Release() })

	b, err := acquireNow(t, t.TempDir())

	require.NoError(t, err)
	require.NoError(t, b.Release())
}

// OPS-05: a lock file left by a process that is gone holds nothing: only a process that is running
// holds the directory, so nobody has to remove a stale file by hand.
func TestALockFileOfAGoneProcessIsNoLock(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, FileName), []byte("999999 ghost\n"), 0o600))

	l, err := acquireNow(t, dir)

	require.NoError(t, err)
	require.NoError(t, l.Release())
}

// OPS-05: another process, not just another file of this one, keeps the directory: it is told who
// holds it, and once that process is gone, even killed, the directory is free.
func TestAnotherProcessHoldsTheDataDirectoryUntilItDies(t *testing.T) {
	dir := t.TempDir()
	holder := startHolder(t, dir)

	_, err := acquireNow(t, dir)

	require.ErrorIs(t, err, ErrLocked)
	if runtime.GOOS != "windows" {
		require.Contains(t, err.Error(), fmt.Sprintf("pid %d", holder.Process.Pid))
	}

	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait()
	l, err := acquireNow(t, dir)
	require.NoError(t, err, "a process that died leaves nothing stale")
	require.NoError(t, l.Release())
}

// OPS-05: a process that starts while this one holds the directory is refused at once and says why.
func TestAProcessThatStartsOnATakenDataDirectoryIsRefused(t *testing.T) {
	dir := t.TempDir()
	l, err := acquireNow(t, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Release() })

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperDir+"="+dir)
	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		out, runErr = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitFor):
		_ = cmd.Process.Kill()
		t.Fatal("the second process waited for the directory")
	}

	var exit *exec.ExitError
	require.ErrorAs(t, runErr, &exit)
	require.Equal(t, exitLocked, exit.ExitCode(), "output: %s", out)
	require.Contains(t, string(out), "already in use by another partyline process")
	require.Contains(t, string(out), dir)
}

// The holder is read from the lock file as the holder wrote it, and only when it looks like a pid and
// a host name: a lock file is a file in the data directory, and its text ends up in logs.
func TestReadHolder(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"pid and host":            {"42 4f2a9c1b7d3e\n", "pid 42 on 4f2a9c1b7d3e"},
		"pid alone":               {"42\n", "pid 42"},
		"fqdn":                    {"7 box-1.example.test\n", "pid 7 on box-1.example.test"},
		"empty":                   {"", ""},
		"not a pid":               {"abc host\n", ""},
		"zero":                    {"0 host\n", ""},
		"negative":                {"-3 host\n", ""},
		"odd host":                {"42 ho$t\n", "pid 42"},
		"control characters":      {"42 ho\x1b[31mst\n", "pid 42"},
		"long host":               {"42 " + strings.Repeat("a", 300) + "\n", "pid 42"},
		"extra words are ignored": {"42 host\nignored text\n", "pid 42 on host"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			f, err := os.Open(path)
			require.NoError(t, err)
			defer func() { _ = f.Close() }()

			require.Equal(t, tc.want, readHolder(f))
		})
	}
}
