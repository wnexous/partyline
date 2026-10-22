package cli

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The release channel is install.sh's: GitHub's latest-release API names the newest tag and the
// files come from <base>/<tag>/. PARTYLINE_RELEASES_API and PARTYLINE_RELEASE_BASE point the two
// at a mirror, for `partyline update` as for the installer.
const (
	defaultReleasesAPI = "https://api.github.com/repos/wnexous/partyline/releases/latest"
	defaultReleaseBase = "https://github.com/wnexous/partyline/releases/download"
)

// goModule is what `go install` builds partyline from; Windows, which cannot replace a running
// program, is told to use it when it prefers that to downloading the archive.
const goModule = "github.com/wnexous/partyline/cmd/partyline"

// updateLimit bounds what an update reads: the answers of the release lookup and of
// checksums.txt are small, an archive holds one Go binary with the web app in it, and the
// unpacked binary must not fill the disk when an archive is a decompression bomb.
type updateLimit struct{ api, checksums, archive, binary int64 }

// updateLimits are the production bounds; tests shrink them.
var updateLimits = updateLimit{api: 1 << 20, checksums: 1 << 20, archive: 256 << 20, binary: 512 << 20}

const (
	lookupTimeout   = 30 * time.Second
	downloadTimeout = 10 * time.Minute
)

// updateCmd runs `partyline update` (UPD-01..UPD-06): it finds the latest release, and when it is
// newer than this binary downloads the archive of this OS and architecture, checks its SHA-256
// against the release's checksums.txt and replaces the running executable with the binary inside.
// The replacement is a rename of a verified temporary file in the executable's own directory, so
// the old binary stays whole until the new one is in place and any failure leaves it untouched.
// With --check it only reports. Exit codes: 0 done or up to date, 1 an update exists (--check) or
// the update failed, 2 usage.
func updateCmd(ctx context.Context, args []string, io stdio) int {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(io.stderr)
	check := flags.Bool("check", false, "print the current and latest versions and change nothing; exit 1 when a newer release exists")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(io.stderr, "partyline update: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(io.stderr, "partyline update: "+format+"\n", a...)
		return 1
	}

	channel, err := releaseChannelFrom(io.getenv)
	if err != nil {
		return fail("%v", err)
	}
	u := updater{client: releaseHTTPClient(io.updateHTTP), channel: channel}
	current := buildVersion()
	tag, err := u.latestTag(ctx)
	if err != nil {
		return fail("cannot find the latest release: %v", err)
	}

	newer := releaseIsNewer(tag, current)
	if *check {
		fmt.Fprintf(io.stdout, "current: %s\nlatest:  %s\n", current, tag)
	}
	if !newer {
		fmt.Fprintf(io.stdout, "partyline %s is up to date\n", current)
		return 0
	}
	if *check {
		fmt.Fprintln(io.stdout, "Run partyline update to install it.")
		return 1
	}

	goos, goarch := io.goos, io.goarch
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	asset, err := archiveName(tag, goos, goarch)
	if err != nil {
		return fail("%v", err)
	}
	if goos == "windows" {
		fmt.Fprintf(io.stderr, "partyline update: Windows cannot replace a program while it runs, so partyline was not changed\n"+
			"Download %s and replace partyline.exe with the one inside, or run:\n  go install %s@%s\n",
			channel.base+"/"+tag+"/"+asset, goModule, tag)
		return 1
	}

	target, err := executablePath(io)
	if err != nil {
		return fail("cannot find the running partyline: %v", err)
	}
	err = u.install(ctx, tag, asset, target)
	var unwritable *unwritableError
	if errors.As(err, &unwritable) {
		fmt.Fprintf(io.stderr, "partyline update: %s is not writable (%v); partyline was not changed\n"+
			"Install the latest release with the installer instead:\n  %s\n"+
			"That installs to ~/.local/bin; to replace this copy, tell it where:\n  %s\n",
			unwritable.dir, unwritable.err, installCommand(io, ""), installCommand(io, unwritable.dir))
		return 1
	}
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(io.stdout, "Updated partyline %s → %s\n", current, tag)
	return 0
}

