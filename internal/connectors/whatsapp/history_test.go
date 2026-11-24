package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
)

// historyPayload is a HistorySync event as evolution-go sends it (pkg/whatsmeow/service: postMap with
// "data" = whatsmeow's *events.HistorySync, encoded with encoding/json): {"Data": the protobuf
// HistorySync with its JSON names}, the event's name after the data (the keys of a Go map are sorted).
// Synthetic values.
func historyPayload(t *testing.T, instance string, conversations ...any) []byte {
	t.Helper()
	p, err := json.Marshal(map[string]any{
		"data":          map[string]any{"Data": map[string]any{"syncType": 6, "conversations": conversations, "progress": 100}},
		"event":         "HistorySync",
		"instanceId":    "00000000-0000-0000-0000-000000000001",
		"instanceName":  instance,
		"instanceToken": "synthetic-instance-token",
	})
	require.NoError(t, err)
	return p
}

// historyFrame wraps a payload in a broadcast frame.
func historyFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	f, err := json.Marshal(Frame{Queue: "00000000-0000-0000-0000-000000000001.historysync", Payload: string(payload)})
	require.NoError(t, err)
	return f
}

// webMsg is a HistorySyncMsg: a WebMessageInfo (key, message, messageTimestamp in seconds, pushName)
// with its order.
func webMsg(remote, id string, fromMe bool, participant string, ts int64, message string) map[string]any {
	key := map[string]any{"remoteJID": remote, "fromMe": fromMe, "ID": id}
	if participant != "" {
		key["participant"] = participant
	}
	return map[string]any{"message": map[string]any{
		"key": key, "message": json.RawMessage(message), "messageTimestamp": ts, "pushName": "Test Person",
	}, "msgOrderID": ts}
}

// syncConversations are a group and a direct chat of a history sync, newest first as WhatsApp lists
// them, with a text, a photo, the account's own message, a reaction and a protocol message.
func syncConversations() []any {
	return []any{
		map[string]any{"ID": groupJID, "name": "Synthetic Group", "messages": []any{
			webMsg(groupJID, "G2", false, peerJID, 1763990200, `{"conversation":"second in the group"}`),
			webMsg(groupJID, "G1", false, peerJID, 1763990100, `{"conversation":"first in the group"}`),
			webMsg(groupJID, "GP", false, peerJID, 1763990150, `{"protocolMessage":{"type":3}}`),
		}},
		map[string]any{"ID": peerJID, "messages": []any{
			webMsg(peerJID, "D3", false, "", 1763990300, `{"reactionMessage":{"key":{"ID":"D1"},"text":"👍"}}`),
			webMsg(peerJID, "D2", true, "", 1763990250, `{"conversation":"typed on the phone"}`),
			webMsg(peerJID, "D1", false, "", 1763990000, `{"imageMessage":{"mimetype":"image/jpeg","caption":"the menu","fileLength":"2048"}}`),
		}},
	}
}

// CH-09: a HistorySync payload becomes the chat messages of each of its conversations, oldest first,
// marked Backfill (added to the history only), with their files named and never downloaded; protocol
// messages are left out and another instance's sync is not this connection's.
func TestCH09ParseHistory(t *testing.T) {
	payload := historyPayload(t, testInstance, syncConversations()...)
	var got []connectors.Inbound
	require.NoError(t, ParseHistory(payload, testInstance, func(p Parsed) { got = append(got, p.Inbound) }))
	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.MessageID
		require.True(t, m.Backfill, m.MessageID)
	}
	require.Equal(t, []string{"G1", "G2", "D1", "D2", "D3"}, ids, "per conversation, oldest first; no protocol message")

	g1 := got[0]
	require.Equal(t, []any{ChatID(testInstance, groupJID), "group", "Synthetic Group", peerJID, "Test Person", "first in the group", false},
		[]any{g1.ChatID, g1.ChatKind, g1.ChatTitle, g1.SenderID, g1.SenderName, g1.Text, g1.FromSelf})
	require.Equal(t, time.Unix(1763990100, 0).UTC(), g1.At.UTC())

	photo, typed, reaction := got[2], got[3], got[4]
	require.Equal(t, []any{ChatID(testInstance, peerJID), "dm", string(dto.MessageKindImage), "the menu", peerJID},
		[]any{photo.ChatID, photo.ChatKind, photo.Kind, photo.Text, photo.SenderID})
	require.Len(t, photo.Media, 1)
	require.Equal(t, []any{"image/jpeg", int64(2048)}, []any{photo.Media[0].Mime, photo.Media[0].Size})
	require.Nil(t, photo.Media[0].Open, "the history names the file; nothing downloads it")
	require.Equal(t, []any{true, "", "typed on the phone"}, []any{typed.FromSelf, typed.SenderID, typed.Text})
	require.Equal(t, []any{string(dto.MessageKindReaction), "D1", "👍"}, []any{reaction.Kind, reaction.ReplyToID, reaction.Text})

	require.ErrorIs(t, ParseHistory(historyPayload(t, "other", syncConversations()...), testInstance, func(Parsed) {}), ErrNotMessage)
	message := frame(t, testInstance, dmInfo("M1"), `{"conversation":"live"}`)
	var f Frame
	require.NoError(t, json.Unmarshal(message, &f))
	require.ErrorIs(t, ParseHistory([]byte(f.Payload), testInstance, func(Parsed) {}), ErrNotMessage)
	require.False(t, isHistoryFrame(message))
	require.True(t, isHistoryFrame(historyFrame(t, payload)))
	// A message whose text reads like the marker is still a message: its quotes are escaped twice.
	require.False(t, isHistoryFrame(frame(t, testInstance, dmInfo("M2"), `{"conversation":"\"event\":\"HistorySync\""}`)))
}

