package memlimit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	mib = 1 << 20
	gib = 1 << 30
)

// OPS-06: PARTYLINE_MEMORY_LIMIT is a size in the syntax of GOMEMLIMIT, or off; anything else names
// what is expected, and sizes that would only keep the garbage collector busy are refused.
func TestParse(t *testing.T) {
	for in, want := range map[string]Limit{
		"300MiB":    {Bytes: 300 * mib},
		"1GiB":      {Bytes: gib},
		"2TiB":      {Bytes: 2 << 40},
		"32768KiB":  {Bytes: 32 * mib},
		"20971520":  {Bytes: 20 * mib},
		"20971520B": {Bytes: 20 * mib},
		" 300 MiB ": {Bytes: 300 * mib},
		"16MiB":     {Bytes: MinLimit},
		"off":       {Off: true},
		" off ":     {Off: true},
	} {
		got, err := Parse(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for in, why := range map[string]string{
		"":                       "use a number of bytes",
		"auto":                   "use a number of bytes",
		"512MB":                  "use a number of bytes",
		"512mib":                 "use a number of bytes",
		"-1GiB":                  "use a number of bytes",
		"0":                      "use a number of bytes",
		"1.5GiB":                 "use a number of bytes",
		"MiB":                    "use a number of bytes",
		"15MiB":                  "at least 16MiB",
		"100":                    "at least 16MiB",
		"9007199254740992TiB":    "too large",
		"9223372036854775808":    "use a number of bytes",
		"9223372036854775807KiB": "too large",
	} {
		_, err := Parse(in)
		require.ErrorContains(t, err, why, in)
		require.ErrorContains(t, err, "invalid size", in)
	}
}

// runtimeLimit stands in for the Go runtime's memory limit: debug.SetMemoryLimit changes the limit of the
// whole test binary, which a test must not do.
type runtimeLimit struct {
	limit int64
	sets  []int64 // the limits Apply asked for
}

func newRuntimeLimit(limit int64) *runtimeLimit { return &runtimeLimit{limit: limit} }

func (r *runtimeLimit) set(n int64) int64 {
	prev := r.limit
	if n >= 0 {
		r.limit = n
		r.sets = append(r.sets, n)
	}
	return prev
}

// write puts the file in the fake filesystem under root.
func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// cgroupsOf is a fake filesystem whose /proc/self/cgroup is self and that holds the files.
func cgroupsOf(t *testing.T, self string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "proc/self/cgroup", self)
	for name, content := range files {
		write(t, root, name, content)
	}
	return root
}

// OPS-06: in a container with a memory limit (cgroup v2 with its own namespace, as Docker runs it) the
// Go limit is 90% of that limit, so the collector works before the container's OOM killer does.
func TestApplyUsesTheMemoryLimitOfTheContainer(t *testing.T) {
	root := cgroupsOf(t, "0::/\n", map[string]string{"sys/fs/cgroup/memory.max": "536870912\n"})
	rt := newRuntimeLimit(unlimited)

	got := Apply(nil, "", root, rt.set)

	want := int64(536870912 / 100 * 90)
	require.Equal(t, Result{Bytes: want, Source: SourceCgroup}, got)
	require.Equal(t, []int64{want}, rt.sets)
	require.Less(t, want, int64(512*mib))
	require.Greater(t, want, int64(450*mib), "about 90%")
}

// OPS-06: a limit set further up counts too: a service in a slice is held by the slice, and the
// smallest limit on the way up to the root is the one that applies. A cgroup without a limit ("max")
// and a root without the file do not hide it.
func TestApplyTakesTheSmallestLimitOnTheWayUp(t *testing.T) {
	root := cgroupsOf(t, "0::/system.slice/partyline.service\n", map[string]string{
		"sys/fs/cgroup/system.slice/partyline.service/memory.max": "max\n",
		"sys/fs/cgroup/system.slice/memory.max":                   "1073741824\n",
	})
	rt := newRuntimeLimit(unlimited)

	got := Apply(nil, "", root, rt.set)

	require.Equal(t, Result{Bytes: int64(gib / 100 * 90), Source: SourceCgroup}, got)

	// The service's own limit, when smaller, wins.
	write(t, root, "sys/fs/cgroup/system.slice/partyline.service/memory.max", "268435456\n")
	got = Apply(nil, "", root, newRuntimeLimit(unlimited).set)
	require.Equal(t, int64(268435456/100*90), got.Bytes)

	// A limit above the service's own that is smaller than it wins: the service may not use more than its slice.
	write(t, root, "sys/fs/cgroup/system.slice/partyline.service/memory.max", "2147483648\n")
	got = Apply(nil, "", root, newRuntimeLimit(unlimited).set)
	require.Equal(t, int64(gib/100*90), got.Bytes, "the slice's 1GiB, not the service's 2GiB")
}

// OPS-06: cgroup v1 reports "no limit" as a huge number, and its files live under the memory controller.
func TestApplyReadsCgroupV1(t *testing.T) {
	self := "12:memory:/docker/abc\n11:cpu,cpuacct:/docker/abc\n1:name=systemd:/docker/abc\n"
	root := cgroupsOf(t, self, map[string]string{"sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes": "268435456\n"})
	require.Equal(t, Result{Bytes: int64(268435456 / 100 * 90), Source: SourceCgroup}, Apply(nil, "", root, newRuntimeLimit(unlimited).set))

	write(t, root, "sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes", "9223372036854771712\n")
	rt := newRuntimeLimit(unlimited)
	require.Equal(t, Result{}, Apply(nil, "", root, rt.set), "the number v1 uses for no limit")
	require.Empty(t, rt.sets)

	// In a cgroup namespace of its own the path is "/".
	root = cgroupsOf(t, "12:memory:/\n", map[string]string{"sys/fs/cgroup/memory/memory.limit_in_bytes": "134217728\n"})
	require.Equal(t, int64(134217728/100*90), Apply(nil, "", root, newRuntimeLimit(unlimited).set).Bytes)
}

// OPS-06: a host that mixes both has the memory controller on v1; its "0::" line is only systemd's tree.
func TestApplyPrefersTheMemoryControllerOfCgroupV1OnAMixedHost(t *testing.T) {
	root := cgroupsOf(t, "12:memory:/user.slice\n0::/user.slice\n", map[string]string{
		"sys/fs/cgroup/memory/user.slice/memory.limit_in_bytes": "268435456\n",
		"sys/fs/cgroup/user.slice/memory.max":                   "4294967296\n",
	})
	require.Equal(t, int64(268435456/100*90), Apply(nil, "", root, newRuntimeLimit(unlimited).set).Bytes)
}

// OPS-06: no cgroup, no limit in it, a file nobody can read, or a container too small to be told what to
// do: there is no limit, and the runtime is left alone.
func TestApplySetsNothingWithoutAUsableCgroupLimit(t *testing.T) {
	for name, root := range map[string]string{
		"no cgroup at all":   t.TempDir(),
		"no limit":           cgroupsOf(t, "0::/\n", map[string]string{"sys/fs/cgroup/memory.max": "max\n"}),
		"no limit file":      cgroupsOf(t, "0::/\n", nil),
		"nonsense":           cgroupsOf(t, "0::/\n", map[string]string{"sys/fs/cgroup/memory.max": "lots\n"}),
		"zero":               cgroupsOf(t, "0::/\n", map[string]string{"sys/fs/cgroup/memory.max": "0\n"}),
		"unparsable cgroups": cgroupsOf(t, "not a cgroup file\n\n", nil),
		"too small to limit": cgroupsOf(t, "0::/\n", map[string]string{"sys/fs/cgroup/memory.max": "8388608\n"}),
	} {
		rt := newRuntimeLimit(unlimited)
		require.Equal(t, Result{}, Apply(nil, "", root, rt.set), name)
		require.Empty(t, rt.sets, name)
	}
}

// OPS-06: PARTYLINE_MEMORY_LIMIT wins over the container and over GOMEMLIMIT, and off removes the limit
// whatever either of them says.
func TestApplyObeysTheConfiguredLimit(t *testing.T) {
	root := cgroupsOf(t, "0::/\n", map[string]string{"sys/fs/cgroup/memory.max": "536870912\n"})

	rt := newRuntimeLimit(unlimited)
	got := Apply(&Limit{Bytes: 300 * mib}, "100MiB", root, rt.set)
	require.Equal(t, Result{Bytes: 300 * mib, Source: SourceConfig}, got)
	require.Equal(t, []int64{300 * mib}, rt.sets)

	rt = newRuntimeLimit(100 * mib) // the runtime applied GOMEMLIMIT
	got = Apply(&Limit{Off: true}, "100MiB", root, rt.set)
	require.Equal(t, Result{}, got)
	require.Equal(t, []int64{unlimited}, rt.sets, "the limit GOMEMLIMIT set is lifted")
}

// OPS-06: GOMEMLIMIT is the runtime's own variable and applied when the process started: Apply reports it
// and changes nothing, however big the container is, and GOMEMLIMIT=off keeps meaning no limit.
func TestApplyLeavesGOMEMLIMITAlone(t *testing.T) {
	root := cgroupsOf(t, "0::/\n", map[string]string{"sys/fs/cgroup/memory.max": "536870912\n"})

	rt := newRuntimeLimit(200 * mib)
	require.Equal(t, Result{Bytes: 200 * mib, Source: SourceGoMemLimit}, Apply(nil, "200MiB", root, rt.set))
	require.Empty(t, rt.sets)

	rt = newRuntimeLimit(unlimited)
	require.Equal(t, Result{}, Apply(nil, "off", root, rt.set))
	require.Empty(t, rt.sets, "off is an answer too: the container's limit is not used")
}
