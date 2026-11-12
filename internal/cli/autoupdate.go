package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The variables of automatic updates (AU-01..AU-09).
const (
	// autoUpdateVar set to 0 turns automatic updates off (AU-08).
	autoUpdateVar = "PARTYLINE_AUTO_UPDATE"
	// supervisedVar is set on the channel process a supervisor runs: the pid of the harness process
	// that started the supervisor, which the child reads as its parent (AU-04).
	supervisedVar = "PARTYLINE_SUPERVISED"
	// resumedVar=1 marks a child that takes over the session of the child before it: it takes nothing
	// over, asks for no principal role and repeats no notice (AU-06).
	resumedVar = "PARTYLINE_RESUMED"
	// linkLiveVar is the live id every child of one supervisor connects with (AU-06).
	linkLiveVar = "PARTYLINE_LINK_LIVE"
)

// The pace of the checks (AU-01) and the bounds of what they wait for.
const (
	autoUpdateFirst    = time.Minute
	autoUpdateEvery    = 15 * time.Minute
	healthTimeout      = 15 * time.Second
	diskVersionTimeout = 10 * time.Second
)

// autoUpdate keeps one session's client at the server's version (AU-01..AU-03): it asks the server
// which version it runs and installs that release over the executable when it is newer.
type autoUpdate struct {
	instance string // the instance base URL
	current  string // the version the session runs now
	exe      string // the executable to replace
	goos     string
	goarch   string
	health   *http.Client
	updater  updater
	// lockPath is the file the sessions of the machine lock while one installs (AU-03); empty: none.
	lockPath string
	// diskVersion reads the version of the executable on disk (nil: runs `<exe> version`).
	diskVersion func(ctx context.Context, exe string) (string, error)
	// first and every are autoUpdateFirst and autoUpdateEvery; tests shorten them.
	first, every time.Duration
	stderr       io.Writer
}

// isRelease reports whether v is a plain release version (v1.2.3, v1.2.3-rc.1), not a local build ("dev")
// nor one from git past a tag (v1.2.3-4-gabc1234, -dirty): only a release updates itself (AU-02).
func isRelease(v string) bool {
	if strings.HasSuffix(v, "-dirty") || describePattern.MatchString(v) {
		return false
	}
	_, ok := parseVersion(v)
	return ok
}

// isPreRelease reports whether v is a release candidate or another pre-release (v1.2.3-rc.1).
func isPreRelease(v string) bool {
	parsed, ok := parseVersion(v)
	return ok && len(parsed.pre) > 0
}

// autoUpdateFor returns the automatic updates of a session of instance, or nil when they are off. note is
// the one line that says why they are off when the person could not guess it (AU-08); it is empty when
// they were turned off on purpose or the client is not a release.
func autoUpdateFor(io stdio, instance string) (au *autoUpdate, note string) {
	if strings.TrimSpace(io.getenv(autoUpdateVar)) == "0" {
		return nil, ""
	}
	current := buildVersion()
	if !isRelease(current) {
		return nil, ""
	}
	goos, goarch := io.goos, io.goarch
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos == "windows" {
		return nil, "partyline: automatic updates are off on Windows, which cannot replace a running program; run partyline update and restart the session"
	}
	exe, err := executablePath(io)
	if err != nil {
		return nil, fmt.Sprintf("partyline: automatic updates are off: cannot find the running partyline: %v", err)
	}
	channel, err := releaseChannelFrom(io.getenv)
	if err != nil {
		return nil, fmt.Sprintf("partyline: automatic updates are off: %v", err)
	}
	lock := ""
	if dir, err := os.UserCacheDir(); err == nil {
		lock = filepath.Join(dir, "partyline", "update.lock")
	}
	return &autoUpdate{
		instance: strings.TrimRight(instance, "/"), current: current, exe: exe, goos: goos, goarch: goarch,
		health:   &http.Client{Timeout: healthTimeout},
		updater:  updater{client: releaseHTTPClient(io.updateHTTP), channel: channel},
		lockPath: lock, first: autoUpdateFirst, every: autoUpdateEvery, stderr: io.stderr,
	}, ""
}

// run checks the server first after a.first and then every a.every. It returns the version the
// executable on disk has once that is newer than a.current, or "" when ctx ends or updates cannot work
// here (the folder is not writable).
func (a *autoUpdate) run(ctx context.Context) string {
	wait := a.first
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ""
		case <-t.C:
		}
		newer, stop := a.check(ctx)
		if newer != "" {
			return newer
		}
		if stop {
			return ""
		}
		wait = a.every
	}
}

