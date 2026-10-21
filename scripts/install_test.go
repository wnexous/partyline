package scripts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/scripts"
)

// binary is the fake release binary: a shell script, so it runs on any test host.
const binary = "#!/bin/sh\necho partyline-fixture\n"

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// release is a fake GitHub: the releases API and the release downloads, named like
// GoReleaser's default archives (partyline_<version>_<os>_<arch>.tar.gz) and checksums.txt.
type release struct {
	srv      *httptest.Server
	mu       sync.Mutex
	files    map[string][]byte // path → body
	requests []string
}

func newRelease(t *testing.T, tag string, platforms ...string) *release {
	t.Helper()
	r := &release{files: map[string][]byte{}}
	version := strings.TrimPrefix(tag, "v")
	var sums strings.Builder
	for _, p := range platforms {
		asset := fmt.Sprintf("partyline_%s_%s.tar.gz", version, p)
		body := tarGz(t, map[string]string{"partyline": binary, "LICENSE": "license\n", "README.md": "readme\n"})
		sum := sha256.Sum256(body)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		r.files["/download/"+tag+"/"+asset] = body
	}
	r.files["/download/"+tag+"/checksums.txt"] = []byte(sums.String())
	r.files["/api/latest"] = []byte(fmt.Sprintf("{\n  \"url\": \"x\",\n  \"tag_name\": %q,\n  \"name\": \"%s\"\n}\n", tag, tag))
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, req.URL.Path)
		body, ok := r.files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// count is the number of requests the fake GitHub received.
func (r *release) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *release) requested(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.requests {
		if p == path {
			return true
		}
	}
	return false
}

// hostPlatform is the asset suffix install.sh picks on this machine.
func hostPlatform(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("install.sh targets Linux and macOS")
	}
	return runtime.GOOS + "_" + runtime.GOARCH
}

type result struct {
	code           int
	stdout, stderr string
}

// runInstall runs script with sh in a clean environment: HOME is a temp dir and PATH holds
// the system tools (plus extraPath first).
func runInstall(t *testing.T, script string, r *release, env map[string]string, extraPath ...string) result {
	t.Helper()
	path := filepath.Join(t.TempDir(), "install.sh")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o644))
	cmd := exec.Command("sh", path)
	cmd.Env = []string{
		"PATH=" + strings.Join(append(extraPath, "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"), ":"),
		"PARTYLINE_RELEASES_API=" + r.srv.URL + "/api/latest",
		"PARTYLINE_RELEASE_BASE=" + r.srv.URL + "/download",
	}
	if _, ok := env["HOME"]; !ok {
		cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func raw(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("install.sh")
	require.NoError(t, err)
	return string(data)
}

// ONB-01: the latest release binary is verified and installed to ~/.local/bin; a PATH hint
// is printed because that directory is not on PATH.
func TestInstallLatestToLocalBin(t *testing.T) {
	r := newRelease(t, "v1.4.0", hostPlatform(t))
	home := t.TempDir()
	res := runInstall(t, raw(t), r, map[string]string{"HOME": home})
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)

	bin := filepath.Join(home, ".local", "bin", "partyline")
	fi, err := os.Stat(bin)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
	out, err := exec.Command(bin).Output()
	require.NoError(t, err)
	require.Equal(t, "partyline-fixture\n", string(out))

	require.True(t, r.requested("/api/latest"), "the latest version comes from the releases API")
	require.True(t, r.requested("/download/v1.4.0/checksums.txt"))
	require.Contains(t, res.stdout, "Installed partyline v1.4.0 to "+bin)
	dir := filepath.Join(home, ".local", "bin")
	require.Contains(t, res.stdout, dir+" is not on your PATH")
	require.Contains(t, res.stdout, `export PATH="`+dir+`:$PATH"`)
}

