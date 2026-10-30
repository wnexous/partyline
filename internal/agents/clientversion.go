package agents

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// MinSessionClientVersion is the oldest client version whose sessions send their harness session id
// (SESS-51): v0.11.0. A session of an older client, or of a client that sends no version at all, cannot
// hold the pinned principal role and is shown as outdated (SESS-63).
const MinSessionClientVersion = "v0.11.0"

// clientVersionPattern is the shape of a version header the server keeps: a release version
// ("v1.2.3", "1.2.3-rc1", "abc1234" from git describe) or a local build's "dev".
var clientVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// ClientVersionFromHeader reads the client version of an agent connection. Absent or malformed is
// empty, which counts as an old client; the header never refuses a connection.
func ClientVersionFromHeader(h http.Header, name string) string {
	v := strings.TrimSpace(h.Get(name))
	if !clientVersionPattern.MatchString(v) {
		return ""
	}
	return v
}

// sessionOutdated reports whether a live session's client is too old to send its harness session id:
// only Claude Code and pi have an id to send (SESS-51), so only they can be outdated. A session with no
// id is outdated, and so is one whose client sends no version or a version older than
// MinSessionClientVersion; a local "dev" build is current, as it is built from this source.
func sessionOutdated(harness, sessionID, clientVersion string) bool {
	if harness != "claude" && harness != "pi" && harness != "" {
		return false
	}
	if sessionID == "" {
		return true
	}
	return versionOlder(clientVersion, MinSessionClientVersion)
}

// versionOlder reports whether version v is older than min: missing (empty) is older, "dev" and a value
// that is not a release version (a git hash, a future scheme) are not. Both values are release versions
// like "v0.11.0"; a suffix after '-' or '+' is ignored.
func versionOlder(v, floor string) bool {
	v, floor = strings.TrimSpace(v), strings.TrimSpace(floor)
	if v == "" {
		return true
	}
	if v == "dev" {
		return false
	}
	got, ok := parseVersion(v)
	if !ok {
		return false
	}
	want, ok := parseVersion(floor)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if got[i] != want[i] {
			return got[i] < want[i]
		}
	}
	return false
}

// parseVersion turns "v1.2.3", "1.2" or "1.2.3-rc1" into its three numbers; a suffix and a leading v are
// ignored. It reports false for anything else.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