// holdingHandler is a Recorder whose Inbound holds messages of the past until released.
type holdingHandler struct {
	*connectortest.Recorder
	release chan struct{}
	once    sync.Once
}

func (h *holdingHandler) Inbound(ctx context.Context, m connectors.Inbound) {
	if m.Backfill {
		<-h.release
	}
	h.Recorder.Inbound(ctx, m)
}

// newHistoryConnector is a connector of the instance whose connection keeps its history while keeps
// says so.
func newHistoryConnector(t *testing.T, f *fakeEvolution, keeps func() bool) *Connector {
	t.Helper()
	cfg := connectors.Config{"base_url": f.srv.URL, "api_key": globalKey, "instances": []string{testInstance}}
	c, err := New(cfg, connectors.Deps{AllowPrivateNetworks: true, ID: "c1", UserAgent: userAgent,
		KeepsHistory: func(context.Context) bool { return keeps() }})
	require.NoError(t, err)
	return c
}

// CH-09: a history sync on the WebSocket reaches the pipeline as messages of the past, on a worker of
// its own: a live message that comes after it is handed on while the sync is still being worked
// through. A connection that keeps no history skips the sync whole.
func TestCH09HistorySyncDoesNotHoldLiveMessages(t *testing.T) {
	f := newFakeEvolution(t)
	keeps := true
	var mu sync.Mutex
	c := newHistoryConnector(t, f, func() bool { mu.Lock(); defer mu.Unlock(); return keeps })
	h := &holdingHandler{Recorder: connectortest.NewRecorder(), release: make(chan struct{})}
	ctx, stop := context.WithCancel(context.Background())
	ran := make(chan error, 1)
	go func() { ran <- c.Run(ctx, h) }()
	t.Cleanup(func() {
		h.once.Do(func() { close(h.release) })
		stop()
		require.ErrorIs(t, <-ran, context.Canceled)
	})
	sock := f.socket()
	require.Equal(t, connectors.StatusConnected, h.NextStatus(t))

	push(t, sock, historyFrame(t, historyPayload(t, testInstance, syncConversations()...)))
	push(t, sock, frame(t, testInstance, dmInfo("LIVE"), `{"conversation":"live while the sync waits"}`))
	live := h.NextInbound(t)
	require.Equal(t, []any{"live while the sync waits", false}, []any{live.Text, live.Backfill})

	h.once.Do(func() { close(h.release) })
	var past []string
	for range 5 {
		m := h.NextInbound(t)
		require.True(t, m.Backfill)
		past = append(past, m.MessageID)
	}
	require.Equal(t, []string{"G1", "G2", "D1", "D2", "D3"}, past)

	mu.Lock()
	keeps = false
	mu.Unlock()
	push(t, sock, historyFrame(t, historyPayload(t, testInstance, syncConversations()...)))
	push(t, sock, frame(t, testInstance, dmInfo("AFTER"), `{"conversation":"after"}`))
	require.Equal(t, "AFTER", h.NextInbound(t).MessageID, "a connection that keeps no history skips the sync")
	require.True(t, h.NoInbound())
}

// setInstance changes what GET /instance/all says of the instance.
func (f *fakeEvolution) setInstance(name string, fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inst := range f.instances {
		if inst["name"] == name {
			for k, v := range fields {
				inst[k] = v
			}
		}
	}
}

// connects returns the bodies of the POST /instance/connect calls made so far.
func (f *fakeEvolution) connects() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, r := range f.requests {
		if r.Path == "/instance/connect" {
			out = append(out, r.Body)
		}
	}
	return out
}

