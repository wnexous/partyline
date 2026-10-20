package cli

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

// withBuildInfo makes the binary report a main module version (ok=false: no build info).
func withBuildInfo(t *testing.T, version string, ok bool) {
	t.Helper()
	old := readBuildInfo
	t.Cleanup(func() { readBuildInfo = old })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		if !ok {
			return nil, false
		}
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/wnexous/partyline", Version: version}}, true
	}
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	t.Cleanup(func() { Version = old })
	Version = v
}

// ONB-07: `go install github.com/wnexous/partyline/cmd/partyline@v0.4.0` sets no ldflags; the
// module version from the build info is printed instead of "dev".
func TestVersionUsesModuleVersionWithoutLdflags(t *testing.T) {
	withVersion(t, "dev")
	withBuildInfo(t, "v0.4.0", true)

	code, out, _ := run("version")
	require.Equal(t, 0, code)
	require.Equal(t, "v0.4.0\n", out)
}

// ONB-07: release builds set the version with -ldflags; that value wins over the build info.
func TestVersionLdflagsValueWinsOverModuleVersion(t *testing.T) {
	withVersion(t, "v1.2.3")
	withBuildInfo(t, "v0.4.0", true)

	code, out, _ := run("version")
	require.Equal(t, 0, code)
	require.Equal(t, "v1.2.3\n", out)
}

// ONB-07: a local build reports "(devel)" as its module version; that prints "dev", as does a
// binary without build info.
func TestVersionDevelBuildPrintsDev(t *testing.T) {
	withVersion(t, "dev")
	withBuildInfo(t, "(devel)", true)
	code, out, _ := run("version")
	require.Equal(t, 0, code)
	require.Equal(t, "dev\n", out)

	withBuildInfo(t, "", false)
	code, out, _ = run("version")
	require.Equal(t, 0, code)
	require.Equal(t, "dev\n", out)
}
