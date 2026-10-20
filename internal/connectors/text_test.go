package connectors

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// TG-02, DC-02: text above a platform limit is split into ordered parts within the limit.
func TestSplitText(t *testing.T) {
	require.Empty(t, SplitText("", 10))
	require.Equal(t, []string{"short"}, SplitText("short", 10))

	parts := SplitText(strings.Repeat("é", 25), 10)
	require.Equal(t, []string{strings.Repeat("é", 10), strings.Repeat("é", 10), strings.Repeat("é", 5)}, parts,
		"limits count characters, not bytes")

	parts = SplitText("line one\nline two\nline three", 20)
	require.Equal(t, []string{"line one\nline two\n", "line three"}, parts, "breaks after the last newline")

	parts = SplitText("alpha beta gamma delta", 12)
	require.Equal(t, []string{"alpha beta ", "gamma delta"}, parts, "else after the last space")

	long := strings.Repeat("word ", 2000)
	parts = SplitText(long, 4096)
	require.Equal(t, long, strings.Join(parts, ""))
	for _, p := range parts {
		require.LessOrEqual(t, utf8.RuneCountInString(p), 4096)
	}
}

func TestAuthorLine(t *testing.T) {
	require.Equal(t, "*Alice*\nhi", AuthorLine("Alice", "*", "*\n", "hi"))
	require.Equal(t, "hi", AuthorLine("", "*", "*\n", "hi"))
	require.Empty(t, AuthorLine("Alice", "*", "*\n", ""))
}
