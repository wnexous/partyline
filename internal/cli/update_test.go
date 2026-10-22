package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/scripts"
)

const (
	latestTag = "v9.9.9"
	linuxAmd  = "linux_amd64"
)

// UPD-01: with a newer release, `partyline update` downloads the archive of this OS and
// architecture, installs the binary it holds, prints the line the spec gives and leaves
// nothing else in the directory.
func TestUpdateReplacesTheRunningBinaryWithTheLatestRelease(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Equal(t, "Updated partyline v0.4.0 → v9.9.9\n", res.stdout)
	require.Empty(t, res.stderr)
	m.requireUpdated(releasedBinary(latestTag, linuxAmd))
	require.True(t, rel.requested("/api/latest"))
	require.True(t, rel.requested(rel.sumsPath()))
	require.True(t, rel.requested(rel.archivePath(linuxAmd)))
}

// UPD-01: the archive is the one of the OS/architecture the binary runs on.
func TestUpdateDownloadsTheArchiveOfTheOSAndArchitecture(t *testing.T) {
	platforms := []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}
	for _, p := range platforms {
		t.Run(p, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, platforms...)
			m := newUpdateMachine(t, rel, "v0.4.0")
			m.goos, m.goarch, _ = strings.Cut(p, "_")

			res := m.run()
			require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
			m.requireUpdated(releasedBinary(latestTag, p))
			for _, other := range platforms {
				require.Equal(t, other == p, rel.requested(rel.archivePath(other)), "archive of %s", other)
			}
		})
	}
}

// UPD-01: without a platform of its own the command uses the one it was built for.
func TestUpdateUsesThePlatformOfTheRunningBinary(t *testing.T) {
	switch runtime.GOOS + "_" + runtime.GOARCH {
	case "linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64":
	default:
		t.Skip("no release archive for this host")
	}
	host := runtime.GOOS + "_" + runtime.GOARCH
	rel := newFakeRelease(t, latestTag, host)
	m := newUpdateMachine(t, rel, "v0.4.0")
	m.goos, m.goarch = "", ""

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	m.requireUpdated(releasedBinary(latestTag, host))
}

// UPD-01, UPD-02: a platform the release has no archive for is a failure that changes
// nothing, and no other platform's archive is downloaded in its place.
func TestUpdateRefusesAPlatformWithoutAnArchive(t *testing.T) {
	for _, p := range []string{"freebsd_amd64", "linux_386", "linux_riscv64", "darwin_ppc64"} {
		t.Run(p, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd, "linux_arm64", "darwin_amd64", "darwin_arm64")
			m := newUpdateMachine(t, rel, "v0.4.0")
			m.goos, m.goarch, _ = strings.Cut(p, "_")
			before := m.state()

			res := m.run()
			require.Equal(t, 1, res.code)
			require.Contains(t, res.stderr, "no release archive for "+strings.Replace(p, "_", "/", 1))
			require.Empty(t, res.stdout)
			m.requireUntouched(before)
			require.False(t, rel.requested(rel.archivePath(linuxAmd)))
		})
	}
}

// UPD-01: the executable's symlinks are resolved: the file the link points to is replaced, the
// link stays a link, and the directory that must be writable is the file's, not the link's.
func TestUpdateReplacesWhatTheRunningBinarysLinkPointsTo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	setup := func(t *testing.T) (*updateMachine, string) {
		rel := newFakeRelease(t, latestTag, linuxAmd)
		m := newUpdateMachine(t, rel, "v0.4.0")
		realDir, linkDir := filepath.Join(m.dir, "real"), filepath.Join(m.dir, "links")
		require.NoError(t, os.Mkdir(realDir, 0o755))
		require.NoError(t, os.Mkdir(linkDir, 0o755))
		require.NoError(t, os.Rename(m.bin, filepath.Join(realDir, "partyline")))
		m.dir, m.bin = realDir, filepath.Join(realDir, "partyline")
		m.exe = filepath.Join(linkDir, "partyline")
		require.NoError(t, os.Symlink(m.bin, m.exe))
		return m, linkDir
	}

	t.Run("replaces the file and keeps the link", func(t *testing.T) {
		m, linkDir := setup(t)
		res := m.run()
		require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
		m.requireUpdated(releasedBinary(latestTag, linuxAmd))
		target, err := os.Readlink(m.exe)
		require.NoError(t, err)
		require.Equal(t, m.bin, target)
		require.Equal(t, []string{"partyline"}, listing(t, linkDir))
	})

	t.Run("a read-only link directory does not matter", func(t *testing.T) {
		needUnixPerms(t)
		m, linkDir := setup(t)
		lock(t, linkDir)
		res := m.run()
		require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
		m.requireUpdated(releasedBinary(latestTag, linuxAmd))
	})

	t.Run("a read-only directory of the file does", func(t *testing.T) {
		needUnixPerms(t)
		m, _ := setup(t)
		lock(t, m.dir)
		before := m.state()
		res := m.run()
		require.Equal(t, 1, res.code)
		require.Contains(t, res.stderr, "install.sh")
		m.requireUntouched(before)
	})
}

// UPD-01: the new binary is executable (mode 0755) whatever the old one's mode was, and an
// old binary that is read-only still gets replaced: only the directory has to be writable.
func TestUpdateInstallsAnExecutableWhateverTheOldModeWas(t *testing.T) {
	needUnixPerms(t)
	for _, mode := range []os.FileMode{0o700, 0o555, 0o644} {
		t.Run(mode.String(), func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			m := newUpdateMachine(t, rel, "v0.4.0")
			require.NoError(t, os.Chmod(m.bin, mode))

			res := m.run()
			require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
			m.requireUpdated(releasedBinary(latestTag, linuxAmd))
		})
	}
}

// UPD-01: the replacement is atomic: while the release is being downloaded the installed binary
// is still the old one, whole, and the new one takes its place as a different file (a rename),
// never as a rewrite of the old file in place.
func TestUpdateReplacesTheBinaryAtomically(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	before := m.state()

	type seen struct {
		content string
		err     error
	}
	during := make(chan seen, 8)
	for _, path := range []string{rel.sumsPath(), rel.archivePath(linuxAmd)} {
		body := rel.body(path)
		rel.answer(path, func(w http.ResponseWriter, _ *http.Request) {
			data, err := os.ReadFile(m.bin)
			during <- seen{string(data), err}
			_, _ = w.Write(body)
		})
	}

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	close(during)
	n := 0
	for s := range during {
		require.NoError(t, s.err)
		require.Equal(t, installedBinary, s.content, "the old binary stays whole until the new one is in place")
		n++
	}
	require.Equal(t, 2, n)
	after := m.state()
	require.Equal(t, releasedBinary(latestTag, linuxAmd), after.content)
	require.False(t, os.SameFile(before.info, after.info), "the old file was rewritten in place")
	require.Equal(t, []string{"partyline"}, after.listing)
}