// CH-10: the connector keeps its instance sending what it needs: when it starts, it turns the WebSocket
// on and subscribes MESSAGE, and HISTORY_SYNC while the connection keeps its history, keeping the
// instance's other events and outputs; a re-pair that turned the WebSocket off is mended at the next
// look; a number that is not logged in is never touched (that would start a QR pairing); an instance
// already right is not asked again.
func TestCH10TheConnectorSubscribesItsInstance(t *testing.T) {
	f := newFakeEvolution(t)
	f.setInstance(testInstance, map[string]any{
		"websocketEnable": "", "events": "MESSAGE,CONNECTION", "webhook": "https://hooks.example.com/in",
		"rabbitmqEnable": "enabled", "natsEnable": "",
	})
	keeps := true
	var mu sync.Mutex
	c := newHistoryConnector(t, f, func() bool { mu.Lock(); defer mu.Unlock(); return keeps })
	checks := checkOnDemand(c)
	rec := connectortest.NewRecorder()
	start(t, c, f, rec)

	req := f.next("/instance/connect")
	require.Equal(t, acmeToken, req.APIKey, "the instance's own token")
	require.Equal(t, map[string]any{
		"subscribe": []any{"MESSAGE", "CONNECTION", "HISTORY_SYNC"}, "websocketEnable": "enabled",
		"webhookUrl": "https://hooks.example.com/in", "rabbitmqEnable": "enabled", "natsEnable": "",
	}, req.Body)

	check(t, checks)
	check(t, checks)
	require.Len(t, f.connects(), 1, "an instance that is right is left alone")

	// A re-pair through a QR code: the WebSocket comes back off.
	f.setInstance(testInstance, map[string]any{"websocketEnable": ""})
	check(t, checks)
	require.Equal(t, "enabled", f.next("/instance/connect").Body["websocketEnable"])

	// The history turned off: HISTORY_SYNC goes, the rest stays.
	mu.Lock()
	keeps = false
	mu.Unlock()
	check(t, checks)
	require.Equal(t, []any{"MESSAGE", "CONNECTION"}, f.next("/instance/connect").Body["subscribe"])

	// A number that is not logged in is never asked: it would start a QR pairing.
	f.setInstance(testInstance, map[string]any{"connected": false, "websocketEnable": ""})
	n := len(f.connects())
	check(t, checks)
	check(t, checks)
	require.Len(t, f.connects(), n)
}

// CH-08: fetch_history asks evolution-go's POST /chat/history-sync, with the instance's token, for count
// messages before the anchor, in the shape of its HistorySyncRequestStruct (whatsmeow's MessageInfo:
// Chat, IsFromMe, IsGroup, ID, Timestamp). An instance not subscribed to HISTORY_SYNC is subscribed
// first; a number that is not connected and a chat of another instance are refused.
func TestCH08FetchHistoryAsksEvolutionGo(t *testing.T) {
	f := newFakeEvolution(t)
	f.setInstance(testInstance, map[string]any{"events": "MESSAGE", "websocketEnable": "enabled"})
	c := newHistoryConnector(t, f, func() bool { return true })
	at := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	err := c.FetchHistory(context.Background(), connectors.HistoryRequest{
		ChatID: ChatID(testInstance, groupJID), AnchorID: "OLDEST", AnchorFromSelf: true, AnchorAt: at, Count: 25,
	})
	require.NoError(t, err)
	connect := f.next("/instance/connect")
	require.Contains(t, connect.Body["subscribe"], "HISTORY_SYNC")
	req := f.next("/chat/history-sync")
	require.Equal(t, acmeToken, req.APIKey)
	require.Equal(t, map[string]any{
		"messageInfo": map[string]any{"Chat": groupJID, "IsFromMe": true, "IsGroup": true, "ID": "OLDEST", "Timestamp": "2026-11-20T12:00:00Z"},
		"count":       float64(25),
	}, req.Body)

	err = c.FetchHistory(context.Background(), connectors.HistoryRequest{ChatID: ChatID("other", peerJID), AnchorID: "X", Count: 1})
	require.ErrorIs(t, err, ErrOtherInstance)
	f.setInstance(testInstance, map[string]any{"connected": false})
	err = c.FetchHistory(context.Background(), connectors.HistoryRequest{ChatID: ChatID(testInstance, peerJID), AnchorID: "X", Count: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not connected")
	require.False(t, errors.Is(err, ErrOtherInstance))
	require.False(t, strings.Contains(err.Error(), acmeToken), "no token in an error")
}
