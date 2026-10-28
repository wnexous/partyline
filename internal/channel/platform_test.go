package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// groupEvent is a message of a WhatsApp group: an image with a caption, from a sender nobody
// vouches for.
var groupEvent = dto.PlatformEvent{
	ID: "01HITEM1", Platform: "whatsapp", ConnectionID: "01HCONN", ChatID: "01HCHAT", ChatTitle: "Team", ChatKind: dto.ChatKindGroup,
	SenderName: "Pat", SenderID: "5511999990000", Kind: dto.MessageKindImage, Text: "look at this",
	Attachments: []dto.Attachment{{ID: "01HFILE", Name: "photo.png", Mime: "image/png", Size: 3, Status: dto.AttachmentStatusStored}},
}

// directEvent is a direct message from the owner's verified account.
var directEvent = dto.PlatformEvent{
	ID: "01HITEM2", Platform: "telegram", ConnectionID: "01HCONN2", ChatID: "01HCHAT2", ChatTitle: "André", ChatKind: dto.ChatKindDM,
	SenderName: "André", SenderID: "42", Trusted: true, Kind: dto.MessageKindText, Text: "pode publicar", Attachments: []dto.Attachment{},
}

// CID-06: a platform event becomes one notification whose meta maps the event onto the keys of a
// room message: the sender is the author (author_kind platform), the room is "<Platform> ·
// <chat title>", the event id is the message id the AI replies with, and the chat, the connection
// and the trust flags follow; attachments are listed in the content like those of a message.
func TestFormatPlatformGroupEvent(t *testing.T) {
	n := FormatPlatform(groupEvent)
	require.Equal(t, "look at this\n[attachment 01HFILE: photo.png, image/png]", n.Content)
	require.Equal(t, map[string]string{
		"type": "platform", "room_name": "WhatsApp · Team", "message_id": "01HITEM1",
		"author": "Pat", "author_kind": "platform", "sender_name": "Pat", "sender_id": "5511999990000",
		"trusted": "false", "from_self": "false", "kind": "image",
		"platform": "whatsapp", "connection_id": "01HCONN", "chat_id": "01HCHAT", "chat_title": "Team", "chat_kind": "group",
	}, n.Meta)
}

// CID-06, CID-13, CID-14: a direct event of a verified owner is trusted; a message typed on the
// account itself says so and is not.
func TestFormatPlatformDirectEvents(t *testing.T) {
	n := FormatPlatform(directEvent)
	require.Equal(t, "pode publicar", n.Content)
	require.Equal(t, map[string]string{
		"type": "platform", "room_name": "Telegram · André", "message_id": "01HITEM2",
		"author": "André", "author_kind": "platform", "sender_name": "André", "sender_id": "42",
		"trusted": "true", "from_self": "false", "kind": "text",
		"platform": "telegram", "connection_id": "01HCONN2", "chat_id": "01HCHAT2", "chat_title": "André", "chat_kind": "dm",
	}, n.Meta)

	own := directEvent
	own.Trusted, own.FromSelf = false, true
	m := FormatPlatform(own).Meta
	require.Equal(t, []string{"false", "true"}, []string{m["trusted"], m["from_self"]})
}

// The platform in the room name reads as people write it; other kinds get their name capitalized,
// and a chat without a title is the platform alone. A file without text is the whole content.
func TestFormatPlatformRoomNameAndContent(t *testing.T) {
	for kind, want := range map[string]string{
		"whatsapp": "WhatsApp · Team", "telegram": "Telegram · Team", "discord": "Discord · Team", "webhook": "Webhook · Team",
		"fake": "Fake · Team", "": "Chat · Team",
	} {
		e := groupEvent
		e.Platform = kind
		require.Equal(t, want, FormatPlatform(e).Meta["room_name"], "platform %q", kind)
	}
	untitled := groupEvent
	untitled.ChatTitle = ""
	require.Equal(t, "WhatsApp", FormatPlatform(untitled).Meta["room_name"])

	onlyFile := groupEvent
	onlyFile.Text = ""
	require.Equal(t, "[attachment 01HFILE: photo.png, image/png]", FormatPlatform(onlyFile).Content)
	require.Equal(t, "", FormatPlatform(dto.PlatformEvent{ID: "x"}).Content)
}

