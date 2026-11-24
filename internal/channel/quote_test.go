package channel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/platform"
)

// QC-05: a platform event that quotes a message carries reply_to, quoted and quoted_from_self="true"
// in the notification meta, so in the channel frame and in the record `partyline listen` prints; the
// adapters' frame shows them as attributes, the preview escaped like any platform content. What is
// not known is left out (QC-04).
func TestQC05FormatPlatformCarriesTheQuote(t *testing.T) {
	e := groupEvent
	e.ReplyTo, e.Quoted, e.QuotedFromSelf = "01HSEND1", `[image] option "B" <b>`, true
	n := FormatPlatform(e)
	require.Equal(t, "01HSEND1", n.Meta["reply_to"])
	require.Equal(t, `[image] option "B" <b>`, n.Meta["quoted"])
	require.Equal(t, "true", n.Meta["quoted_from_self"])
	require.Contains(t, Frame(PlatformRecord(e)),
		` message_id="01HITEM1" reply_to="01HSEND1" quoted="[image] option &quot;B&quot; &lt;b&gt;" quoted_from_self="true" connection_id="01HCONN"`)

	e.ReplyTo, e.QuotedFromSelf = "", false
	n = FormatPlatform(e)
	require.NotContains(t, n.Meta, "reply_to")
	require.NotContains(t, n.Meta, "quoted_from_self")
	require.Equal(t, `[image] option "B" <b>`, n.Meta["quoted"])

	n = FormatPlatform(groupEvent)
	for _, k := range []string{"reply_to", "quoted", "quoted_from_self"} {
		require.NotContains(t, n.Meta, k)
	}
}

// QC-05: an item queued while the AI was offline reaches its session with the quote it was queued
// with; the acknowledgement deletes the preview with the text.
func TestQC05ChannelDeliversTheQuoteOfAQueuedItem(t *testing.T) {
	p := newPlatformChan(t)
	earlier := p.enqueue(p.dm, "m1", "which one?")
	quoting := p.enqueue(p.dm, "m2", "that one", func(it *platform.Item) {
		it.ReplyTo, it.Quoted, it.QuotedFromSelf = earlier, "which one?", true
	})
	s := startSession(t, p.options())
	s.handshake()

	n := s.notification()
	require.Equal(t, earlier, n.Meta["message_id"])
	require.NotContains(t, n.Meta, "reply_to")
	n = s.notification()
	require.Equal(t, quoting, n.Meta["message_id"])
	require.Equal(t, earlier, n.Meta["reply_to"])
	require.Equal(t, "which one?", n.Meta["quoted"])
	require.Equal(t, "true", n.Meta["quoted_from_self"])

	require.Eventually(t, func() bool { return !p.item(quoting).AckedAt.IsZero() }, wait, 10*time.Millisecond)
	require.Empty(t, p.item(quoting).Quoted, "the preview goes with the text")
	require.Equal(t, earlier, p.item(quoting).ReplyTo)
}
