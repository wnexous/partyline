package runners

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// CA-01: the name defaults to the machine and is made unique per owner with -2, -3…, fitting the
// 64-character limit.
func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"devbox": true, "devbox-2": true}
	require.Equal(t, "desk", uniqueName("desk", taken))
	require.Equal(t, "devbox-3", uniqueName("devbox", taken))
	require.Equal(t, "devbox-2", uniqueName("devbox", map[string]bool{"devbox": true}))

	long := strings.Repeat("a", MaxNameLength)
	require.Equal(t, long, uniqueName(long, nil))
	suffixed := uniqueName(long, map[string]bool{long: true})
	require.Len(t, suffixed, MaxNameLength)
	require.True(t, ValidName(suffixed), suffixed)
	require.True(t, strings.HasSuffix(suffixed, "-2"), suffixed)
}

// CA-01: a runner name is a short label without control characters; a machine is a hostname.
func TestValidNames(t *testing.T) {
	for _, name := range []string{"devbox", "Dev Box 1", "a", "a.b-c_d", strings.Repeat("x", MaxNameLength)} {
		require.True(t, ValidName(name), name)
	}
	for _, name := range []string{"", " x", strings.Repeat("x", MaxNameLength+1), "bad/name", "bad\nname"} {
		require.False(t, ValidName(name), name)
	}
	require.True(t, validMachine("devbox"))
	require.True(t, validMachine("Dev Box"))
	require.False(t, validMachine(""))
	require.False(t, validMachine(strings.Repeat("m", MaxMachineLength+1)))
	require.False(t, validMachine("bad\x00box"))
}