// unwritableError means the executable's directory takes no new file (UPD-05).
type unwritableError struct {
	dir string
	err error
}

func (e *unwritableError) Error() string { return e.dir + " is not writable: " + e.err.Error() }

// install replaces target with the binary of the release archive asset of tag. It stages the new
// binary in a temporary file next to target - a directory that takes no new file is reported
// before anything is downloaded - verifies the archive against checksums.txt, unpacks it into the
// temporary file, makes that executable and renames it over target. Nothing but that last rename
// touches target, and every failure removes the temporary file.
func (u updater) install(ctx context.Context, tag, asset, target string) error {
	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, ".partyline-update-*")
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err // the temporary file's random name says nothing
		}
		return &unwritableError{dir: dir, err: err}
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = staged.Close()
			_ = os.Remove(staged.Name())
		}
	}()

	sums, err := u.fetch(ctx, u.channel.base+"/"+tag+"/checksums.txt", updateLimits.checksums, lookupTimeout)
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	want, ok := wantedChecksum(sums, asset)
	if !ok {
		return fmt.Errorf("%s is not listed in checksums.txt", asset)
	}
	archive, err := u.fetch(ctx, u.channel.base+"/"+tag+"/"+asset, updateLimits.archive, downloadTimeout)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("checksum mismatch for %s: nothing was installed", asset)
	}

	switch err := unpackBinary(archive, staged, updateLimits.binary); {
	case err == nil:
	case errors.Is(err, errNoBinary):
		return fmt.Errorf("%s has no partyline binary", asset)
	case errors.Is(err, errEmptyBinary):
		return fmt.Errorf("%s: partyline is empty", asset)
	case errors.Is(err, errBinaryTooLarge):
		return fmt.Errorf("%s: partyline is larger than %d bytes", asset, updateLimits.binary)
	default:
		return fmt.Errorf("could not unpack %s: %w", asset, err)
	}
	if err := staged.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", staged.Name(), err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("write %s: %w", staged.Name(), err)
	}
	if err := os.Chmod(staged.Name(), 0o755); err != nil { // #nosec G302 -- an installed executable is 0755
		return err
	}
	if err := os.Rename(staged.Name(), target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	renamed = true
	return nil
}

// updater fetches release files over HTTPS from one channel.
type updater struct {
	client  *http.Client
	channel releaseChannel
}

// releaseChannel is where the releases are looked up and downloaded from.
type releaseChannel struct{ api, base string }

// releaseChannelFrom reads the channel from the environment (UPD-06): both URLs must be https
// URLs with a host and without user info, plain http included even for loopback hosts. The
// value is never echoed, as it could hold a password.
func releaseChannelFrom(getenv func(string) string) (releaseChannel, error) {
	var ch releaseChannel
	for _, v := range []struct {
		name, fallback string
		dst            *string
	}{
		{"PARTYLINE_RELEASES_API", defaultReleasesAPI, &ch.api},
		{"PARTYLINE_RELEASE_BASE", defaultReleaseBase, &ch.base},
	} {
		raw := strings.TrimSpace(getenv(v.name))
		if raw == "" {
			raw = v.fallback
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return releaseChannel{}, fmt.Errorf("refusing %s: want an https:// URL with a host and without user info", v.name)
		}
		*v.dst = strings.TrimRight(raw, "/")
	}
	return ch, nil
}

