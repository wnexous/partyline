package agents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ID-01: Antigravity is a known harness (also under its launcher's name agy); its label reads
// "Antigravity", and an unknown harness falls back to "agent".
func TestHarnessIDAndLabelAntigravity(t *testing.T) {
	for _, v := range []string{"antigravity", "Antigravity", " agy "} {
		require.Equal(t, "antigravity", HarnessID(v), v)
	}
	require.Equal(t, "Antigravity", HarnessLabel("antigravity"))
	require.Equal(t, "agent", HarnessID("cursor"))
	require.False(t, PushedHarness("antigravity"), "its messages arrive at turn end, not pushed")
}