// UPD-01, UPD-02: every spelling of a checksums.txt line that sha256sum, shasum or GoReleaser
// writes is understood, and the line of this platform's archive is the one that counts.
func TestUpdateReadsEveryFormOfChecksumsLine(t *testing.T) {
	archive := releaseArchive(t, latestTag, linuxAmd)
	sum, asset := sha256Hex(archive), assetName(latestTag, linuxAmd)
	elsewhere := strings.Repeat("0", 64) + "  " + assetName(latestTag, "linux_arm64") + "\n"
	for name, sums := range map[string]string{
		"two spaces":              sum + "  " + asset + "\n",
		"binary-mode marker":      sum + " *" + asset + "\n",
		"upper-case hex":          strings.ToUpper(sum) + "  " + asset + "\n",
		"no trailing newline":     sum + "  " + asset,
		"CRLF line endings":       elsewhere[:len(elsewhere)-1] + "\r\n" + sum + "  " + asset + "\r\n",
		"other platforms first":   elsewhere + elsewhere + sum + "  " + asset + "\n",
		"other platforms after":   sum + "  " + asset + "\n" + elsewhere,
		"tab between the columns": sum + "\t" + asset + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd, "linux_arm64")
			rel.set(rel.sumsPath(), []byte(sums))
			m := newUpdateMachine(t, rel, "v0.4.0")

			res := m.run()
			require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
			m.requireUpdated(releasedBinary(latestTag, linuxAmd))
		})
	}
}

// UPD-01: a release API that names the tag without its "v" still installs that release and the
// message names it with the "v" (install.sh adds it the same way).
func TestUpdateNamesATagWithoutVLikeInstallSh(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	rel.set("/api/latest", []byte(`{"tag_name": "9.9.9"}`))
	m := newUpdateMachine(t, rel, "v0.4.0")

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Equal(t, "Updated partyline v0.4.0 → v9.9.9\n", res.stdout)
	m.requireUpdated(releasedBinary(latestTag, linuxAmd))
}

// UPD-01: a development build has no release version, so any release is newer.
func TestUpdateReplacesADevelopmentBuild(t *testing.T) {
	for _, current := range []string{"dev", "dev-0123456789ab", "0123abc"} {
		t.Run(current, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			m := newUpdateMachine(t, rel, current)

			res := m.run()
			require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
			require.Equal(t, "Updated partyline "+current+" → v9.9.9\n", res.stdout)
			m.requireUpdated(releasedBinary(latestTag, linuxAmd))
		})
	}
}

// UPD-02: whatever goes wrong - the lookup, the download, the checksum or the archive - the
// installed binary stays the very same file with the same bytes and mode, nothing is left in
// its directory, the reason is printed and the exit code is 1. Each case breaks only the one
// thing it names, so no other guard can be what refuses it.
func TestUpdateFailureLeavesTheCurrentBinaryUnchanged(t *testing.T) {
	asset := assetName(latestTag, linuxAmd)
	// publish replaces the platform's archive and makes checksums.txt list it correctly.
	publish := func(r *fakeRelease, archive []byte) {
		r.set(r.archivePath(linuxAmd), archive)
		r.set(r.sumsPath(), []byte(sha256Hex(archive)+"  "+asset+"\n"))
	}
	for _, tc := range []struct {
		name    string
		damage  func(r *fakeRelease)
		reasons []string
	}{
		{"archive differs from its checksum", func(r *fakeRelease) {
			r.set(r.archivePath(linuxAmd), gzTar(r.t, tarEntry{name: "partyline", body: "#!/bin/sh\necho tampered\n"}))
		}, []string{"checksum mismatch", asset}},
		{"checksums.txt lists another checksum", func(r *fakeRelease) {
			r.set(r.sumsPath(), []byte(sha256Hex([]byte("other"))+"  "+asset+"\n"))
		}, []string{"checksum mismatch", asset}},
		{"checksums.txt lists a malformed checksum", func(r *fakeRelease) {
			r.set(r.sumsPath(), []byte("not-a-checksum  "+asset+"\n"))
		}, []string{"checksum mismatch", asset}},
		{"checksums.txt lists the archive with a truncated checksum", func(r *fakeRelease) {
			r.set(r.sumsPath(), []byte(sha256Hex(r.body(r.archivePath(linuxAmd)))[:63]+"  "+asset+"\n"))
		}, []string{"checksum mismatch", asset}},
		{"checksums.txt does not list the archive", func(r *fakeRelease) {
			r.set(r.sumsPath(), []byte(sha256Hex([]byte("x"))+"  "+assetName(latestTag, "linux_arm64")+"\n"))
		}, []string{asset, "is not listed in checksums.txt"}},
		{"checksums.txt lists the archive under a path", func(r *fakeRelease) {
			r.set(r.sumsPath(), []byte(sha256Hex(r.body(r.archivePath(linuxAmd)))+"  dist/"+asset+"\n"))
		}, []string{asset, "is not listed in checksums.txt"}},
		{"checksums.txt lists a longer name", func(r *fakeRelease) {
			r.set(r.sumsPath(), []byte(sha256Hex(r.body(r.archivePath(linuxAmd)))+"  "+asset+".sbom.json\n"))
		}, []string{asset, "is not listed in checksums.txt"}},
		{"checksums.txt is empty", func(r *fakeRelease) {
			r.set(r.sumsPath(), nil)
		}, []string{asset, "is not listed in checksums.txt"}},
		{"checksums.txt is missing", func(r *fakeRelease) {
			r.drop(r.sumsPath())
		}, []string{"checksums.txt", "404"}},
		{"archive is missing", func(r *fakeRelease) {
			r.drop(r.archivePath(linuxAmd))
		}, []string{asset, "404"}},
		{"archive download fails", func(r *fakeRelease) {
			r.answer(r.archivePath(linuxAmd), func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			})
		}, []string{asset, "500"}},
		{"archive download is cut short", func(r *fakeRelease) {
			body := r.body(r.archivePath(linuxAmd))
			r.answer(r.archivePath(linuxAmd), func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
				_, _ = w.Write(body[:len(body)/2])
			})
		}, []string{"download " + asset}},
		{"archive is not a gzip archive", func(r *fakeRelease) {
			publish(r, []byte("this is not an archive"))
		}, []string{"could not unpack " + asset}},
		{"archive holds no partyline", func(r *fakeRelease) {
			publish(r, gzTar(r.t, tarEntry{name: "README.md", body: "readme\n"}))
		}, []string{asset + " has no partyline binary"}},
		{"archive holds partyline only in a folder", func(r *fakeRelease) {
			publish(r, gzTar(r.t, tarEntry{name: "partyline_9.9.9/partyline", body: "x"}))
		}, []string{asset + " has no partyline binary"}},
		{"archive holds partyline as a link", func(r *fakeRelease) {
			publish(r, gzTar(r.t, tarEntry{name: "partyline", typ: tar.TypeSymlink, link: "/bin/sh"}))
		}, []string{asset + " has no partyline binary"}},
		{"archive holds an empty partyline", func(r *fakeRelease) {
			publish(r, gzTar(r.t, tarEntry{name: "partyline", body: ""}))
		}, []string{asset, "partyline is empty"}},
		{"release lookup fails", func(r *fakeRelease) {
			r.answer("/api/latest", func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "rate limited", http.StatusForbidden)
			})
		}, []string{"latest release", "403"}},
		{"release lookup is not JSON", func(r *fakeRelease) {
			r.set("/api/latest", []byte("<html>not json</html>"))
		}, []string{"latest release"}},
		{"release lookup names no tag", func(r *fakeRelease) {
			r.set("/api/latest", []byte(`{"name": "x"}`))
		}, []string{"latest release"}},
		{"release server is down", func(r *fakeRelease) {
			r.srv.Close()
		}, []string{"latest release"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			m := newUpdateMachine(t, rel, "v0.4.0")
			before := m.state()
			tc.damage(rel)

			res := m.run()
			require.Equal(t, 1, res.code, "stdout: %s", res.stdout)
			require.Empty(t, res.stdout, "nothing may be reported as updated")
			require.Contains(t, res.stderr, "partyline update: ")
			for _, want := range tc.reasons {
				require.Contains(t, res.stderr, want)
			}
			m.requireUntouched(before)
		})
	}
}