// ONB-01: $PARTYLINE_INSTALL_DIR wins; no PATH hint when the directory is on PATH;
// $PARTYLINE_VERSION skips the latest-release lookup.
func TestInstallDirOnPathAndPinnedVersion(t *testing.T) {
	r := newRelease(t, "v1.3.2", hostPlatform(t))
	dir := filepath.Join(t.TempDir(), "my bin")
	res := runInstall(t, raw(t), r, map[string]string{"PARTYLINE_INSTALL_DIR": dir, "PARTYLINE_VERSION": "1.3.2"}, dir)
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	_, err := os.Stat(filepath.Join(dir, "partyline"))
	require.NoError(t, err)
	require.NotContains(t, res.stdout, "is not on your PATH")
	require.False(t, r.requested("/api/latest"))
}

// ONB-01: Linux and macOS on amd64 and arm64 map to the release asset names; other systems
// fail without installing.
func TestInstallPicksAssetPerPlatform(t *testing.T) {
	hostPlatform(t)
	cases := []struct{ s, m, platform string }{
		{"Linux", "x86_64", "linux_amd64"},
		{"Linux", "aarch64", "linux_arm64"},
		{"Darwin", "x86_64", "darwin_amd64"},
		{"Darwin", "arm64", "darwin_arm64"},
	}
	for _, c := range cases {
		t.Run(c.platform, func(t *testing.T) {
			r := newRelease(t, "v2.0.0", c.platform)
			fake := fakeUname(t, c.s, c.m)
			dir := t.TempDir()
			res := runInstall(t, raw(t), r, map[string]string{"PARTYLINE_INSTALL_DIR": dir}, fake)
			require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
			require.True(t, r.requested("/download/v2.0.0/partyline_2.0.0_"+c.platform+".tar.gz"))
			_, err := os.Stat(filepath.Join(dir, "partyline"))
			require.NoError(t, err)
		})
	}
	for _, c := range []struct{ s, m string }{{"FreeBSD", "x86_64"}, {"Linux", "i686"}} {
		t.Run(c.s+"_"+c.m, func(t *testing.T) {
			r := newRelease(t, "v2.0.0", "linux_amd64")
			dir := t.TempDir()
			res := runInstall(t, raw(t), r, map[string]string{"PARTYLINE_INSTALL_DIR": dir}, fakeUname(t, c.s, c.m))
			require.NotEqual(t, 0, res.code)
			require.Contains(t, res.stderr, "unsupported")
			_, err := os.Stat(filepath.Join(dir, "partyline"))
			require.True(t, os.IsNotExist(err))
		})
	}
}

// fakeUname returns a directory with a uname that reports system s and machine m.
func fakeUname(t *testing.T, s, m string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; *) echo %s ;; esac\n", s, m, s)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "uname"), []byte(script), 0o755))
	return dir
}

// ONB-02: a checksum mismatch exits non-zero and leaves no binary.
func TestInstallChecksumMismatchInstallsNothing(t *testing.T) {
	platform := hostPlatform(t)
	r := newRelease(t, "v1.4.0", platform)
	asset := "/download/v1.4.0/partyline_1.4.0_" + platform + ".tar.gz"
	r.files[asset] = tarGz(t, map[string]string{"partyline": "#!/bin/sh\necho tampered\n"})

	dir := t.TempDir()
	res := runInstall(t, raw(t), r, map[string]string{"PARTYLINE_INSTALL_DIR": dir})
	require.NotEqual(t, 0, res.code)
	require.Contains(t, res.stderr, "checksum mismatch")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "no binary and no temporary file")
}

