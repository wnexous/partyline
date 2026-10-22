package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// installedBinary is what the machine under test runs before an update: a stand-in for the
// partyline executable that no test ever starts.
const installedBinary = "#!/bin/sh\necho installed partyline\n"

// releasedBinary is the partyline file of a release archive. It differs per tag and platform,
// so a test can tell which archive an update used.
func releasedBinary(tag, platform string) string {
	return "#!/bin/sh\necho partyline " + tag + " " + platform + "\n"
}

// tarEntry is one file of a fake release archive; typ 0 is a regular file.
type tarEntry struct {
	name, body, link string
	typ              byte
}

// releaseArchive builds a .tar.gz the way GoReleaser does for a release: the binary at the top
// level next to the licence and the READMEs.
func releaseArchive(t *testing.T, tag, platform string) []byte {
	t.Helper()
	return gzTar(t,
		tarEntry{name: "partyline", body: releasedBinary(tag, platform)},
		tarEntry{name: "LICENSE", body: "license\n"},
		tarEntry{name: "README.md", body: "readme\n"})
}

func gzTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o755, Linkname: e.link}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if typ == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// downloadPath is where the fake GitHub serves a release file.
func downloadPath(tag, name string) string { return "/download/" + tag + "/" + name }

// assetName is the archive GoReleaser publishes for a tag and platform ("linux_amd64").
func assetName(tag, platform string) string {
	return "partyline_" + strings.TrimPrefix(tag, "v") + "_" + platform + ".tar.gz"
}

// fakeRelease is a fake GitHub over HTTPS: the latest-release API, a checksums.txt and one
// archive per platform, named like GoReleaser's default release. Nothing leaves the machine.
type fakeRelease struct {
	t   *testing.T
	srv *httptest.Server
	tag string

	mu     sync.Mutex
	files  map[string][]byte           // path → body
	custom map[string]http.HandlerFunc // path → handler, wins over files
	paths  []string                    // every requested path, in order
}

func newFakeRelease(t *testing.T, tag string, platforms ...string) *fakeRelease {
	t.Helper()
	r := &fakeRelease{t: t, tag: tag, files: map[string][]byte{}, custom: map[string]http.HandlerFunc{}}
	var sums strings.Builder
	for _, p := range platforms {
		body := releaseArchive(t, tag, p)
		fmt.Fprintf(&sums, "%s  %s\n", sha256Hex(body), assetName(tag, p))
		r.files[downloadPath(tag, assetName(tag, p))] = body
	}
	r.files[downloadPath(tag, "checksums.txt")] = []byte(sums.String())
	r.files["/api/latest"] = []byte(fmt.Sprintf("{\n  \"url\": \"x\",\n  \"tag_name\": %q,\n  \"name\": %q\n}\n", tag, tag))
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRelease) handle(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.paths = append(r.paths, req.URL.Path)
	h, hasHandler := r.custom[req.URL.Path]
	body, ok := r.files[req.URL.Path]
	r.mu.Unlock()
	switch {
	case hasHandler:
		h(w, req)
	case ok:
		_, _ = w.Write(body)
	default:
		http.NotFound(w, req)
	}
}

// set replaces the body served at path.
func (r *fakeRelease) set(path string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[path] = body
}

func (r *fakeRelease) drop(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.files, path)
}

// answer makes the server answer path with h instead of serving its file.
func (r *fakeRelease) answer(path string, h http.HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.custom[path] = h
}

func (r *fakeRelease) body(path string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.files[path]
}

func (r *fakeRelease) requested(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.paths {
		if p == path {
			return true
		}
	}
	return false
}

// all is every path requested so far, in order.
func (r *fakeRelease) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func (r *fakeRelease) requests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths)
}

// archivePath and sumsPath are the files an update of tag for platform downloads.
func (r *fakeRelease) archivePath(platform string) string {
	return downloadPath(r.tag, assetName(r.tag, platform))
}

func (r *fakeRelease) sumsPath() string { return downloadPath(r.tag, "checksums.txt") }

// env points the update command at this server, the way PARTYLINE_RELEASES_API and
// PARTYLINE_RELEASE_BASE point install.sh at a mirror.
func (r *fakeRelease) env() map[string]string {
	return map[string]string{
		"PARTYLINE_RELEASES_API": r.srv.URL + "/api/latest",
		"PARTYLINE_RELEASE_BASE": r.srv.URL + "/download",
	}
}

// listenerHit counts the requests a server receives; a redirect that must be refused is
// pointed at one and the count stays zero.
type listenerHit struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits int
}

func newTLSHit(t *testing.T) *listenerHit {
	t.Helper()
	l := &listenerHit{}
	l.srv = httptest.NewTLSServer(http.HandlerFunc(l.serve))
	t.Cleanup(l.srv.Close)
	return l
}