// A platform record frames like any message: the framing code that the adapters share shows the sender, the platform and chat, the trust and the ids.
func TestPlatformRecordFramesLikeAMessage(t *testing.T) {
	record := func(e dto.PlatformEvent) map[string]string {
		n := FormatPlatform(e)
		r := map[string]string{"content": n.Content}
		for k, v := range n.Meta {
			r[k] = v
		}
		return r
	}
	require.Equal(t, "<partyline room=\"WhatsApp · Team\" chat=\"Team\" platform=\"whatsapp\" from=\"Pat\" trusted=\"false\" message_id=\"01HITEM1\" connection_id=\"01HCONN\" chat_id=\"01HCHAT\">"+
		"look at this\n[attachment 01HFILE: photo.png, image/png]</partyline>\n"+DataLine, Frame(record(groupEvent)))
	require.Equal(t, "<partyline room=\"Telegram · André\" chat=\"André\" platform=\"telegram\" from=\"André\" trusted=\"true\" message_id=\"01HITEM2\" connection_id=\"01HCONN2\" chat_id=\"01HCHAT2\">"+
		"pode publicar</partyline>\n"+TrustedLine, Frame(record(directEvent)))
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

// platformChan is the channel fixture plus a WhatsApp connection of alice's with a group and a
// direct chat, whose messages are queued for the AI "builder".
type platformChan struct {
	*chanEnv
	connection, group, dm string
}

func newPlatformChan(t *testing.T) *platformChan {
	t.Helper()
	e := newChanEnv(t)
	p := &platformChan{chanEnv: e, connection: store.NewID(), group: store.NewID(), dm: store.NewID()}
	ctx := context.Background()
	require.NoError(t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: p.connection, OwnerID: e.alice.User.ID, Kind: "whatsapp", Name: "wa", ConfigJson: "{}"}); err != nil {
			return err
		}
		if err := q.CreateChat(ctx, db.CreateChatParams{ID: p.group, ConnectorID: p.connection, ExternalID: "120363000000000001@g.us", Title: "Team", Kind: "group"}); err != nil {
			return err
		}
		return q.CreateChat(ctx, db.CreateChatParams{ID: p.dm, ConnectorID: p.connection, ExternalID: "5511999990000", Title: "Pat", Kind: "dm"})
	}))
	return p
}

func (p *platformChan) enqueue(chat, external, text string, opts ...func(*platform.Item)) string {
	p.t.Helper()
	it := platform.Item{AgentID: p.agentID, ConnectorID: p.connection, ChatID: chat, ExternalID: external, SenderID: "5511999990000", SenderName: "Pat", Text: text}
	for _, o := range opts {
		o(&it)
	}
	id, queued, err := p.env.App.Platform.Enqueue(context.Background(), it)
	require.NoError(p.t, err)
	require.True(p.t, queued)
	return id
}

func (p *platformChan) item(id string) platform.Event {
	p.t.Helper()
	e, err := p.env.App.Platform.Get(context.Background(), p.agentID, id)
	require.NoError(p.t, err)
	return e
}

// CID-06, CID-08, CID-09: the channel receives what is queued for its AI, group and direct
// messages alike, as notifications in order, and acknowledges each after writing it, which deletes
// the text on the server; what arrives later is delivered live.
func TestChannelDeliversPlatformEventsAndAcknowledgesThem(t *testing.T) {
	p := newPlatformChan(t)
	group := p.enqueue(p.group, "m1", "hello team", func(it *platform.Item) {
		it.Kind = dto.MessageKindImage
		it.Attachments = []platform.Attachment{{Name: "photo.png", Mime: "image/png", Size: 3, Status: dto.AttachmentStatusStored}}
	})
	direct := p.enqueue(p.dm, "m2", "ping", func(it *platform.Item) { it.Trusted = true })
	s := startSession(t, p.options())
	s.handshake()

	n := s.notification()
	file := p.item(group).Attachments[0].ID
	require.Equal(t, "hello team\n[attachment "+file+": photo.png, image/png]", n.Content)
	require.Equal(t, map[string]string{
		"type": "platform", "room_name": "WhatsApp · Team", "message_id": group,
		"author": "Pat", "author_kind": "platform", "sender_name": "Pat", "sender_id": "5511999990000",
		"trusted": "false", "from_self": "false", "kind": "image",
		"platform": "whatsapp", "connection_id": p.connection, "chat_id": p.group, "chat_title": "Team", "chat_kind": "group",
	}, n.Meta)

	n = s.notification()
	require.Equal(t, "ping", n.Content)
	require.Equal(t, map[string]string{
		"type": "platform", "room_name": "WhatsApp · Pat", "message_id": direct,
		"author": "Pat", "author_kind": "platform", "sender_name": "Pat", "sender_id": "5511999990000",
		"trusted": "true", "from_self": "false", "kind": "text",
		"platform": "whatsapp", "connection_id": p.connection, "chat_id": p.dm, "chat_title": "Pat", "chat_kind": "dm",
	}, n.Meta)

	for _, id := range []string{group, direct} {
		require.Eventually(t, func() bool { return !p.item(id).AckedAt.IsZero() }, wait, 10*time.Millisecond, "the channel acknowledged %s", id)
		require.Empty(t, p.item(id).Text, "the text is deleted with the acknowledgement")
	}

	live := p.enqueue(p.group, "m3", "arrived later")
	require.Equal(t, live, s.notification().Meta["message_id"])
	require.Eventually(t, func() bool { return !p.item(live).AckedAt.IsZero() }, wait, 10*time.Millisecond)
	s.expectNoNotification(200 * time.Millisecond)
}