// ONB-02: an asset missing from checksums.txt is refused the same way.
func TestInstallUnlistedAssetInstallsNothing(t *testing.T) {
	platform := hostPlatform(t)
	r := newRelease(t, "v1.4.0", platform)
	r.files["/download/v1.4.0/checksums.txt"] = []byte("0000  partyline_1.4.0_other_arch.tar.gz\n")
	dir := t.TempDir()
	res := runInstall(t, raw(t), r, map[string]string{"PARTYLINE_INSTALL_DIR": dir})
	require.NotEqual(t, 0, res.code)
	require.Contains(t, res.stderr, "not listed in checksums.txt")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// ONB-03: the served script carries the instance URL; running it saves the URL as the
// default for setup in a private config file (0600 in a 0700 directory), never replacing
// an existing config file.
func TestServedInstallSavesInstanceURL(t *testing.T) {
	r := newRelease(t, "v1.4.0", hostPlatform(t))
	served := scripts.Install("https://partyline.example.com")
	require.Contains(t, served, "DEFAULT_URL='https://partyline.example.com'")

	home := t.TempDir()
	res := runInstall(t, served, r, map[string]string{"HOME": home})
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Contains(t, res.stdout, "Next: partyline setup --browser")
	cfg := filepath.Join(home, ".config", "partyline", "config.json")
	data, err := os.ReadFile(cfg)
	require.NoError(t, err)
	require.JSONEq(t, `{"url":"https://partyline.example.com","token":""}`, string(data))
	fi, err := os.Stat(cfg)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(filepath.Dir(cfg))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm())

	// A second install keeps the existing file (it may hold a token).
	require.NoError(t, os.WriteFile(cfg, []byte(`{"url":"https://other.example.com","token":"pl_agent_kept"}`), 0o600))
	res = runInstall(t, scripts.Install("https://partyline.example.com"), r, map[string]string{"HOME": home})
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	data, err = os.ReadFile(cfg)
	require.NoError(t, err)
	require.JSONEq(t, `{"url":"https://other.example.com","token":"pl_agent_kept"}`, string(data))
}

// The raw script (not served by an instance) writes no config and asks for --url.
func TestRawInstallHasNoDefaultURL(t *testing.T) {
	r := newRelease(t, "v1.4.0", hostPlatform(t))
	home := t.TempDir()
	res := runInstall(t, raw(t), r, map[string]string{"HOME": home})
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Contains(t, res.stdout, "Next: partyline setup --browser --url https://<your instance>")
	_, err := os.Stat(filepath.Join(home, ".config", "partyline", "config.json"))
	require.True(t, os.IsNotExist(err))
}

// ONB-03: a URL with a single quote stays one shell word.
func TestInstallQuotesURL(t *testing.T) {
	require.Contains(t, scripts.Install("https://x.example.com/a'b"), `DEFAULT_URL='https://x.example.com/a'\''b'`)
}

// homeFiles lists every file under home (relative paths).
func homeFiles(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(home, path)
			out = append(out, rel)
		}
		return err
	}))
	return out
}