// UPD-02: a release whose tag is not a version is not trusted to build a download URL from:
// the update stops after the lookup, whatever the tag tries to reach.
func TestUpdateRefusesATagThatIsNotAVersion(t *testing.T) {
	for _, tag := range []string{"latest", "v1.2", "v1.2.3.4", "../../etc/passwd", "v1.2.3/../../x", "v1.2.3?x=1", "v1.2.3 ", "v1.2.3\n", "vv1.2.3", "v1.2.x", ""} {
		t.Run(fmt.Sprintf("%q", tag), func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			rel.set("/api/latest", []byte(fmt.Sprintf(`{"tag_name": %q}`, tag)))
			m := newUpdateMachine(t, rel, "v0.4.0")
			before := m.state()

			res := m.run()
			require.Equal(t, 1, res.code)
			require.Contains(t, res.stderr, "latest release")
			require.Empty(t, res.stdout)
			m.requireUntouched(before)
			require.Equal(t, []string{"/api/latest"}, rel.all(), "only the lookup")
		})
	}
}

// UPD-02: nothing is read into memory or onto the disk without a bound. Each limit accepts a
// body of exactly its size and refuses one byte more.
func TestUpdateBoundsWhatItDownloadsAndUnpacks(t *testing.T) {
	payload := releasedBinary(latestTag, linuxAmd)
	for _, tc := range []struct {
		name string
		size func(r *fakeRelease) int
		set  func(l *updateLimit, n int64)
	}{
		{"release lookup", func(r *fakeRelease) int { return len(r.body("/api/latest")) }, func(l *updateLimit, n int64) { l.api = n }},
		{"checksums.txt", func(r *fakeRelease) int { return len(r.body(r.sumsPath())) }, func(l *updateLimit, n int64) { l.checksums = n }},
		{"archive", func(r *fakeRelease) int { return len(r.body(r.archivePath(linuxAmd))) }, func(l *updateLimit, n int64) { l.archive = n }},
		{"unpacked binary", func(*fakeRelease) int { return len(payload) }, func(l *updateLimit, n int64) { l.binary = n }},
	} {
		for _, over := range []bool{false, true} {
			name := tc.name + " at the limit"
			if over {
				name = tc.name + " one byte over"
			}
			t.Run(name, func(t *testing.T) {
				rel := newFakeRelease(t, latestTag, linuxAmd)
				m := newUpdateMachine(t, rel, "v0.4.0")
				before := m.state()
				old := updateLimits
				t.Cleanup(func() { updateLimits = old })
				n := int64(tc.size(rel))
				if over {
					n--
				}
				tc.set(&updateLimits, n)

				res := m.run()
				if !over {
					require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
					m.requireUpdated(payload)
					return
				}
				require.Equal(t, 1, res.code)
				require.Contains(t, res.stderr, "larger than")
				require.Empty(t, res.stdout)
				m.requireUntouched(before)
			})
		}
	}
}

// UPD-02: pressing Ctrl+C while the archive downloads ends the update with exit 1 and leaves the
// binary and its directory as they were.
func TestUpdateInterruptedMidDownloadLeavesTheBinaryUntouched(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	before := m.state()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rel.answer(rel.archivePath(linuxAmd), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		cancel()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})

	res := m.runWith(ctx)
	require.True(t, rel.requested(rel.archivePath(linuxAmd)), "the download had started")
	require.Equal(t, 1, res.code)
	require.Contains(t, res.stderr, "partyline update: ")
	require.Empty(t, res.stdout)
	m.requireUntouched(before)
}

