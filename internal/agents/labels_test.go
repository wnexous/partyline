package agents

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// SESS-11: the label of a worktree session is the slug of its folder's base name.
func TestLabelForFolder(t *testing.T) {
	cases := []struct {
		name   string
		folder string
		want   string
	}{
		{"the base name", "/repo/wt", "wt"},
		{"uppercase and punctuation give one hyphen", "/repo/Acme API!", "acme-api"},
		{"separators only fall back to session", "/repo/___", "session"},
		{"cut to 40 characters", "/repo/" + strings.Repeat("a", 50), strings.Repeat("a", 40)},
		{"a hyphen left by the cut is dropped", "/repo/" + strings.Repeat("a", 39) + "-bb", strings.Repeat("a", 39)},
		{"an underscore is a separator", "/repo/Acme_2", "acme-2"},
		{"a windows drive path keeps its base", `C:\work\Acme_2`, "acme-2"},
		{"the root falls back to session", "/", "session"},
		{"a hyphen stays", "/repo/claude-2", "claude-2"},
		{"a run of hyphens collapses", "/repo/a--b", "a-b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, LabelForFolder(c.folder))
		})
	}
}

// SESS-11: a session's label is its preferred one when free, else "<harness>-<n>" with the
// smallest free n >= 2; a remembered label wins when it is still free (the reconnect case).
func TestAllocateLabel(t *testing.T) {
	taken := func(labels ...string) func(string) bool {
		return func(label string) bool { return slices.Contains(labels, label) }
	}
	cases := []struct {
		name       string
		preferred  string
		harness    string
		taken      func(string) bool
		remembered string
		want       string
	}{
		{"a free principal", "", "claude", taken(), "", ""},
		{"a free preferred label", "wt", "claude", taken(), "", "wt"},
		{"the preferred label is taken", "wt", "claude", taken("wt"), "", "claude-2"},
		{"the second numbered label is taken too", "wt", "claude", taken("wt", "claude-2"), "", "claude-3"},
		{"the principal label is taken", "", "claude", taken(""), "", "claude-2"},
		{"an empty harness falls back to agent", "", "", taken(""), "", "agent-2"},
		{"a 40-character preferred label leaves room for the suffix", strings.Repeat("a", 40), "claude", taken(strings.Repeat("a", 40)), "", "claude-2"},
		{"a free remembered label wins over the preferred one", "wt", "claude", taken(), "claude-7", "claude-7"},
		{"a taken remembered label gives the next free one", "wt", "claude", taken("claude-7"), "claude-7", "wt"},
		{"a taken worktree preferred label gives the next free numbered one", "claude-2", "claude", taken("claude-2"), "", "claude-3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AllocateLabel(c.preferred, c.harness, c.taken, c.remembered)
			require.Equal(t, c.want, got)
			require.LessOrEqual(t, len(got), dto.MaxSessionLabel)
		})
	}
}

// SESS-11 and SESS-21: a label is 1 to 40 characters of a-z0-9-, never starting with a hyphen.
func TestSessionLabelPattern(t *testing.T) {
	pattern := regexp.MustCompile(dto.SessionLabelPattern)
	require.Equal(t, 40, dto.MaxSessionLabel)
	for _, label := range []string{"wt", "claude-2", strings.Repeat("a", 40)} {
		require.True(t, pattern.MatchString(label), label)
	}
	for _, label := range []string{"", "Wt", "claude_2", "-x", strings.Repeat("a", 41)} {
		require.False(t, pattern.MatchString(label), label)
	}
}