// ONB-10: `curl … | sh` may run a script cut short by a dropped connection. Cut at any line
// before its last, the served script downloads nothing and writes nothing.
func TestInstallTruncatedRunsNothing(t *testing.T) {
	r := newRelease(t, "v1.4.0", hostPlatform(t))
	served := scripts.Install("https://partyline.example.com")
	lines := strings.SplitAfter(served, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for cut := 1; cut < len(lines); cut++ {
		home := t.TempDir()
		res := runInstall(t, strings.Join(lines[:cut], ""), r, map[string]string{"HOME": home})
		require.Empty(t, homeFiles(t, home), "cut after line %d wrote files (exit %d)", cut, res.code)
		require.NotContains(t, res.stdout, "Downloading", "cut after line %d", cut)
	}
	require.Zero(t, r.count(), "a truncated script made requests")

	// The whole script still installs.
	home := t.TempDir()
	res := runInstall(t, served, r, map[string]string{"HOME": home})
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.FileExists(t, filepath.Join(home, ".local", "bin", "partyline"))
}

// ONB-10: plain http is refused for the instance and the release URLs, before anything is
// downloaded, except for loopback hosts (localhost, 127.0.0.1, ::1). The scheme is compared
// without case, a URL without a scheme (curl would use http) is refused, and so is any URL
// with user info, which would let http://localhost:80@evil.com reach evil.com.
func TestInstallRefusesPlainHTTP(t *testing.T) {
	r := newRelease(t, "v1.4.0", hostPlatform(t))
	httpMsg := func(u string) string { return "partyline install: refusing plain http URL " + u + ": use https" }
	userMsg := func(u string) string {
		return "partyline install: refusing URL " + u + ": user info (@) is not allowed"
	}
	for _, tc := range []struct {
		name, script string
		env          map[string]string
		msg          string
	}{
		{name: "served from http", script: scripts.Install("http://partyline.example.com"), msg: httpMsg("http://partyline.example.com")},
		{name: "PARTYLINE_URL", script: raw(t), env: map[string]string{"PARTYLINE_URL": "http://10.0.0.5:8080"}, msg: httpMsg("http://10.0.0.5:8080")},
		{name: "release base", script: raw(t), env: map[string]string{"PARTYLINE_RELEASE_BASE": "http://mirror.example.com/download"}, msg: httpMsg("http://mirror.example.com/download")},
		{name: "releases API", script: raw(t), env: map[string]string{"PARTYLINE_RELEASES_API": "http://api.example.com/latest"}, msg: httpMsg("http://api.example.com/latest")},
		{name: "localhost lookalike", script: scripts.Install("http://localhost.example.com"), msg: httpMsg("http://localhost.example.com")},
		{name: "loopback lookalike", script: raw(t), env: map[string]string{"PARTYLINE_URL": "http://127.0.0.1.nip.io:8080"}, msg: httpMsg("http://127.0.0.1.nip.io:8080")},
		{name: "mixed-case scheme", script: scripts.Install("Http://partyline.example.com"), msg: httpMsg("Http://partyline.example.com")},
		{name: "mixed-case scheme in env", script: raw(t), env: map[string]string{"PARTYLINE_URL": "hTTp://10.0.0.5"}, msg: httpMsg("hTTp://10.0.0.5")},
		{name: "upper-case release base", script: raw(t), env: map[string]string{"PARTYLINE_RELEASE_BASE": "HTTP://mirror.example.com/download"}, msg: httpMsg("HTTP://mirror.example.com/download")},
		{name: "localhost user info", script: scripts.Install("http://localhost:80@evil.com/"), msg: userMsg("http://localhost:80@evil.com/")},
		{name: "127.0.0.1 user info", script: raw(t), env: map[string]string{"PARTYLINE_URL": "http://127.0.0.1:1@evil.com"}, msg: userMsg("http://127.0.0.1:1@evil.com")},
		{name: "::1 user info", script: raw(t), env: map[string]string{"PARTYLINE_RELEASE_BASE": "http://[::1]:8080@evil.com/download"}, msg: userMsg("http://[::1]:8080@evil.com/download")},
		{name: "https user info", script: raw(t), env: map[string]string{"PARTYLINE_URL": "https://me:pw@partyline.example.com"}, msg: userMsg("https://me:pw@partyline.example.com")},
		{name: "no scheme", script: raw(t), env: map[string]string{"PARTYLINE_URL": "partyline.example.com"}, msg: "partyline install: refusing URL partyline.example.com: use https"},
		{name: "non-numeric loopback port", script: raw(t), env: map[string]string{"PARTYLINE_URL": "http://localhost:8a80"}, msg: httpMsg("http://localhost:8a80")},
		{name: "other scheme on loopback", script: raw(t), env: map[string]string{"PARTYLINE_URL": "ftp://localhost/x"}, msg: "partyline install: refusing URL ftp://localhost/x: use https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			env := map[string]string{"HOME": home}
			for k, v := range tc.env {
				env[k] = v
			}
			before := r.count()
			res := runInstall(t, tc.script, r, env)
			require.Equal(t, 1, res.code, "stdout: %s", res.stdout)
			require.Contains(t, res.stderr, tc.msg)
			require.Empty(t, homeFiles(t, home))
			require.Equal(t, before, r.count(), "nothing downloaded")
		})
	}

	for _, u := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "HTTP://LocalHost:8080/", "http://localhost", "https://partyline.example.com"} {
		home := t.TempDir()
		res := runInstall(t, scripts.Install(u), r, map[string]string{"HOME": home})
		require.Equal(t, 0, res.code, "%s: %s", u, res.stderr)
		data, err := os.ReadFile(filepath.Join(home, ".config", "partyline", "config.json"))
		require.NoError(t, err)
		require.JSONEq(t, `{"url":"`+u+`","token":""}`, string(data))
	}
}
