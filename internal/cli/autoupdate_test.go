package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// healthServer answers /healthz with version, counting the calls.
func healthServer(t *testing.T, version string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		fmt.Fprintf(w, `{"status":"ok","version":%q}`, version)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// autoUpdateMachine is an installed client of version running against a server of serverVersion, with
// the release rel to download from.
func autoUpdateMachine(t *testing.T, rel *fakeRelease, version, serverVersion string) (*updateMachine, *autoUpdate, *bytes.Buffer) {
	t.Helper()
	m := newUpdateMachine(t, rel, version)
	health, _ := healthServer(t, serverVersion)
	var errOut bytes.Buffer
	au, note := autoUpdateFor(stdio{
		stderr: &errOut, getenv: func(k string) string { return m.env[k] },
		executable: func() (string, error) { return m.exe, nil },
		updateHTTP: m.client, goos: m.goos, goarch: m.goarch,
	}, health.URL+"/")
	require.Empty(t, note)
	require.NotNil(t, au)
	au.lockPath = filepath.Join(t.TempDir(), "update.lock")
	au.diskVersion = func(context.Context, string) (string, error) { return version, nil }
	return m, au, &errOut
}

// AU-01: a client older than the server installs the server's release over its executable.
func TestAutoUpdateInstallsTheServersRelease(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m, au, errOut := autoUpdateMachine(t, rel, "v0.4.0", latestTag)

	newer, stop := au.check(context.Background())
	require.Equal(t, latestTag, newer)
	require.False(t, stop)
	m.requireUpdated(releasedBinary(latestTag, linuxAmd))
	require.Equal(t, "partyline: updated v0.4.0 → v9.9.9\n", errOut.String())
}

// AU-02: a server that is not newer, or not a release, changes nothing and downloads nothing.
func TestAutoUpdateFollowsOnlyANewerReleaseOfTheServer(t *testing.T) {
	for _, server := range []string{"v0.4.0", "v0.3.9", "dev", "v9.9.9-3-gabc1234", ""} {
		t.Run(server, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			m, au, _ := autoUpdateMachine(t, rel, "v0.4.0", server)
			before := m.state()

			newer, stop := au.check(context.Background())
			require.Empty(t, newer)
			require.False(t, stop)
			require.Zero(t, rel.requests())
			m.requireUntouched(before)
		})
	}
}

// AU-02: a client that is not a release (a local build, one from git) never updates itself.
func TestAutoUpdateIsOffForABuildThatIsNotARelease(t *testing.T) {
	for _, v := range []string{"dev", "v0.4.0-3-gabc1234", "v0.4.0-dirty"} {
		withVersion(t, v)
		withBuildInfo(t, "", false)
		au, note := autoUpdateFor(stdio{getenv: func(string) string { return "" }, goos: "linux", goarch: "amd64"}, "https://x")
		require.Nil(t, au, v)
		require.Empty(t, note, v)
	}
}

// AU-03: an executable that another session (or the person) already brought to the server's version is
// not downloaded again: the session only starts again on it.
func TestAutoUpdateUsesTheExecutableAnotherSessionInstalled(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m, au, _ := autoUpdateMachine(t, rel, "v0.4.0", latestTag)
	au.diskVersion = func(context.Context, string) (string, error) { return latestTag, nil }
	before := m.state()

	newer, stop := au.check(context.Background())
	require.Equal(t, latestTag, newer)
	require.False(t, stop)
	require.Zero(t, rel.requests())
	m.requireUntouched(before)
}

// AU-02: a local build someone put in the executable's place is left alone, and the checks end.
func TestAutoUpdateLeavesALocalBuildOnDiskAlone(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m, au, _ := autoUpdateMachine(t, rel, "v0.4.0", latestTag)
	au.diskVersion = func(context.Context, string) (string, error) { return "dev", nil }
	before := m.state()

	newer, stop := au.check(context.Background())
	require.Empty(t, newer)
	require.True(t, stop)
	require.Zero(t, rel.requests())
	m.requireUntouched(before)
}

// AU-08: an executable in a folder that takes no new file keeps running as it is, and says so once.
func TestAutoUpdateStopsWhenTheFolderIsNotWritable(t *testing.T) {
	needUnixPerms(t)
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m, au, errOut := autoUpdateMachine(t, rel, "v0.4.0", latestTag)
	before := m.state()
	require.NoError(t, os.Chmod(m.dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(m.dir, 0o755) })

	newer, stop := au.check(context.Background())
	require.Empty(t, newer)
	require.True(t, stop)
	require.Contains(t, errOut.String(), "automatic updates are off: "+m.dir+" is not writable")
	m.requireUntouched(before)
}

// AU-08: PARTYLINE_AUTO_UPDATE=0 turns it off without a word; Windows says why it is off.
func TestAutoUpdateOptOutAndWindows(t *testing.T) {
	withVersion(t, "v0.4.0")
	withBuildInfo(t, "", false)
	off, note := autoUpdateFor(stdio{getenv: func(k string) string {
		if k == autoUpdateVar {
			return "0"
		}
		return ""
	}, goos: "linux", goarch: "amd64"}, "https://x")
	require.Nil(t, off)
	require.Empty(t, note)

	win, note := autoUpdateFor(stdio{getenv: func(string) string { return "" }, goos: "windows", goarch: "amd64"}, "https://x")
	require.Nil(t, win)
	require.Contains(t, note, "automatic updates are off on Windows")
}