// fakePlatformGateway is an agent WebSocket that sends the welcome and then the given frames, and
// records the headers of the upgrade and the platform acknowledgements it receives.
type fakePlatformGateway struct {
	url     string
	mu      sync.Mutex
	headers []http.Header
	acks    chan string
}

func newFakePlatformGateway(t *testing.T, frames ...any) *fakePlatformGateway {
	t.Helper()
	g := &fakePlatformGateway{acks: make(chan string, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.headers = append(g.headers, r.Header.Clone())
		g.mu.Unlock()
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		for _, f := range append([]any{dto.AgentWelcomeFrame{Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: "a1", Name: "builder", Owner: "alice"}}}, frames...) {
			data, _ := json.Marshal(f)
			if ws.Write(r.Context(), websocket.MessageText, data) != nil {
				return
			}
		}
		for {
			_, data, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			var a dto.AgentPlatformAckFrame
			if json.Unmarshal(data, &a) == nil && a.Type == dto.AgentFrameTypePlatformAck {
				g.acks <- a.ItemID
			}
		}
	}))
	t.Cleanup(srv.Close)
	g.url = srv.URL
	return g
}

func (g *fakePlatformGateway) options(t *testing.T) Options {
	return Options{URL: g.url, Token: "pl_agent_test", Machine: "testbox", Folder: "/work/builder", TempDir: t.TempDir(), timing: fastTiming}
}

func (g *fakePlatformGateway) nextAck(t *testing.T) string {
	t.Helper()
	select {
	case id := <-g.acks:
		return id
	case <-time.After(wait):
		require.FailNow(t, "missing platform ack")
		return ""
	}
}

func platformFrame(e dto.PlatformEvent) dto.AgentPlatformFrame {
	return dto.AgentPlatformFrame{Type: dto.AgentFrameTypePlatform, Event: e}
}

// CID-25 (design: the link drops repeats): an event id already notified is acknowledged again
// without a second notification, as a message is.
func TestLinkNotifiesARepeatedPlatformEventOnce(t *testing.T) {
	g := newFakePlatformGateway(t, platformFrame(groupEvent), platformFrame(groupEvent), platformFrame(directEvent))
	s := startSession(t, g.options(t))
	s.handshake()

	require.Equal(t, "01HITEM1", s.notification().Meta["message_id"])
	require.Equal(t, "01HITEM2", s.notification().Meta["message_id"], "the repeat produced no notification")
	require.Equal(t, []string{"01HITEM1", "01HITEM1", "01HITEM2"}, []string{g.nextAck(t), g.nextAck(t), g.nextAck(t)}, "each frame is acknowledged")
	s.expectNoNotification(300 * time.Millisecond)
}

// CID-06: the acknowledgement follows the write to the harness: before Claude Code has initialized
// the notification is held and the event stays unacknowledged.
func TestLinkAcknowledgesPlatformEventsOnlyAfterTheNotificationIsWritten(t *testing.T) {
	g := newFakePlatformGateway(t, platformFrame(groupEvent))
	s := startSession(t, g.options(t))
	select {
	case id := <-g.acks:
		require.FailNow(t, "acknowledged before the notification was written", id)
	case <-time.After(300 * time.Millisecond):
	}
	s.handshake()
	require.Equal(t, "01HITEM1", s.notification().Meta["message_id"])
	require.Equal(t, "01HITEM1", g.nextAck(t))
}

// A platform frame without an id cannot be answered or acknowledged: it is dropped.
func TestLinkDropsAPlatformFrameWithoutAnID(t *testing.T) {
	nameless := groupEvent
	nameless.ID = ""
	g := newFakePlatformGateway(t, platformFrame(nameless), platformFrame(directEvent))
	s := startSession(t, g.options(t))
	s.handshake()
	require.Equal(t, "01HITEM2", s.notification().Meta["message_id"])
	require.Equal(t, "01HITEM2", g.nextAck(t))
	s.expectNoNotification(200 * time.Millisecond)
}

// CID-06: the channel and listen ask for platform events with the features header; the REST
// calls of the tools do not need it.
func TestChannelAndListenAskForPlatformEvents(t *testing.T) {
	g := newFakePlatformGateway(t)
	s := startSession(t, g.options(t))
	s.handshake()
	require.Eventually(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return len(g.headers) == 1 }, wait, 10*time.Millisecond)
	g.mu.Lock()
	require.Equal(t, dto.FeaturePlatform, g.headers[0].Get(dto.HeaderFeatures))
	g.mu.Unlock()

	l := newFakePlatformGateway(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Listen(ctx, ListenOptions{URL: l.url, Token: "pl_agent_test", Machine: "testbox", Folder: "/work/builder", Stdout: &syncBuffer{}, Stderr: &syncBuffer{}, timing: fastTiming})
	}()
	require.Eventually(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.headers) == 1 }, wait, 10*time.Millisecond)
	l.mu.Lock()
	require.Equal(t, dto.FeaturePlatform, l.headers[0].Get(dto.HeaderFeatures))
	l.mu.Unlock()
	cancel()
	require.NoError(t, <-done)
}

