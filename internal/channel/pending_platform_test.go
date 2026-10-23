package channel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

var pendingTime = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// pendingMessage is a room message of the owner's, created at pendingTime plus offset.
func pendingMessage(id, text string, offset time.Duration) dto.AgentMessage {
	return dto.AgentMessage{Trusted: true, Room: dto.RoomRef{ID: "r1", Name: "Support"}, Message: dto.Message{
		ID: id, RoomID: "r1", AuthorKind: dto.AuthorKindUser, AuthorName: "Alice", Kind: dto.MessageKindText, Text: text, CreatedAt: pendingTime.Add(offset),
	}}
}

func pendingEvent(e dto.PlatformEvent, offset time.Duration) dto.PlatformEvent {
	e.ReceivedAt = pendingTime.Add(offset)
	return e
}

const (
	framedGroup = `<partyline room="WhatsApp · Team" chat="Team" platform="whatsapp" from="Pat" trusted="false" message_id="01HITEM1" chat_id="01HCHAT">` +
		"look at this\n[attachment 01HFILE: photo.png, image/png]</partyline>\n" + DataLine
	framedDirect = `<partyline room="Telegram · André" chat="André" platform="telegram" from="André" trusted="true" message_id="01HITEM2" chat_id="01HCHAT2">` +
		"pode publicar</partyline>\n" + OwnerLine
	framedRoom = `<partyline room="Support" conversation="group" from="Alice" trusted="true" message_id="m1">first</partyline>` + "\n" + OwnerLine
)

// CID-06: FrameAll frames platform events with the same framing and the same trust line as room
// messages, separated by a blank line, oldest first across both kinds; a message goes before an
// event of the same time, and the messages alone frame as they always did.
func TestFrameAllFramesPlatformEventsWithMessages(t *testing.T) {
	require.Equal(t, framedGroup+"\n\n"+framedDirect, FrameAll(nil, pendingEvent(groupEvent, 0), pendingEvent(directEvent, time.Minute)))
	require.Equal(t, framedGroup, FrameAll(nil, groupEvent))
	require.Equal(t, "", FrameAll(nil))

	m1, m2 := pendingMessage("m1", "first", 0), pendingMessage("m2", "second", 2*time.Hour)
	framedSecond := `<partyline room="Support" conversation="group" from="Alice" trusted="true" message_id="m2">second</partyline>` + "\n" + OwnerLine
	require.Equal(t, framedRoom+"\n\n"+framedSecond, FrameAll([]dto.AgentMessage{m1, m2}))

	// Between the two messages an event arrived, and one before and one after both.
	before, between, after := pendingEvent(groupEvent, -time.Hour), pendingEvent(directEvent, time.Hour), pendingEvent(groupEvent, 3*time.Hour)
	between.ID, after.ID = "01HITEM3", "01HITEM4"
	frame := func(e dto.PlatformEvent) string { return Frame(PlatformRecord(e)) }
	require.Equal(t,
		frame(before)+"\n\n"+framedRoom+"\n\n"+frame(between)+"\n\n"+framedSecond+"\n\n"+frame(after),
		FrameAll([]dto.AgentMessage{m1, m2}, before, between, after))

	tie := pendingEvent(directEvent, 0)
	require.Equal(t, framedRoom+"\n\n"+framedDirect, FrameAll([]dto.AgentMessage{m1}, tie), "a message first on a tie")
}

// PlatformRecord is the record of a platform event: the keys of its notification plus the content.
func TestPlatformRecordIsTheNotificationWithItsContent(t *testing.T) {
	r := PlatformRecord(groupEvent)
	n := FormatPlatform(groupEvent)
	require.Equal(t, n.Content, r["content"])
	for k, v := range n.Meta {
		require.Equal(t, v, r[k], k)
	}
	require.Len(t, r, len(n.Meta)+1)
}

// pendingServer answers GET /agent/pending with body and records the query of every call.
func pendingServer(t *testing.T, body string) (string, chan url.Values) {
	t.Helper()
	queries := make(chan url.Values, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, queries
}

const pendingBody = `{"items":[],"platform":[{"id":"01HITEM1","platform":"whatsapp","connection_id":"01HCONN","chat_id":"01HCHAT","chat_title":"Team","chat_kind":"group",` +
	`"sender_name":"Pat","sender_id":"5511999990000","trusted":false,"from_self":false,"kind":"image","text":"look at this",` +
	`"attachments":[{"id":"01HFILE","name":"photo.png","mime":"image/png","size":3,"status":"stored"}],"received_at":"2026-10-22T12:00:00Z"}]}`

// CID-06: Pending (the turn-end hooks) asks for the platform events with platform=1 and returns
// both lists; a server that answers with the room messages alone, as an older one does, gives no
// events and no error.
func TestPendingAsksForPlatformEventsAndReturnsBothLists(t *testing.T) {
	url, queries := pendingServer(t, pendingBody)
	got, err := Pending(context.Background(), PendingOptions{URL: url, Token: "pl_agent_x", Identity: builderIdentity})
	require.NoError(t, err)
	require.Equal(t, url2values("platform=1"), <-queries, "no wait: the hooks do not wait")
	require.Empty(t, got.Items)
	require.Len(t, got.Platform, 1)
	require.Equal(t, "01HITEM1", got.Platform[0].ID)
	require.Equal(t, "look at this", got.Platform[0].Text)
	require.True(t, pendingTime.Equal(got.Platform[0].ReceivedAt), "received at %s", got.Platform[0].ReceivedAt)

	old, _ := pendingServer(t, `{"items":[]}`)
	got, err = Pending(context.Background(), PendingOptions{URL: old, Token: "pl_agent_x", Identity: builderIdentity})
	require.NoError(t, err)
	require.Empty(t, got.Items)
	require.Empty(t, got.Platform)
}

func url2values(q string) url.Values {
	v, _ := url.ParseQuery(q)
	return v
}

// TURN-01, CID-06: wait_for_messages asks for the platform events too, with the wait it was
// given, and returns what arrived framed like the hooks frame it; with nothing it says so as before.
func TestWaitForMessagesToolShowsPlatformEvents(t *testing.T) {
	srvURL, queries := pendingServer(t, pendingBody)
	s := startSession(t, Options{URL: srvURL, Token: "pl_agent_x", Machine: "testbox", Folder: "/work/builder", ToolsOnly: true, TempDir: t.TempDir(), timing: fastTiming})
	s.handshake()

	text, isErr := s.call("wait_for_messages", map[string]any{"timeout_seconds": 7})
	require.False(t, isErr, text)
	require.Equal(t, framedGroup, text)
	require.Equal(t, url2values("wait=7&platform=1"), <-queries)

	empty, _ := pendingServer(t, `{"items":[],"platform":[]}`)
	s = startSession(t, Options{URL: empty, Token: "pl_agent_x", Machine: "testbox", Folder: "/work/builder", ToolsOnly: true, TempDir: t.TempDir(), timing: fastTiming})
	s.handshake()
	text, isErr = s.call("wait_for_messages", map[string]any{"timeout_seconds": 0})
	require.False(t, isErr, text)
	require.Equal(t, "no new messages in 0 seconds", text)
}