// UPD-03: when no newer release exists the command says so, exits 0 and downloads nothing.
func TestUpdateSaysWhenItIsUpToDate(t *testing.T) {
	rel := newFakeRelease(t, "v0.4.0", linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	before := m.state()

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Equal(t, "partyline v0.4.0 is up to date\n", res.stdout)
	require.Empty(t, res.stderr)
	m.requireUntouched(before)
	require.False(t, rel.requested(rel.sumsPath()))
	require.False(t, rel.requested(rel.archivePath(linuxAmd)))
}

// UPD-03: being up to date needs no write access: a binary in a directory the user cannot
// write is reported current, not as an UPD-05 failure.
func TestUpdateUpToDateInAReadOnlyDirectory(t *testing.T) {
	needUnixPerms(t)
	rel := newFakeRelease(t, "v0.4.0", linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	lock(t, m.dir)
	before := m.state()

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Equal(t, "partyline v0.4.0 is up to date\n", res.stdout)
	require.Empty(t, res.stderr)
	m.requireUntouched(before)
}

// UPD-01, UPD-03, UPD-04: which release counts as newer. Versions compare field by field as
// numbers (v0.10.0 follows v0.9.0), a pre-release precedes its release, a build from git that is
// some commits past a tag counts as that tag, and a build that is not a release is older than
// every release. `--check` shows the answer as its exit code without changing anything.
func TestUpdateVersionOrder(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		update          bool
	}{
		{"v0.4.0", "v0.4.0", false},
		{"v0.4.0", "v0.4.1", true},
		{"v0.4.1", "v0.4.0", false},
		{"v0.4.0", "v0.5.0", true},
		{"v0.5.0", "v0.4.9", false},
		{"v1.9.9", "v2.0.0", true},
		{"v2.0.0", "v1.9.9", false},
		{"v0.9.0", "v0.10.0", true},
		{"v0.10.0", "v0.9.0", false},
		{"v0.0.9", "v0.0.10", true},
		{"v0.0.10", "v0.0.9", false},
		{"0.4.0", "v0.4.1", true},
		{"0.4.1", "v0.4.1", false},
		{"v0.4.0+local", "v0.4.0", false},
		{"v0.5.0-rc.1", "v0.5.0", true},
		{"v0.5.0", "v0.5.0-rc.1", false},
		{"v0.5.0-rc.2", "v0.5.0-rc.10", true},
		{"v0.5.0-rc.10", "v0.5.0-rc.2", false},
		{"v0.5.0-alpha", "v0.5.0-beta", true},
		{"v0.5.0-rc.1", "v0.5.0-rc.1", false},
		{"v0.5.0-rc.1", "v0.5.0-rc.1.1", true},
		{"v0.5.0-1", "v0.5.0-alpha", true},
		{"v0.4.0-3-g0123abc", "v0.4.0", false},
		{"v0.4.0-3-g0123abc-dirty", "v0.4.0", false},
		{"v0.4.0-dirty", "v0.4.0", false},
		{"v0.4.0-3-g0123abc", "v0.4.1", true},
		{"v0.4.0-dirty", "v0.4.1", true},
		{"v0.5.0-rc.1-2-g0123abc", "v0.5.0", true},
		{"dev", "v0.1.0", true},
		{"dev-0123456789ab", "v0.1.0", true},
		{"0123abc", "v0.1.0", true},
		{"0123abc-dirty", "v0.1.0", true},
	} {
		t.Run(tc.current+" to "+tc.latest, func(t *testing.T) {
			rel := newFakeRelease(t, tc.latest)
			m := newUpdateMachine(t, rel, tc.current)
			before := m.state()

			res := m.run("--check")
			require.Empty(t, res.stderr)
			if tc.update {
				require.Equal(t, 1, res.code, "stdout: %s", res.stdout)
				require.Contains(t, res.stdout, "latest:  "+tc.latest)
				require.NotContains(t, res.stdout, "up to date")
			} else {
				require.Equal(t, 0, res.code, "stdout: %s", res.stdout)
				require.Contains(t, res.stdout, "partyline "+tc.current+" is up to date")
			}
			m.requireUntouched(before)
		})
	}
}

// UPD-04: --check on an older binary prints both versions, exits 1 and changes nothing - not
// the binary, not its directory, and it does not even download the release.
func TestUpdateCheckReportsAnAvailableUpdate(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	before := m.state()

	res := m.run("--check")
	require.Equal(t, 1, res.code, "stderr: %s", res.stderr)
	require.Contains(t, res.stdout, "v0.4.0")
	require.Contains(t, res.stdout, "v9.9.9")
	require.NotContains(t, res.stdout, "Updated")
	require.Empty(t, res.stderr)
	m.requireUntouched(before)
	require.False(t, rel.requested(rel.sumsPath()))
	require.False(t, rel.requested(rel.archivePath(linuxAmd)))
}

// UPD-04: --check on the latest version prints both versions and exits 0.
func TestUpdateCheckReportsUpToDate(t *testing.T) {
	rel := newFakeRelease(t, "v0.4.0", linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	before := m.state()

	res := m.run("--check")
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Contains(t, res.stdout, "current: v0.4.0")
	require.Contains(t, res.stdout, "latest:  v0.4.0")
	require.Contains(t, res.stdout, "partyline v0.4.0 is up to date")
	require.Empty(t, res.stderr)
	m.requireUntouched(before)
}

// UPD-04: --check tells the two versions apart in the output, and needs no write access.
func TestUpdateCheckNamesEachVersionAndNeedsNoWriteAccess(t *testing.T) {
	needUnixPerms(t)
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	lock(t, m.dir)
	before := m.state()

	res := m.run("--check")
	require.Equal(t, 1, res.code, "stderr: %s", res.stderr)
	require.Contains(t, res.stdout, "current: v0.4.0")
	require.Contains(t, res.stdout, "latest:  v9.9.9")
	require.Empty(t, res.stderr, "an update that exists is not a failure to write")
	m.requireUntouched(before)
}

// UPD-04, UPD-02: a lookup that fails is an error on stderr with exit 1 and nothing on stdout.
func TestUpdateCheckWhenTheLookupFails(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	rel.answer("/api/latest", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	})
	m := newUpdateMachine(t, rel, "v0.4.0")
	before := m.state()

	res := m.run("--check")
	require.Equal(t, 1, res.code)
	require.Empty(t, res.stdout, "scripts tell a failed lookup from an available update by the empty stdout")
	require.Contains(t, res.stderr, "latest release")
	require.Contains(t, res.stderr, "502")
	m.requireUntouched(before)
}

// UPD-04: the check output belongs to --check; a plain update that finds a newer release prints
// only the result line.
func TestUpdateWithoutCheckPrintsOnlyTheResult(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.NotContains(t, res.stdout, "current:")
	require.NotContains(t, res.stdout, "latest:")
	require.Equal(t, 1, strings.Count(res.stdout, "\n"))
}

// `partyline update` takes only --check: anything else is a usage error (exit 2) that changes
// nothing and contacts nobody.
func TestUpdateUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"v9.9.9"}, {"--check", "extra"}, {"-h"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			m := newUpdateMachine(t, rel, "v0.4.0")
			before := m.state()

			res := m.run(args...)
			require.Equal(t, 2, res.code)
			require.NotEmpty(t, res.stderr)
			require.Empty(t, res.stdout)
			m.requireUntouched(before)
			require.Zero(t, rel.requests())
		})
	}
}

// UPD-05: a directory the user cannot write gets no update: exit 1, the binary and directory
// untouched, and the install command to run instead, with the instance URL from PARTYLINE_URL.
func TestUpdateInAnUnwritableDirectoryPrintsTheInstallCommand(t *testing.T) {
	needUnixPerms(t)
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	m.env["PARTYLINE_URL"] = "https://partyline.example.com/"
	lock(t, m.dir)
	before := m.state()

	res := m.run()
	require.Equal(t, 1, res.code)
	require.Contains(t, res.stderr, "curl -fsSL https://partyline.example.com/install.sh | sh")
	require.Contains(t, res.stderr, m.dir+" is not writable")
	require.Empty(t, res.stdout)
	m.requireUntouched(before)
}