// check runs one check (AU-01..AU-03). newer is the version now on disk when it is newer than the one
// running; stop says that updates cannot work here and the checks end.
func (a *autoUpdate) check(ctx context.Context) (newer string, stop bool) {
	server, err := a.serverVersion(ctx)
	if err != nil || !isRelease(server) || !releaseIsNewer(server, a.current) {
		return "", false // an older or equal server, a dev one, or one that did not answer: nothing to do
	}
	if isPreRelease(server) && !isPreRelease(a.current) {
		return "", false // a client on releases follows no release candidate (AU-02)
	}
	unlock := a.lock()
	defer unlock()
	// Another session of this machine, or the person, may have installed it already (AU-03).
	disk, err := a.readDiskVersion(ctx)
	if err == nil && !isRelease(disk) {
		return "", true // someone put a local build there: leave it alone
	}
	if err == nil && !releaseIsNewer(server, disk) {
		if releaseIsNewer(disk, a.current) {
			return disk, false
		}
		return "", false
	}
	asset, err := archiveName(server, a.goos, a.goarch)
	if err != nil {
		fmt.Fprintf(a.stderr, "partyline: automatic updates are off: %v\n", err)
		return "", true
	}
	err = a.updater.install(ctx, server, asset, a.exe)
	var unwritable *unwritableError
	switch {
	case errors.As(err, &unwritable):
		fmt.Fprintf(a.stderr, "partyline: automatic updates are off: %s is not writable; run partyline update and restart the session\n", unwritable.dir)
		return "", true
	case err != nil:
		if ctx.Err() == nil {
			fmt.Fprintf(a.stderr, "partyline: the update to %s failed (%v); trying again later\n", server, err)
		}
		return "", false
	}
	fmt.Fprintf(a.stderr, "partyline: updated %s → %s\n", a.current, server)
	return server, false
}

// serverVersion is the version the instance runs, as its /healthz says.
func (a *autoUpdate) serverVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.instance+"/healthz", nil)
	if err != nil {
		return "", err
	}
	res, err := a.health.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("healthz: %s", res.Status)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body); err != nil {
		return "", err
	}
	return strings.TrimSpace(body.Version), nil
}

// readDiskVersion is what the executable on disk says it is.
func (a *autoUpdate) readDiskVersion(ctx context.Context) (string, error) {
	if a.diskVersion != nil {
		return a.diskVersion(ctx, a.exe)
	}
	ctx, cancel := context.WithTimeout(ctx, diskVersionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, a.exe, "version").Output() // #nosec G204 -- this program's own path
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// lock takes the machine's update lock (AU-03) and returns its release; without a lock file it is a
// no-op, as two installs still each rename a whole verified file into place.
func (a *autoUpdate) lock() func() {
	if a.lockPath == "" {
		return func() {}
	}
	if err := os.MkdirAll(filepath.Dir(a.lockPath), 0o700); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(a.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if err := lockExclusive(f); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() { _ = f.Close() } // closing the file releases the lock
}

// supervisedParent is the harness pid a supervisor hands its child in PARTYLINE_SUPERVISED, 0 when this
// process is not such a child.
func supervisedParent(getenv func(string) string) int {
	p, err := strconv.Atoi(strings.TrimSpace(getenv(supervisedVar)))
	if err != nil || p <= 0 {
		return 0
	}
	return p
}

// checkAutoUpdate is the doctor line on automatic updates (AU-11), and false for a build that is not a
// release, which never updates itself. It warns when the sessions cannot follow the server though
// nobody turned that off: a harness shows the session's stderr to nobody, so doctor is where it shows.
func checkAutoUpdate(io stdio) (check, bool) {
	c := check{name: "auto-update", ok: true}
	if !isRelease(buildVersion()) {
		return c, false
	}
	if strings.TrimSpace(io.getenv(autoUpdateVar)) == "0" {
		c.detail = "off (" + autoUpdateVar + "=0); run partyline update when the server moves"
		return c, true
	}
	au, note := autoUpdateFor(io, "")
	if au == nil {
		c.warn, c.detail = true, strings.TrimPrefix(note, "partyline: ")
		return c, true
	}
	dir := filepath.Dir(au.exe)
	if err := writableDir(dir); err != nil {
		c.warn = true
		c.detail = dir + " is not writable, so sessions cannot update themselves"
		c.fix = "install where you can write (" + installCommand(io, "") + "), or run partyline update as its owner when the server moves"
		return c, true
	}
	c.detail = "on: sessions follow the server's version"
	return c, true
}

// writableDir reports whether dir takes a new file, as an update needs (UPD-05).
func writableDir(dir string) error {
	f, err := os.CreateTemp(dir, ".partyline-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