// AU-01: run checks after the first wait and then on every interval, until the server has a newer release.
func TestAutoUpdateRunChecksUntilTheServerIsNewer(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	_, au, _ := autoUpdateMachine(t, rel, "v0.4.0", "v0.4.0")
	var version atomic.Value
	version.Store("v0.4.0")
	var calls atomic.Int32
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 3 {
			version.Store(latestTag)
		}
		fmt.Fprintf(w, `{"version":%q}`, version.Load())
	}))
	t.Cleanup(health.Close)
	au.instance = health.URL
	au.first, au.every = time.Millisecond, time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.Equal(t, latestTag, au.run(ctx))
	require.Equal(t, int32(3), calls.Load(), "two checks found the same version, the third a newer one")
}

// AU-01: run ends with nothing when the session ends.
func TestAutoUpdateRunEndsWithTheSession(t *testing.T) {
	_, au, _ := autoUpdateMachine(t, nil, "v0.4.0", "v0.4.0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Empty(t, au.run(ctx))
}

func TestSupervisedParent(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == supervisedVar {
				return v
			}
			return ""
		}
	}
	require.Equal(t, 4321, supervisedParent(env("4321")))
	for _, v := range []string{"", "0", "-1", "abc"} {
		require.Zero(t, supervisedParent(env(v)), v)
	}
}

// AU-06, AU-07: listen becomes the updated program as the same session: no takeover, no principal role.
func TestListenRestartsAsTheSameSession(t *testing.T) {
	var gotPath string
	var gotArgv, gotEnv []string
	var errOut bytes.Buffer
	code := restartListen(stdio{
		stderr:  &errOut,
		environ: func() []string { return []string{"PARTYLINE_TAKEOVER=1", "PARTYLINE_LIVE=1:abcdefghijklmnop"} },
		exec: func(path string, argv, env []string) (int, error) {
			gotPath, gotArgv, gotEnv = path, argv, env
			return 0, nil
		},
	}, "/bin/partyline", []string{"--as", "x"}, "v9.9.9")
	require.Equal(t, 0, code)
	require.Equal(t, "/bin/partyline", gotPath)
	require.Equal(t, []string{"/bin/partyline", "listen", "--as", "x"}, gotArgv)
	// The later values win: the session is resumed and asks for nothing again; its live id is kept.
	require.Equal(t, []string{"PARTYLINE_TAKEOVER=1", "PARTYLINE_LIVE=1:abcdefghijklmnop", resumedVar + "=1", "PARTYLINE_TAKEOVER=0", principalEnvVar + "=0"}, gotEnv)
	require.Equal(t, "partyline: listen starts again on v9.9.9\n", errOut.String())
}

// AU-11: doctor says whether this machine's sessions follow the server, and warns only when they cannot
// though nobody turned that off; a build that is not a release gets no line.
func TestDoctorReportsAutoUpdate(t *testing.T) {
	exeIn := func(dir string) func() (string, error) {
		exe := filepath.Join(dir, "partyline")
		require.NoError(t, os.WriteFile(exe, []byte("bin"), 0o755))
		return func() (string, error) { return exe, nil }
	}
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

	withVersion(t, "dev")
	withBuildInfo(t, "", false)
	_, shown := checkAutoUpdate(stdio{getenv: env(nil), goos: "linux", goarch: "amd64", executable: exeIn(t.TempDir())})
	require.False(t, shown, "a local build never updates itself")

	withVersion(t, "v0.4.0")
	c, shown := checkAutoUpdate(stdio{getenv: env(nil), goos: "linux", goarch: "amd64", executable: exeIn(t.TempDir())})
	require.True(t, shown)
	require.Equal(t, check{name: "auto-update", ok: true, detail: "on: sessions follow the server's version"}, c)

	c, _ = checkAutoUpdate(stdio{getenv: env(map[string]string{autoUpdateVar: "0"}), goos: "linux", goarch: "amd64", executable: exeIn(t.TempDir())})
	require.True(t, c.ok)
	require.False(t, c.warn, "turned off on purpose")
	require.Contains(t, c.detail, "off (PARTYLINE_AUTO_UPDATE=0)")

	c, _ = checkAutoUpdate(stdio{getenv: env(nil), goos: "windows", goarch: "amd64", executable: exeIn(t.TempDir())})
	require.True(t, c.warn)
	require.Contains(t, c.detail, "automatic updates are off on Windows")

	needUnixPerms(t)
	dir := t.TempDir()
	exe := exeIn(dir)
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	c, _ = checkAutoUpdate(stdio{getenv: env(nil), goos: "linux", goarch: "amd64", executable: exe})
	require.True(t, c.warn)
	require.Equal(t, dir+" is not writable, so sessions cannot update themselves", c.detail)
	require.Contains(t, c.fix, "install.sh")
}