// UPD-05: the install command names the instance the CLI is configured for: PARTYLINE_URL wins
// over the saved config, the saved config is used without it, and a placeholder stands in when
// there is neither.
func TestUpdateInstallCommandUsesTheConfiguredInstance(t *testing.T) {
	needUnixPerms(t)
	for _, tc := range []struct {
		name, envURL, savedURL, want string
	}{
		{"environment", "https://env.example.com", "", "curl -fsSL https://env.example.com/install.sh | sh"},
		{"environment over saved config", "https://env.example.com", "https://saved.example.com", "curl -fsSL https://env.example.com/install.sh | sh"},
		{"saved config", "", "https://saved.example.com/", "curl -fsSL https://saved.example.com/install.sh | sh"},
		{"local instance", "", "http://localhost:8080", "curl -fsSL http://localhost:8080/install.sh | sh"},
		{"neither", "", "", "curl -fsSL https://<your instance>/install.sh | sh"},
		{"user info is left out", "https://me:hunter2@env.example.com", "", "curl -fsSL https://env.example.com/install.sh | sh"},
		{"query and fragment are left out", "https://env.example.com/?a=b#c", "", "curl -fsSL https://env.example.com/install.sh | sh"},
		{"not an http URL", "ftp://env.example.com", "", "curl -fsSL https://<your instance>/install.sh | sh"},
		{"no host", "https:///x", "", "curl -fsSL https://<your instance>/install.sh | sh"},
		{"not a URL", "env.example.com:8080", "", "curl -fsSL https://<your instance>/install.sh | sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			m := newUpdateMachine(t, rel, "v0.4.0")
			home := t.TempDir()
			m.env["HOME"] = home
			if tc.envURL != "" {
				m.env["PARTYLINE_URL"] = tc.envURL
			}
			if tc.savedURL != "" {
				cfg := filepath.Join(home, ".config", "partyline", "config.json")
				require.NoError(t, cliconfig.Save(cfg, cliconfig.Config{URL: tc.savedURL, Token: "pl_agent_synthetic"}))
			}
			lock(t, m.dir)

			res := m.run()
			require.Equal(t, 1, res.code)
			require.Contains(t, res.stderr, tc.want)
			require.NotContains(t, res.stderr, "pl_agent_synthetic", "the token is never printed")
			require.NotContains(t, res.stderr, "hunter2", "a password in the URL is never printed")
		})
	}
}

// UPD-05: the install command says where the installer puts the binary, so the user can aim it
// at the directory of the copy that was just left alone.
func TestUpdateInstallCommandNamesTheInstallDirectory(t *testing.T) {
	needUnixPerms(t)
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	lock(t, m.dir)

	res := m.run()
	require.Equal(t, 1, res.code)
	require.Contains(t, res.stderr, "PARTYLINE_INSTALL_DIR="+m.dir)
}

// UPD-05: only the directory decides. A binary nobody may write, in a directory that is
// writable, is replaced (TestUpdateInstallsAnExecutableWhateverTheOldModeWas), and a writable
// binary in a directory that is not is not touched.
func TestUpdateWritableBinaryInAnUnwritableDirectoryIsLeftAlone(t *testing.T) {
	needUnixPerms(t)
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	require.NoError(t, os.Chmod(m.bin, 0o777))
	lock(t, m.dir)
	before := m.state()

	res := m.run()
	require.Equal(t, 1, res.code)
	require.Contains(t, res.stderr, "install.sh")
	m.requireUntouched(before)
}

// Windows cannot replace a running program: the command prints the manual way to update (the
// archive of the release and the go install command) and exits 1 without changes. Asking only
// for the versions, or finding nothing newer, works there like anywhere else.
func TestUpdateOnWindowsPrintsTheManualCommand(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		rel := newFakeRelease(t, latestTag, "windows_amd64")
		m := newUpdateMachine(t, rel, "v0.4.0")
		m.goos, m.goarch = "windows", "amd64"
		before := m.state()

		res := m.run()
		require.Equal(t, 1, res.code)
		require.Contains(t, res.stderr, rel.srv.URL+"/download/v9.9.9/partyline_9.9.9_windows_amd64.zip")
		require.Contains(t, res.stderr, "go install github.com/wnexous/partyline/cmd/partyline@v9.9.9")
		require.Empty(t, res.stdout)
		m.requireUntouched(before)
		require.False(t, rel.requested(downloadPath(latestTag, "partyline_9.9.9_windows_amd64.zip")))
	})
	t.Run("check", func(t *testing.T) {
		rel := newFakeRelease(t, latestTag, "windows_amd64")
		m := newUpdateMachine(t, rel, "v0.4.0")
		m.goos, m.goarch = "windows", "arm64"
		res := m.run("--check")
		require.Equal(t, 1, res.code)
		require.Contains(t, res.stdout, "v9.9.9")
		require.Empty(t, res.stderr)
	})
	t.Run("up to date", func(t *testing.T) {
		rel := newFakeRelease(t, "v0.4.0", "windows_amd64")
		m := newUpdateMachine(t, rel, "v0.4.0")
		m.goos, m.goarch = "windows", "amd64"
		res := m.run()
		require.Equal(t, 0, res.code)
		require.Equal(t, "partyline v0.4.0 is up to date\n", res.stdout)
	})
}