// latestTag asks the release API for the newest release and returns its tag with the leading v
// install.sh adds. The tag becomes part of download URLs, so it must be a version.
func (u updater) latestTag(ctx context.Context) (string, error) {
	body, err := u.fetch(ctx, u.channel.api, updateLimits.api, lookupTimeout)
	if err != nil {
		return "", err
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil || release.TagName == "" {
		return "", errors.New("the answer names no release")
	}
	if _, ok := parseVersion(release.TagName); !ok {
		return "", fmt.Errorf("the tag %q is not a version", release.TagName)
	}
	return "v" + strings.TrimPrefix(release.TagName, "v"), nil
}

// fetch GETs rawURL and returns the body, which may not be longer than limit bytes.
func (u updater) fetch(ctx context.Context, rawURL string, limit int64, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "partyline/"+buildVersion())
	resp, err := u.client.Do(req)
	if err != nil {
		// The error of the client quotes the URL, and a redirect target is a signed URL.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the response is larger than %d bytes", limit)
	}
	return body, nil
}

// releaseHTTPClient is base (nil: a plain client) with the redirect policy of release downloads.
func releaseHTTPClient(base *http.Client) *http.Client {
	c := &http.Client{}
	if base != nil {
		*c = *base
	}
	c.CheckRedirect = checkReleaseRedirect
	return c
}

// githubReleaseHost serves GitHub's release downloads. It answers every one with a redirect to a
// host of githubAssetHosts, where the file lives, so a rule of "the release host only" would
// refuse the real thing; only github.com gets this allowance, never a mirror or the API host.
const githubReleaseHost = "github.com"

var githubAssetHosts = map[string]bool{
	"release-assets.githubusercontent.com":  true,
	"objects.githubusercontent.com":         true,
	"github-releases.githubusercontent.com": true,
}

// maxRedirects is how many redirects one download may follow; GitHub's takes one.
const maxRedirects = 4

// origin is a scheme's host and port as the redirect rule compares them.
type origin struct{ host, port string }

func originOf(u *url.URL) origin {
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return origin{host: strings.ToLower(u.Hostname()), port: port}
}

func (o origin) String() string {
	if o.port == "443" {
		return o.host
	}
	return o.host + ":" + o.port
}

// checkReleaseRedirect is the http.Client policy for release lookups and downloads (UPD-06): a
// redirect must stay on the host (and port) the first request went to, and over https; GitHub's
// release host may hand the download to GitHub's release-asset hosts. The error names the host
// only, as the rest of a redirect URL can carry a signature.
func checkReleaseRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	to := originOf(req.URL)
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect to %s: only https is allowed", to)
	}
	from := originOf(via[0].URL)
	switch {
	case to == from:
		return nil
	case from == origin{githubReleaseHost, "443"} && githubAssetHosts[to.host] && to.port == "443":
		return nil
	}
	return fmt.Errorf("refusing redirect to %s: the release files are served from %s", to, from)
}

// archiveName is the release archive of a tag for an OS and architecture, named like
// GoReleaser's archives and install.sh's downloads: partyline_<version>_<os>_<arch>.tar.gz (.zip
// for Windows).
func archiveName(tag, goos, goarch string) (string, error) {
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", fmt.Errorf("no release archive for %s/%s", goos, goarch)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("no release archive for %s/%s", goos, goarch)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "partyline_" + strings.TrimPrefix(tag, "v") + "_" + goos + "_" + goarch + ext, nil
}

// wantedChecksum returns the SHA-256 that checksums.txt lists for asset, in lower case. The lines
// are the ones sha256sum and GoReleaser write, "<hex>  <name>" or "<hex> *<name>" (binary mode).
func wantedChecksum(sums []byte, asset string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && (f[1] == asset || f[1] == "*"+asset) {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

var (
	errNoBinary       = errors.New("no partyline binary")
	errEmptyBinary    = errors.New("empty partyline binary")
	errBinaryTooLarge = errors.New("partyline binary too large")
)

// unpackBinary copies the regular file "partyline" at the top of the tar.gz archive to dst, which
// may not take more than limit bytes. errNoBinary, errEmptyBinary and errBinaryTooLarge say what
// is wrong with a readable archive.
func unpackBinary(archive []byte, dst io.Writer, limit int64) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errNoBinary
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || path.Clean(hdr.Name) != "partyline" {
			continue
		}
		n, err := io.Copy(dst, io.LimitReader(tr, limit+1))
		if err != nil {
			return err
		}
		switch {
		case n > limit:
			return errBinaryTooLarge
		case n == 0:
			return errEmptyBinary
		}
		return nil
	}
}