func newPlainHit(t *testing.T) *listenerHit {
	t.Helper()
	l := &listenerHit{}
	l.srv = httptest.NewServer(http.HandlerFunc(l.serve))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *listenerHit) serve(w http.ResponseWriter, _ *http.Request) {
	l.mu.Lock()
	l.hits++
	l.mu.Unlock()
	_, _ = w.Write([]byte("must not be fetched"))
}

func (l *listenerHit) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hits
}

// updateMachine is a machine with an installed partyline in a directory of its own, a fake
// release channel and the platform linux/amd64 (so tests do not depend on the host).
type updateMachine struct {
	t            *testing.T
	rel          *fakeRelease
	dir          string // the directory of the installed binary
	bin          string // the installed binary
	exe          string // the path the running binary reports for itself (bin, or a link to it)
	env          map[string]string
	goos, goarch string
	client       *http.Client
}

// newUpdateMachine installs installedBinary (mode 0755) in a fresh directory, makes the
// binary report version and points the update command at rel (nil: no release channel).
func newUpdateMachine(t *testing.T, rel *fakeRelease, version string) *updateMachine {
	t.Helper()
	withVersion(t, version)
	withBuildInfo(t, "", false)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	m := &updateMachine{t: t, rel: rel, dir: dir, bin: filepath.Join(dir, "partyline"), env: map[string]string{}, goos: "linux", goarch: "amd64"}
	m.exe = m.bin
	require.NoError(t, os.WriteFile(m.bin, []byte(installedBinary), 0o755))
	if rel != nil {
		m.client = rel.srv.Client()
		for k, v := range rel.env() {
			m.env[k] = v
		}
	}
	return m
}

type updateResult struct {
	code           int
	stdout, stderr string
}

func (m *updateMachine) run(args ...string) updateResult {
	m.t.Helper()
	return m.runWith(context.Background(), args...)
}

func (m *updateMachine) runWith(ctx context.Context, args ...string) updateResult {
	m.t.Helper()
	var out, errOut bytes.Buffer
	code := execute(ctx, append([]string{"partyline", "update"}, args...), stdio{
		stdin: strings.NewReader(""), stdout: &out, stderr: &errOut,
		getenv:     func(k string) string { return m.env[k] },
		executable: func() (string, error) { return m.exe, nil },
		updateHTTP: m.client, goos: m.goos, goarch: m.goarch,
	})
	return updateResult{code: code, stdout: out.String(), stderr: errOut.String()}
}

// installState is everything an update could change about the installed binary: its content,
// its mode, whether it is still the same file, and what else the directory holds.
type installState struct {
	content string
	mode    fs.FileMode
	info    os.FileInfo
	listing []string
}

func (m *updateMachine) state() installState {
	m.t.Helper()
	data, err := os.ReadFile(m.bin)
	require.NoError(m.t, err)
	info, err := os.Stat(m.bin)
	require.NoError(m.t, err)
	return installState{content: string(data), mode: info.Mode().Perm(), info: info, listing: listing(m.t, m.dir)}
}

func listing(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// requireUntouched fails unless the binary and its directory are exactly as before: same bytes,
// same mode, the same file (never replaced) and no temporary file left behind.
func (m *updateMachine) requireUntouched(before installState) {
	m.t.Helper()
	after := m.state()
	require.Equal(m.t, before.content, after.content, "the binary's content changed")
	require.Equal(m.t, before.mode, after.mode, "the binary's mode changed")
	require.True(m.t, os.SameFile(before.info, after.info), "the binary was replaced")
	require.Equal(m.t, before.listing, after.listing, "the directory changed")
}

// requireUpdated fails unless the binary now holds content, is executable (mode 0755) and is
// the only file of its directory.
func (m *updateMachine) requireUpdated(content string) {
	m.t.Helper()
	after := m.state()
	require.Equal(m.t, content, after.content)
	require.Equal(m.t, fs.FileMode(0o755), after.mode)
	require.Equal(m.t, []string{"partyline"}, after.listing, "no temporary file may remain")
}

// needUnixPerms skips a test that relies on unix directory permissions: Windows has none and
// root ignores them.
func needUnixPerms(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permission bits and a non-root user")
	}
}

// lock makes dir read-only for the rest of the test.
func lock(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// redirectTo answers every request with a 302 to target.
func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, http.StatusFound) }
}

// mapHostsTo makes a client dial addr whatever host and port it asked for, so a test can use a
// host name that is "another host" to the update command but reaches the fake server.
func mapHostsTo(c *http.Client, addr string) *http.Client {
	tr := c.Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	out := *c
	out.Transport = tr
	return &out
}