// UPD-06: the release lookup and the downloads use HTTPS only. A release URL of any other kind
// - plain http (loopback included, unlike install.sh), another scheme, no scheme, user info or
// no host - is refused before anything is requested, and a password in it is not echoed.
func TestUpdateRefusesReleaseURLsThatAreNotHTTPS(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	plain := newPlainHit(t)
	good := rel.srv.URL
	withUserInfo := strings.Replace(good, "https://", "https://me:hunter2@", 1)
	for _, tc := range []struct {
		name, key, value string
	}{
		{"API over http", "PARTYLINE_RELEASES_API", plain.srv.URL + "/api/latest"},
		{"download base over http", "PARTYLINE_RELEASE_BASE", plain.srv.URL + "/download"},
		{"API over http in capitals", "PARTYLINE_RELEASES_API", strings.Replace(plain.srv.URL, "http://", "HTTP://", 1) + "/api/latest"},
		{"download base over http in capitals", "PARTYLINE_RELEASE_BASE", strings.Replace(plain.srv.URL, "http://", "HTTP://", 1) + "/download"},
		{"API with another scheme", "PARTYLINE_RELEASES_API", "ftp://releases.example.com/latest"},
		{"download base with another scheme", "PARTYLINE_RELEASE_BASE", "file:///tmp/releases"},
		{"API without a scheme", "PARTYLINE_RELEASES_API", strings.TrimPrefix(good, "https://") + "/api/latest"},
		{"download base without a scheme", "PARTYLINE_RELEASE_BASE", strings.TrimPrefix(good, "https://") + "/download"},
		{"API with user info", "PARTYLINE_RELEASES_API", withUserInfo + "/api/latest"},
		{"download base with user info", "PARTYLINE_RELEASE_BASE", withUserInfo + "/download"},
		{"API without a host", "PARTYLINE_RELEASES_API", "https:///api/latest"},
		{"download base without a host", "PARTYLINE_RELEASE_BASE", "https:///download"},
	} {
		for _, args := range [][]string{nil, {"--check"}} {
			name := tc.name
			if len(args) > 0 {
				name += " with --check"
			}
			t.Run(name, func(t *testing.T) {
				m := newUpdateMachine(t, rel, "v0.4.0")
				m.env[tc.key] = tc.value
				before := m.state()
				requests := rel.requests()

				res := m.run(args...)
				require.Equal(t, 1, res.code)
				require.Contains(t, res.stderr, "partyline update: ")
				require.Contains(t, res.stderr, "https")
				require.NotContains(t, res.stderr, "hunter2")
				require.Empty(t, res.stdout)
				require.Equal(t, requests, rel.requests(), "nothing is requested from a refused URL")
				require.Zero(t, plain.count())
				m.requireUntouched(before)
			})
		}
	}
}

// UPD-06: a scheme in capitals is still https.
func TestUpdateAcceptsHTTPSInCapitals(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	for k, v := range m.env {
		m.env[k] = strings.Replace(v, "https://", "HTTPS://", 1)
	}

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	m.requireUpdated(releasedBinary(latestTag, linuxAmd))
}

// UPD-06: a redirect inside the release host is followed - for the lookup, the checksums and the
// archive - because release servers move files around (renamed repositories, mirrors).
func TestUpdateFollowsRedirectsInsideTheReleaseHost(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	for _, path := range []string{"/api/latest", rel.sumsPath(), rel.archivePath(linuxAmd)} {
		moved := "/moved" + path
		rel.set(moved, rel.body(path))
		rel.answer(path, redirectTo(moved))
	}

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	m.requireUpdated(releasedBinary(latestTag, linuxAmd))
	for _, path := range []string{"/api/latest", rel.sumsPath(), rel.archivePath(linuxAmd)} {
		require.True(t, rel.requested("/moved"+path), path)
	}
}

// UPD-06: a redirect to any other host is refused for each of the three requests an update makes,
// whether the target is another server, the same server under another name, or plain http. The
// target never receives a request, the exit code is 1 and the binary is untouched.
func TestUpdateRefusesRedirectsToOtherHosts(t *testing.T) {
	for _, file := range []string{"release lookup", "checksums.txt", "archive"} {
		for _, target := range []string{"another server", "another name of the server", "plain http"} {
			t.Run(file+" to "+target, func(t *testing.T) {
				rel := newFakeRelease(t, latestTag, linuxAmd)
				m := newUpdateMachine(t, rel, "v0.4.0")
				before := m.state()

				var location string
				var elsewhere func() bool // whether the target was reached
				switch target {
				case "another server":
					other := newTLSHit(t)
					location, elsewhere = other.srv.URL+"/elsewhere", func() bool { return other.count() > 0 }
				case "plain http":
					other := newPlainHit(t)
					location, elsewhere = other.srv.URL+"/elsewhere", func() bool { return other.count() > 0 }
				case "another name of the server":
					// example.com is on the test certificate; the client is told to reach the fake
					// server for it, so a redirect that is followed would succeed.
					_, p, _ := net.SplitHostPort(rel.srv.Listener.Addr().String())
					m.client = mapHostsTo(rel.srv.Client(), rel.srv.Listener.Addr().String())
					rel.set("/elsewhere", []byte(`{"tag_name": "v9.9.9"}`))
					location, elsewhere = "https://example.com:"+p+"/elsewhere", func() bool { return rel.requested("/elsewhere") }
				}
				path := map[string]string{"release lookup": "/api/latest", "checksums.txt": rel.sumsPath(), "archive": rel.archivePath(linuxAmd)}[file]
				rel.answer(path, redirectTo(location))

				res := m.run()
				require.Equal(t, 1, res.code, "stdout: %s", res.stdout)
				require.Contains(t, res.stderr, "redirect")
				require.Empty(t, res.stdout)
				require.False(t, elsewhere(), "the redirect target was requested")
				m.requireUntouched(before)
			})
		}
	}
}

