package connectors_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// Synthetic platform ids.
const (
	groupID   = "120363000000000001@g.us"
	otherID   = "120363000000000002@g.us"
	senderID  = "5511999990000@s.whatsapp.net"
	senderAlt = "100000000000001@lid"
)

type inboundEnv struct {
	env   *testkit.Env
	alice *testkit.Client
	conn  dto.Connector
	inst  *connectortest.Instance
}

// newInboundEnv runs an app (1 MiB upload limit) with a running fake connector of alice.
func newInboundEnv(t *testing.T) *inboundEnv {
	t.Helper()
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()), testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	alice := env.NewUser("alice")
	on := true
	conn := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	inst := fake.Instance(t, conn.ID)
	inst.WaitRun(t)
	return &inboundEnv{env: env, alice: alice, conn: conn, inst: inst}
}

func (e *inboundEnv) emit(m connectors.Inbound) { e.inst.Emit(context.Background(), m) }

func (e *inboundEnv) chats(t *testing.T) []dto.Chat {
	t.Helper()
	return testkit.Decode[dto.List[dto.Chat]](e.alice.Get("/api/v1/connectors/" + e.conn.ID + "/chats").RequireStatus(http.StatusOK)).Items
}

func (e *inboundEnv) chatID(t *testing.T, external string) string {
	t.Helper()
	for _, c := range e.chats(t) {
		if c.ExternalID == external {
			return c.ID
		}
	}
	t.Fatalf("no chat %s", external)
	return ""
}

func (e *inboundEnv) messageCount(t *testing.T) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(e.env.App.Config.DataDir, app.DatabaseFile)+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	var n int
	require.NoError(t, raw.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM messages").Scan(&n))
	return n
}

// routedRoom creates a room of alice with the chat as a member.
func (e *inboundEnv) routedRoom(t *testing.T, name, chatID string) dto.Room {
	t.Helper()
	room := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: name}).RequireStatus(http.StatusCreated))
	e.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chatID}).RequireStatus(http.StatusCreated)
	return room
}

func (e *inboundEnv) messages(t *testing.T, roomID string) []dto.Message {
	t.Helper()
	return testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK)).Items
}

func text(chat, id, body string) connectors.Inbound {
	return connectors.Inbound{
		ChatID: chat, ChatTitle: "Support group", ChatKind: "group", MessageID: id,
		SenderID: senderID, SenderAltID: senderAlt, SenderName: "Customer", Text: body, At: time.Now(),
	}
}

// CON-07: a message from an unknown chat creates the chat and stores no message.
func TestInboundFromUnroutedChatCreatesChatOnly(t *testing.T) {
	e := newInboundEnv(t)
	live, _ := e.alice.DialLive("")
	before := e.messageCount(t)

	e.emit(text(groupID, "M1", "hello"))

	chats := e.chats(t)
	require.Len(t, chats, 1)
	require.Equal(t, groupID, chats[0].ExternalID)
	require.Equal(t, "Support group", chats[0].Title)
	require.Equal(t, dto.ChatKindGroup, chats[0].Kind)
	require.Equal(t, e.conn.ID, chats[0].ConnectorID)
	require.NotNil(t, chats[0].LastSeenAt)
	require.Equal(t, before, e.messageCount(t), "nothing stored for an unrouted chat")

	var discovered dto.Chat
	live.Expect(dto.LiveEventTypeChatDiscovered, &discovered, connectortest.Timeout)
	require.Equal(t, chats[0].ID, discovered.ID)
}

// CON-08, CON-10: a routed chat's message is stored once per room; a repeat is not stored.
func TestInboundFromRoutedChatIsStoredOncePerRoom(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	chat := e.chatID(t, groupID)
	a := e.routedRoom(t, "A", chat)
	b := e.routedRoom(t, "B", chat)
	before := e.messageCount(t)

	e.emit(text(groupID, "M2", "where is my order?"))
	e.emit(text(groupID, "M2", "where is my order?"))
	require.Equal(t, before+2, e.messageCount(t), "one per room, the repeat is dropped")

	for _, room := range []dto.Room{a, b} {
		msgs := e.messages(t, room.ID)
		require.Len(t, msgs, 1)
		m := msgs[0]
		require.Equal(t, dto.AuthorKindChat, m.AuthorKind)
		require.Equal(t, chat, m.AuthorID)
		require.Equal(t, senderID, m.SenderID)
		require.Equal(t, senderAlt, m.SenderAltID)
		require.Equal(t, "Customer", m.SenderName)
		require.Equal(t, "M2", m.ExternalID)
		require.Equal(t, "where is my order?", m.Text)
		require.Equal(t, dto.MessageKindText, m.Kind)
		require.Empty(t, m.AuthorUserID, "unverified sender")
	}
}