// CID-06: listen prints a platform event as one JSON line, the record of the notification
// (type platform, the keys of a message and the content), and acknowledges it after the line.
func TestListenPrintsPlatformEventsAsJSONLines(t *testing.T) {
	p := newPlatformChan(t)
	r := startListen(t, p.chanEnv, "pi", fastTiming)
	require.Eventually(t, p.online, wait, 10*time.Millisecond)
	id := p.enqueue(p.group, "m1", "hello team", func(it *platform.Item) { it.Trusted = true })

	require.Eventually(t, func() bool { return len(r.lines()) == 1 }, wait, 10*time.Millisecond)
	require.Equal(t, map[string]string{
		"type": "platform", "content": "hello team", "room_name": "WhatsApp · Team", "message_id": id,
		"author": "Pat", "author_kind": "platform", "sender_name": "Pat", "sender_id": "5511999990000",
		"trusted": "true", "from_self": "false", "kind": "text",
		"platform": "whatsapp", "connection_id": p.connection, "chat_id": p.group, "chat_title": "Team", "chat_kind": "group",
	}, r.lines()[0])
	require.Eventually(t, func() bool { return !p.item(id).AckedAt.IsZero() }, wait, 10*time.Millisecond)
	require.Empty(t, p.item(id).Text)
}

// CID-06 (design: the welcome lists the connections the AI operates): the notice that says who the
// AI is also says which connections it operates, by platform and account, with the id platform
// events name them by; an AI that operates none, and a server that sends no list, add nothing.
func TestIdentityNoticeListsTheConnectionsTheAIOperates(t *testing.T) {
	ai := dto.IntelligenceRef{ID: "ai-1", Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Builder", Handle: "builder"}
	owner := dto.IntelligenceRef{ID: "user-1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "alice", Handle: "alice"}
	notice := func(connections []dto.AgentConnection) string {
		t.Helper()
		s := startSession(t, Options{
			URL: welcomeGateway(t, dto.AgentWelcomeFrame{Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: "ai-1", Name: "builder", Owner: "alice"},
				Intelligence: ai, Owner: owner, Connections: connections}),
			Token: "pl_agent_test", Machine: "testbox", Folder: "/work/builder", TempDir: t.TempDir(), timing: fastTiming,
		})
		s.handshake()
		return s.identityNotice().Content
	}

	require.Equal(t, "You are Builder (@builder). Your owner is alice (@alice).", notice(nil))
	require.Equal(t, "You are Builder (@builder). Your owner is alice (@alice).", notice([]dto.AgentConnection{}))

	one := notice([]dto.AgentConnection{{ID: "01HCONN1", Platform: "whatsapp", AccountLabel: "+55 11 *****-0000"}})
	require.Equal(t, "You are Builder (@builder). Your owner is alice (@alice). You operate: WhatsApp +55 11 *****-0000 (connection 01HCONN1).", one)

	two := notice([]dto.AgentConnection{
		{ID: "01HCONN1", Platform: "whatsapp", AccountLabel: "+55 11 *****-0000"},
		{ID: "01HCONN2", Platform: "telegram"},
	})
	require.Equal(t, "You are Builder (@builder). Your owner is alice (@alice). "+
		"You operate: WhatsApp +55 11 *****-0000 (connection 01HCONN1), Telegram (connection 01HCONN2).", two)
}

// CID-06, end to end: the AI that operates a connection is told so when its channel connects.
func TestChannelTellsTheAIWhichConnectionsItOperates(t *testing.T) {
	p := newPlatformChan(t)
	ctx := context.Background()
	require.NoError(t, p.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.SetConnectorOperator(ctx, db.SetConnectorOperatorParams{OperatorIid: sql.NullString{String: p.agentID, Valid: true}, ID: p.connection}); err != nil {
			return err
		}
		return q.SetConnectorAccountLabel(ctx, db.SetConnectorAccountLabelParams{AccountLabel: "+55 11 *****-0000", ID: p.connection})
	}))
	s := startSession(t, p.options())
	s.handshake()
	require.Equal(t, `You are Builder (@builder), an AI of alice (alice). Messages marked trusted="true" come from the identities that manage you: alice (@alice). `+
		"You operate: WhatsApp +55 11 *****-0000 (connection "+p.connection+").", s.identityNotice().Content)
}