// UPD-06: the redirect rule as a function of the two URLs. A hop stays on the host the first
// request went to, over https and on the same port; GitHub's own release host may hand the
// download to GitHub's release-asset hosts (it always does) and nobody else may; a request that
// was redirected four times is not redirected again.
func TestReleaseRedirectPolicy(t *testing.T) {
	req := func(raw string) *http.Request {
		r, err := http.NewRequest(http.MethodGet, raw, nil)
		require.NoError(t, err)
		return r
	}
	const gh = "https://github.com/acme/partyline/releases/download/v9.9.9/checksums.txt"
	for _, tc := range []struct {
		name, from, to string
		allowed        bool
	}{
		{"same host", "https://releases.example.com/a", "https://releases.example.com/b", true},
		{"same host in another case", "https://Releases.Example.com/a", "https://releases.example.com/b", true},
		{"explicit default port", "https://releases.example.com/a", "https://releases.example.com:443/b", true},
		{"default port on the first request", "https://releases.example.com:443/a", "https://releases.example.com/b", true},
		{"same non-default port", "https://releases.example.com:8443/a", "https://releases.example.com:8443/b", true},
		{"another host", "https://releases.example.com/a", "https://downloads.example.com/b", false},
		{"parent domain", "https://releases.example.com/a", "https://example.com/b", false},
		{"sub domain", "https://releases.example.com/a", "https://x.releases.example.com/b", false},
		{"look-alike suffix", "https://releases.example.com/a", "https://releases.example.com.evil.net/b", false},
		{"look-alike user info", "https://releases.example.com/a", "https://releases.example.com@evil.net/b", false},
		{"another port", "https://releases.example.com/a", "https://releases.example.com:8443/b", false},
		{"another port than a non-default one", "https://releases.example.com:8443/a", "https://releases.example.com/b", false},
		{"downgrade to http", "https://releases.example.com/a", "http://releases.example.com/b", false},
		{"downgrade to http on port 80", "https://releases.example.com/a", "http://releases.example.com:80/b", false},
		{"downgrade to http on port 443", "https://releases.example.com/a", "http://releases.example.com:443/b", false},
		{"downgrade to http on a non-default port", "https://releases.example.com:8443/a", "http://releases.example.com:8443/b", false},
		{"other scheme", "https://releases.example.com/a", "ftp://releases.example.com/b", false},
		{"GitHub to release-assets", gh, "https://release-assets.githubusercontent.com/github-production-release-asset/1/x", true},
		{"GitHub to objects", gh, "https://objects.githubusercontent.com/github-production-release-asset-2e65be/1/x", true},
		{"GitHub to github-releases", gh, "https://github-releases.githubusercontent.com/1/x", true},
		{"GitHub to itself", gh, "https://github.com/acme/other/releases/download/v9.9.9/checksums.txt", true},
		{"GitHub to its assets in another case", gh, "https://Release-Assets.GitHubUserContent.com/x", true},
		{"GitHub to its assets on the default port", gh, "https://release-assets.githubusercontent.com:443/x", true},
		{"GitHub to its assets over http", gh, "http://release-assets.githubusercontent.com/x", false},
		{"GitHub to its assets over http on port 443", gh, "http://release-assets.githubusercontent.com:443/x", false},
		{"GitHub to its assets on another port", gh, "https://release-assets.githubusercontent.com:8443/x", false},
		{"GitHub to another user-content host", gh, "https://raw.githubusercontent.com/acme/partyline/main/x", false},
		{"GitHub to a look-alike of its assets", gh, "https://release-assets.githubusercontent.com.evil.net/x", false},
		{"GitHub to a sub domain of its assets", gh, "https://x.release-assets.githubusercontent.com/x", false},
		{"GitHub to another host", gh, "https://evil.example.net/x", false},
		{"GitHub's API to GitHub's assets", "https://api.github.com/repos/acme/partyline/releases/latest", "https://release-assets.githubusercontent.com/x", false},
		{"GitHub's API to itself", "https://api.github.com/repos/acme/partyline/releases/latest", "https://api.github.com/repositories/1/releases/latest", true},
		{"a mirror to GitHub's assets", "https://mirror.example.com/download/v9.9.9/x", "https://release-assets.githubusercontent.com/x", false},
		{"a look-alike of GitHub to GitHub's assets", "https://github.com.evil.net/a", "https://release-assets.githubusercontent.com/x", false},
		{"a sub domain of GitHub to GitHub's assets", "https://evil.github.com/a", "https://release-assets.githubusercontent.com/x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkReleaseRedirect(req(tc.to), []*http.Request{req(tc.from)})
			if tc.allowed {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), "redirect")
		})
	}

	t.Run("the first request is the anchor, not the previous hop", func(t *testing.T) {
		via := []*http.Request{req(gh), req("https://release-assets.githubusercontent.com/x")}
		require.NoError(t, checkReleaseRedirect(req("https://objects.githubusercontent.com/y"), via))
		require.Error(t, checkReleaseRedirect(req("https://evil.example.net/y"), via))
		via = []*http.Request{req("https://mirror.example.com/a"), req("https://mirror.example.com/b")}
		require.NoError(t, checkReleaseRedirect(req("https://mirror.example.com/c"), via))
		require.Error(t, checkReleaseRedirect(req("https://release-assets.githubusercontent.com/c"), via))
	})

	t.Run("four redirects are followed and a fifth is not", func(t *testing.T) {
		chain := func(n int) []*http.Request {
			via := make([]*http.Request, n)
			for i := range via {
				via[i] = req(fmt.Sprintf("https://releases.example.com/%d", i))
			}
			return via
		}
		require.NoError(t, checkReleaseRedirect(req("https://releases.example.com/next"), chain(4)))
		err := checkReleaseRedirect(req("https://releases.example.com/next"), chain(5))
		require.Error(t, err)
		require.Contains(t, err.Error(), "redirect")
	})
}

// UPD-06: a trailing slash on a release URL does not break the download URLs built from it.
func TestUpdateAcceptsTrailingSlashesOnReleaseURLs(t *testing.T) {
	rel := newFakeRelease(t, latestTag, linuxAmd)
	m := newUpdateMachine(t, rel, "v0.4.0")
	m.env["PARTYLINE_RELEASE_BASE"] += "//"

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	m.requireUpdated(releasedBinary(latestTag, linuxAmd))
}

// UPD-01: the binary is found at the top of the archive whichever way the archiver spells it.
func TestUpdateFindsPartylineAtTheTopOfTheArchive(t *testing.T) {
	asset := assetName(latestTag, linuxAmd)
	for _, name := range []string{"partyline", "./partyline"} {
		t.Run(name, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			archive := gzTar(t,
				tarEntry{name: "LICENSE", body: "license\n"},
				tarEntry{name: "nested/partyline", body: "wrong\n"},
				tarEntry{name: name, body: "right\n"})
			rel.set(rel.archivePath(linuxAmd), archive)
			rel.set(rel.sumsPath(), []byte(sha256Hex(archive)+"  "+asset+"\n"))
			m := newUpdateMachine(t, rel, "v0.4.0")

			res := m.run()
			require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
			m.requireUpdated("right\n")
		})
	}
}

// UPD-05: the command is meant to be pasted into a shell, so an instance URL or a directory that
// the shell would read as more than one word, or as code, is quoted.
func TestUpdateInstallCommandIsQuotedForTheShell(t *testing.T) {
	needUnixPerms(t)
	for _, tc := range []struct{ name, url, want string }{
		{"semicolon", "https://x.example.com/;id", "curl -fsSL 'https://x.example.com/;id/install.sh' | sh"},
		{"dollar", "https://x.example.com/$HOME", "curl -fsSL 'https://x.example.com/$HOME/install.sh' | sh"},
		{"single quote", "https://x.example.com/it's", `curl -fsSL 'https://x.example.com/it'\''s/install.sh' | sh`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := newFakeRelease(t, latestTag, linuxAmd)
			m := newUpdateMachine(t, rel, "v0.4.0")
			m.env["PARTYLINE_URL"] = tc.url
			lock(t, m.dir)

			res := m.run()
			require.Equal(t, 1, res.code)
			require.Contains(t, res.stderr, tc.want)
		})
	}

	t.Run("directory with a space", func(t *testing.T) {
		rel := newFakeRelease(t, latestTag, linuxAmd)
		m := newUpdateMachine(t, rel, "v0.4.0")
		spaced := filepath.Join(m.dir, "my bin")
		require.NoError(t, os.Mkdir(spaced, 0o755))
		require.NoError(t, os.Rename(m.bin, filepath.Join(spaced, "partyline")))
		m.dir, m.bin = spaced, filepath.Join(spaced, "partyline")
		m.exe = m.bin
		lock(t, spaced)

		res := m.run()
		require.Equal(t, 1, res.code)
		require.Contains(t, res.stderr, "| PARTYLINE_INSTALL_DIR='"+spaced+"' sh")
	})
}