// executablePath is the file the running binary is: its path with symlinks resolved, so the update
// replaces what a link points to and keeps the link.
func executablePath(io stdio) (string, error) {
	executable := io.executable
	if executable == nil {
		executable = os.Executable
	}
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

// instanceURL is the instance this machine is set up for (PARTYLINE_URL, else the saved config)
// as the base of a URL to print: http(s) with a host, without user info, query or fragment.
// Anything else - or no instance - is "".
func instanceURL(io stdio) string {
	base := strings.TrimSpace(io.getenv("PARTYLINE_URL"))
	if base == "" {
		if cfg, _, err := loadSavedConfig(io); err == nil {
			base = strings.TrimSpace(cfg.URL)
		}
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return strings.TrimRight(u.String(), "/")
}

// installCommand is the install.sh command of the instance (a placeholder when there is none),
// aimed at dir when dir is not empty.
func installCommand(io stdio, dir string) string {
	script := "https://<your instance>/install.sh"
	if base := instanceURL(io); base != "" {
		script = shellQuote(base + "/install.sh")
	}
	if dir == "" {
		return "curl -fsSL " + script + " | sh"
	}
	return "curl -fsSL " + script + " | PARTYLINE_INSTALL_DIR=" + shellQuote(dir) + " sh"
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./~-]+$`)

// shellQuote leaves a word of plain characters as it is and single-quotes anything else, so a
// command printed for the user to run cannot say more than it shows.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// version is a release version: major.minor.patch and the pre-release identifiers, if any.
type version struct {
	major, minor, patch uint64
	pre                 []string
}

var (
	versionPattern  = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	describePattern = regexp.MustCompile(`-\d+-g[0-9a-f]{4,40}$`)
)

// parseVersion reads v1.2.3, 1.2.3, v1.2.3-rc.1 and v1.2.3+build; anything else is not a version.
func parseVersion(s string) (version, bool) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	var v version
	for i, dst := range []*uint64{&v.major, &v.minor, &v.patch} {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return version{}, false
		}
		*dst = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

// compare orders versions by number and then by pre-release, as semver does: a release follows
// its pre-releases, numeric identifiers compare as numbers and come before the others.
func (v version) compare(o version) int {
	for _, c := range []int{cmp.Compare(v.major, o.major), cmp.Compare(v.minor, o.minor), cmp.Compare(v.patch, o.patch)} {
		if c != 0 {
			return c
		}
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		a, aerr := strconv.ParseUint(v.pre[i], 10, 64)
		b, berr := strconv.ParseUint(o.pre[i], 10, 64)
		var c int
		switch {
		case aerr == nil && berr == nil:
			c = cmp.Compare(a, b)
		case aerr == nil:
			c = -1
		case berr == nil:
			c = 1
		default:
			c = strings.Compare(v.pre[i], o.pre[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(v.pre), len(o.pre))
}

// releaseIsNewer reports whether the release tag is newer than the running version. A build from
// git that is some commits past a tag (v0.4.0-3-g0123abc, -dirty) counts as that tag, and a
// running version that is not a version at all (dev) is older than every release.
func releaseIsNewer(tag, running string) bool {
	release, ok := parseVersion(tag)
	if !ok {
		return false
	}
	running = strings.TrimSuffix(running, "-dirty")
	running = describePattern.ReplaceAllString(running, "")
	current, ok := parseVersion(running)
	return !ok || release.compare(current) > 0
}