// CON-12: messages of the connector's own account are dropped.
func TestInboundDropsOwnMessages(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	e.routedRoom(t, "A", e.chatID(t, groupID))
	before := e.messageCount(t)

	own := text(groupID, "M2", "sent from the phone")
	own.FromSelf = true
	e.emit(own)
	own.ChatID, own.MessageID = otherID, "M3"
	e.emit(own)
	require.Equal(t, before, e.messageCount(t))
	require.Len(t, e.chats(t), 1, "no chat is created from own messages")
}

// CON-11: media within the upload limit is stored; larger media is recorded as too_large
// without a blob.
func TestInboundMedia(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	room := e.routedRoom(t, "A", e.chatID(t, groupID))

	small := []byte("synthetic image bytes")
	big := bytes.Repeat([]byte{1}, 1<<20+1)
	m := text(groupID, "M2", "photos")
	m.Kind = "image"
	m.Media = []connectors.Media{
		{Name: "small.png", Mime: "image/png", Open: opener(small)},
		{Name: "big.png", Mime: "image/png", Open: opener(big)},
		{Name: "declared.bin", Mime: "application/octet-stream", Size: 1<<20 + 1, Open: func(context.Context) (io.ReadCloser, error) {
			t.Error("media declared too large must not be downloaded")
			return io.NopCloser(bytes.NewReader(nil)), nil
		}},
	}
	e.emit(m)

	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, dto.MessageKindImage, msgs[0].Kind)
	atts := msgs[0].Attachments
	require.Len(t, atts, 3)
	require.Equal(t, dto.AttachmentStatusStored, atts[0].Status)
	require.Equal(t, int64(len(small)), atts[0].Size)
	got := e.alice.Get("/api/v1/attachments/" + atts[0].ID).RequireStatus(http.StatusOK)
	require.Equal(t, small, got.Body)

	require.Equal(t, dto.AttachmentStatusTooLarge, atts[1].Status)
	require.Equal(t, "big.png", atts[1].Name)
	e.alice.Get("/api/v1/attachments/"+atts[1].ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, dto.AttachmentStatusTooLarge, atts[2].Status)
	require.Equal(t, int64(1<<20+1), atts[2].Size)
	require.Equal(t, 1, blobCount(t, e.env), "only the small file is in the blob store")
}

func opener(data []byte) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
}

func blobCount(t *testing.T, env *testkit.Env) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(env.App.Config.DataDir, app.BlobsDir), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			n++
		}
		return err
	})
	require.NoError(t, err)
	return n
}

// IDN-02, IDN-03: "!link CODE" and "/link code" link the sender and are never routed;
// unknown codes link nothing and are not routed either. DLV-06: later messages of the
// linked sender resolve to the user.
func TestInboundPairingCommands(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	room := e.routedRoom(t, "A", e.chatID(t, groupID))
	before := e.messageCount(t)

	e.emit(text(groupID, "M2", "!link ZZZZZZ"))
	require.Empty(t, testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items)

	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", nil).RequireStatus(http.StatusCreated))
	e.emit(text(groupID, "M3", "  /link "+strings.ToLower(code.Code)+" "))
	require.Equal(t, before, e.messageCount(t), "pairing commands are not routed")

	ids := testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	require.Len(t, ids, 1)
	require.Equal(t, connectortest.KindName, ids[0].ConnectorKind)
	require.Equal(t, senderID, ids[0].ExternalID)
	require.Equal(t, senderAlt, ids[0].AltExternalID)

	code2 := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", nil).RequireStatus(http.StatusCreated))
	e.emit(text(groupID, "M4", "!link "+code2.Code))
	require.Equal(t, before, e.messageCount(t))

	e.emit(text(groupID, "M5", "it is me"))
	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, e.alice.User.ID, msgs[0].AuthorUserID, "a verified sender resolves to the user")
}

// A reply to an earlier chat message of the room points at it.
func TestInboundReplyPointsAtTheQuotedMessage(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	room := e.routedRoom(t, "A", e.chatID(t, groupID))
	e.emit(text(groupID, "M2", "question"))
	reply := text(groupID, "M3", "follow-up")
	reply.ReplyToID = "M2"
	e.emit(reply)

	msgs := e.messages(t, room.ID) // newest first
	require.Len(t, msgs, 2)
	require.Equal(t, msgs[1].ID, msgs[0].ReplyTo)
}