// stubTransport answers requests from a table keyed by "https://host/path", so a test can play
// GitHub - including its redirect to the release-asset host - without any network.
type stubTransport struct {
	mu     sync.Mutex
	routes map[string]func(*http.Request) *http.Response
	urls   []string
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path
	s.mu.Lock()
	s.urls = append(s.urls, key)
	route := s.routes[key]
	s.mu.Unlock()
	if route == nil {
		return nil, fmt.Errorf("stub: no route for %s", key)
	}
	resp := route(req)
	resp.Request = req
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	return resp, nil
}

func (s *stubTransport) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

func stubBody(status int, body []byte) func(*http.Request) *http.Response {
	return func(*http.Request) *http.Response {
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body))}
	}
}

func stubRedirect(location string) func(*http.Request) *http.Response {
	return func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {location}}, Body: io.NopCloser(strings.NewReader(""))}
	}
}

// UPD-01, UPD-06: with no environment at all the command takes the release channel install.sh
// uses - GitHub's latest-release API and github.com release downloads - and follows GitHub's
// redirect to its release-asset host, which a plain "same host only" rule would break. The
// transport is a stub: nothing goes out to the network.
func TestUpdateUsesTheGitHubChannelOfInstallSh(t *testing.T) {
	archive := releaseArchive(t, latestTag, linuxAmd)
	const base = "https://github.com/wnexous/partyline/releases/download/v9.9.9/"
	asset := assetName(latestTag, linuxAmd)
	stub := &stubTransport{routes: map[string]func(*http.Request) *http.Response{
		"https://api.github.com/repos/wnexous/partyline/releases/latest": stubBody(200, []byte(`{"tag_name": "v9.9.9"}`)),
		base + "checksums.txt": stubRedirect("https://release-assets.githubusercontent.com/github-production-release-asset/1/sums?sig=secret"),
		base + asset:           stubRedirect("https://release-assets.githubusercontent.com/github-production-release-asset/1/archive?sig=secret"),
		"https://release-assets.githubusercontent.com/github-production-release-asset/1/sums":    stubBody(200, []byte(sha256Hex(archive)+"  "+asset+"\n")),
		"https://release-assets.githubusercontent.com/github-production-release-asset/1/archive": stubBody(200, archive),
	}}
	m := newUpdateMachine(t, nil, "v0.4.0")
	m.client = &http.Client{Transport: stub}

	res := m.run()
	require.Equal(t, 0, res.code, "stderr: %s", res.stderr)
	require.Equal(t, "Updated partyline v0.4.0 → v9.9.9\n", res.stdout)
	m.requireUpdated(releasedBinary(latestTag, linuxAmd))
	require.Equal(t, []string{
		"https://api.github.com/repos/wnexous/partyline/releases/latest",
		base + "checksums.txt",
		"https://release-assets.githubusercontent.com/github-production-release-asset/1/sums",
		base + asset,
		"https://release-assets.githubusercontent.com/github-production-release-asset/1/archive",
	}, stub.requested())
}

// UPD-06: GitHub's allowance is for GitHub. A redirect from github.com to any other host, or from
// a mirror to GitHub's release-asset host, is refused, and the error does not print the signed
// URL it was refused.
func TestUpdateRefusesGitHubRedirectsThatLeaveGitHub(t *testing.T) {
	archive := releaseArchive(t, latestTag, linuxAmd)
	asset := assetName(latestTag, linuxAmd)
	sums := stubBody(200, []byte(sha256Hex(archive)+"  "+asset+"\n"))
	for _, tc := range []struct {
		name, release string
		redirect      string
	}{
		{"GitHub to another host", "https://github.com/wnexous/partyline/releases/download", "https://evil.example.net/x?sig=secret"},
		{"GitHub to a look-alike of its assets", "https://github.com/wnexous/partyline/releases/download", "https://release-assets.githubusercontent.com.evil.net/x?sig=secret"},
		{"a mirror to GitHub's assets", "https://mirror.example.com/releases", "https://release-assets.githubusercontent.com/x?sig=secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.release + "/v9.9.9/"
			stub := &stubTransport{routes: map[string]func(*http.Request) *http.Response{
				"https://api.github.com/repos/wnexous/partyline/releases/latest": stubBody(200, []byte(`{"tag_name": "v9.9.9"}`)),
				base + "checksums.txt": sums,
				base + asset:           stubRedirect(tc.redirect),
			}}
			m := newUpdateMachine(t, nil, "v0.4.0")
			m.client = &http.Client{Transport: stub}
			m.env["PARTYLINE_RELEASE_BASE"] = tc.release
			before := m.state()

			res := m.run()
			require.Equal(t, 1, res.code)
			require.Contains(t, res.stderr, "redirect")
			require.NotContains(t, res.stderr, "sig=secret", "a signed URL is a credential")
			require.Empty(t, res.stdout)
			m.requireUntouched(before)
			for _, u := range stub.requested() {
				require.NotContains(t, u, "evil", "the redirect was followed")
				require.NotContains(t, u, "release-assets", "the redirect was followed")
			}
		})
	}
}

// The release channel is the one install.sh uses: the same repository, the same two URLs, the
// same variables to point them at a mirror and the same archive names. This fails when one of
// them changes without the other.
func TestUpdateAndInstallShShareOneReleaseChannel(t *testing.T) {
	script := scripts.Install("https://partyline.example.com")
	for _, want := range []string{
		"REPO=wnexous/partyline",
		`"${PARTYLINE_RELEASES_API:-https://api.github.com/repos/$REPO/releases/latest}"`,
		`"${PARTYLINE_RELEASE_BASE:-https://github.com/$REPO/releases/download}"`,
		`asset="partyline_${version}_${os}_${arch}.tar.gz"`,
		`$2 == f || $2 == "*" f`,
	} {
		require.Contains(t, script, want)
	}
	require.Equal(t, "https://api.github.com/repos/wnexous/partyline/releases/latest", defaultReleasesAPI)
	require.Equal(t, "https://github.com/wnexous/partyline/releases/download", defaultReleaseBase)
}

// The command is part of the CLI: `partyline --help` lists it with its one flag.
func TestHelpListsUpdate(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		t.Run(flag, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := execute(context.Background(), []string{"partyline", flag}, stdio{stdout: &out, stderr: &errOut, getenv: func(string) string { return "" }})
			require.Equal(t, 0, code)
			require.Contains(t, out.String(), "\n  update ")
			require.Contains(t, out.String(), "--check")
		})
	}
}
