package connectors_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// QC-02: the preview of a quoted message is its text, or a bracketed kind followed by its caption
// when there is one; one line, at most 200 characters.
func TestQC02QuotePreview(t *testing.T) {
	for _, c := range []struct {
		kind       dto.MessageKind
		text, name string
		want       string
	}{
		{dto.MessageKindText, "hello", "", "hello"},
		{"", "untyped is text", "", "untyped is text"},
		{dto.MessageKindText, "  two\n\nlines  ", "", "two lines"},
		{dto.MessageKindImage, "option B", "", "[image] option B"},
		{dto.MessageKindImage, "", "", "[image]"},
		{dto.MessageKindVideo, "", "", "[video]"},
		{dto.MessageKindAudio, "", "", "[audio]"},
		{dto.MessageKindDocument, "the plan", "plan.pdf", "[document: plan.pdf] the plan"},
		{dto.MessageKindDocument, "", "", "[document]"},
		{dto.MessageKindSticker, "", "", "[sticker]"},
		{dto.MessageKindLocation, "Office", "", "[location] Office"},
		{dto.MessageKindContact, "Ana", "", "[contact] Ana"},
		{dto.MessageKindPoll, "Lunch?", "", "[poll] Lunch?"},
		{dto.MessageKindUnknown, "", "", ""},
	} {
		require.Equal(t, c.want, connectors.QuotePreview(c.kind, c.text, c.name), "%s %q", c.kind, c.text)
	}
	long := connectors.QuotePreview(dto.MessageKindImage, strings.Repeat("é", 500), "")
	require.Equal(t, dto.MaxQuotedPreview, len([]rune(long)))
	require.True(t, strings.HasPrefix(long, "[image] éé"))
	require.True(t, strings.HasSuffix(long, "é…"))
	require.Equal(t, strings.Repeat("x", 200), connectors.QuotePreview(dto.MessageKindText, strings.Repeat("x", 200), ""), "200 fit")
}
