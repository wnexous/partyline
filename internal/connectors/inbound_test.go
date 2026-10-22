package connectors_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
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
	fake  *connectortest.Fake
	env   *testkit.Env
	alice *testkit.Client
	conn  dto.Connector
	inst  *connectortest.Instance
}

// newInboundEnv runs an app (1 MiB upload limit) with a running fake connector of alice.
func newInboundEnv(t *testing.T, opts ...testkit.Option) *inboundEnv {
	t.Helper()
	fake := connectortest.New()
	opts = append(opts, testkit.WithConnectorKinds(fake.Kind()), testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	env := testkit.New(t, opts...)
	alice := env.NewUser("alice")
	on := true
	conn := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	inst := fake.Instance(t, conn.ID)
	inst.WaitRun(t)
	return &inboundEnv{fake: fake, env: env, alice: alice, conn: conn, inst: inst}
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

// CON-12, as refined by ACC-05: a message of the connector's own account is not a message of a
// chat: it is dropped unless its chat is in rooms, where it is the operator's (selfmsg_test.go),
// and it never creates a chat.
func TestInboundDropsOwnMessagesOfChatsInNoRoom(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	before := e.messageCount(t)

	typed := text(groupID, "M2", "sent from the phone")
	typed.FromSelf = true
	e.emit(typed)
	typed.ChatID, typed.MessageID = otherID, "M3"
	e.emit(typed)
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

// CON-11, S8: a download the platform client cuts off at the upload limit is recorded as
// too_large, not unavailable.
func TestInboundMediaCutOffByTheClientCapIsTooLarge(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	room := e.routedRoom(t, "A", e.chatID(t, groupID))
	m := text(groupID, "M2", "video")
	m.Media = []connectors.Media{{Name: "clip.mp4", Mime: "video/mp4", Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(io.MultiReader(bytes.NewReader([]byte("partial")), errReader{fmt.Errorf("file download: %w", safehttp.ErrResponseTooLarge)})), nil
	}}}
	e.emit(m)

	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Attachments, 1)
	require.Equal(t, dto.AttachmentStatusTooLarge, msgs[0].Attachments[0].Status)
	require.Zero(t, blobCount(t, e.env))
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// CON-10, CON-11: a repeated message is not stored again and its media is not downloaded
// again.
func TestInboundRepeatDownloadsNothing(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	room := e.routedRoom(t, "A", e.chatID(t, groupID))

	var opens atomic.Int32
	m := text(groupID, "M2", "photo")
	m.Kind = "image"
	m.Media = []connectors.Media{{Name: "p.png", Mime: "image/png", Open: func(ctx context.Context) (io.ReadCloser, error) {
		opens.Add(1)
		return opener([]byte("synthetic image bytes"))(ctx)
	}}}
	e.emit(m)
	e.emit(m)

	require.Equal(t, int32(1), opens.Load(), "the repeat opened no media")
	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Attachments, 1)
	require.Equal(t, 1, blobCount(t, e.env))
}

// CON-14: inbound text over 16,000 characters (code points) is stored cut to 16,000,
// ending with the truncation marker; text at the limit is stored as is.
func TestInboundLongTextIsTruncated(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "first"))
	room := e.routedRoom(t, "A", e.chatID(t, groupID))

	long := strings.Repeat("é", 20000)
	e.emit(text(groupID, "M2", long))
	exact := strings.Repeat("a", dto.MaxTextLength)
	e.emit(text(groupID, "M3", exact))
	// 20,000 bytes but 10,000 code points: the limit counts code points, not bytes.
	multiByte := strings.Repeat("é", 10000)
	e.emit(text(groupID, "M4", multiByte))

	msgs := e.messages(t, room.ID) // newest first
	require.Len(t, msgs, 3)
	require.Equal(t, multiByte, msgs[0].Text, "CON-14: under 16,000 code points is stored unchanged")
	require.Equal(t, exact, msgs[1].Text)
	got := msgs[2].Text
	require.Equal(t, 16000, utf8.RuneCountInString(got))
	require.Equal(t, strings.Repeat("é", 16000-utf8.RuneCountInString("…[truncated]"))+"…[truncated]", got)
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

	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	e.emit(text(groupID, "M3", "  /link "+strings.ToLower(code.Code)+" "))
	require.Equal(t, before, e.messageCount(t), "pairing commands are not routed")

	ids := testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	require.Len(t, ids, 1)
	require.Equal(t, e.conn.ID, ids[0].ConnectorID)
	require.Equal(t, connectortest.KindName, ids[0].ConnectorKind)
	require.Equal(t, senderID, ids[0].ExternalID)
	require.Equal(t, senderAlt, ids[0].AltExternalID)

	code2 := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	e.emit(text(groupID, "M4", "!link "+code2.Code))
	require.Equal(t, before, e.messageCount(t))

	e.emit(text(groupID, "M5", "it is me"))
	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, e.alice.User.ID, msgs[0].AuthorUserID, "a verified sender resolves to the user")
}

// IDN-02: a linked sender is trusted only on the connector it was linked through; the same
// sender id arriving through another connector of the same kind resolves to no user.
func TestInboundIdentityIsScopedToItsConnector(t *testing.T) {
	e := newInboundEnv(t)
	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	e.emit(text(groupID, "M1", "!link "+code.Code))
	require.Len(t, testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items, 1)

	on := true
	other := testkit.Decode[dto.Connector](e.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "second", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	inst := e.fake.Instance(t, other.ID)
	inst.WaitRun(t)
	inst.Emit(context.Background(), text(otherID, "M2", "first"))
	var chat string
	for _, c := range testkit.Decode[dto.List[dto.Chat]](e.alice.Get("/api/v1/connectors/" + other.ID + "/chats").RequireStatus(http.StatusOK)).Items {
		chat = c.ID
	}
	room := e.routedRoom(t, "B", chat)
	inst.Emit(context.Background(), text(otherID, "M3", "same sender, other connector"))
	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Empty(t, msgs[0].AuthorUserID)
}

// IDN-02 (validation probe S5): alice's code typed through mallory's connector, which listens
// in the same chat, links nothing there, is not routed, and still links through alice's own
// connector.
func TestInboundPairingCodeOnlyLinksOnItsConnector(t *testing.T) {
	e := newInboundEnv(t)
	mallory := e.env.NewUser("mallory")
	on := true
	mconn := testkit.Decode[dto.Connector](mallory.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "listener", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	minst := e.fake.Instance(t, mconn.ID)
	minst.WaitRun(t)
	minst.Emit(context.Background(), text(groupID, "M1", "first"))
	var mchat string
	for _, c := range testkit.Decode[dto.List[dto.Chat]](mallory.Get("/api/v1/connectors/" + mconn.ID + "/chats").RequireStatus(http.StatusOK)).Items {
		mchat = c.ID
	}
	mroom := testkit.Decode[dto.Room](mallory.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "M"}).RequireStatus(http.StatusCreated))
	mallory.Post("/api/v1/rooms/"+mroom.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: mchat}).RequireStatus(http.StatusCreated)

	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	minst.Emit(context.Background(), text(groupID, "M2", "!link "+code.Code))
	require.Empty(t, testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items,
		"mallory's connector linked nothing")
	require.Empty(t, testkit.Decode[dto.Page[dto.Message]](mallory.Get("/api/v1/rooms/"+mroom.ID+"/messages").RequireStatus(http.StatusOK)).Items,
		"the command is not routed")

	e.emit(text(groupID, "M3", "!link "+code.Code))
	ids := testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	require.Len(t, ids, 1)
	require.Equal(t, e.conn.ID, ids[0].ConnectorID)
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

// TG-01 edge case: a migrated chat keeps its record and rooms under the new platform id;
// a migration onto an existing chat changes nothing.
func TestMigrateChatKeepsRooms(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text("-4000000001", "M1", "before"))
	chat := e.chatID(t, "-4000000001")
	room := e.routedRoom(t, "A", chat)

	e.inst.MigrateChat(context.Background(), "-4000000001", "-1001000000001")
	require.Equal(t, chat, e.chatID(t, "-1001000000001"), "same chat, new external id")
	require.Len(t, e.chats(t), 1)

	e.emit(text("-1001000000001", "M2", "after"))
	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, "after", msgs[0].Text, "messages at the new id reach the chat's rooms")

	e.emit(text(otherID, "M3", "x"))
	e.inst.MigrateChat(context.Background(), "-1001000000001", otherID)
	require.Equal(t, chat, e.chatID(t, "-1001000000001"), "the new id was already a chat: nothing moves")
}

// CTX-08: a changed title on a later inbound message updates the stored chat (and its member
// name in rooms); an empty title keeps the stored one.
func TestInboundTitleChangeUpdatesTheChat(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M1", "hello"))
	room := e.routedRoom(t, "A", e.chatID(t, groupID))

	renamed := text(groupID, "M2", "again")
	renamed.ChatTitle = "Support group (renamed)"
	e.emit(renamed)
	require.Equal(t, "Support group (renamed)", e.chats(t)[0].Title)
	members := testkit.Decode[dto.List[dto.RoomMember]](e.alice.Get("/api/v1/rooms/" + room.ID + "/members").RequireStatus(http.StatusOK)).Items
	require.Equal(t, "Support group (renamed)", members[0].Name)

	untitled := text(groupID, "M3", "no title this time")
	untitled.ChatTitle = ""
	e.emit(untitled)
	require.Equal(t, "Support group (renamed)", e.chats(t)[0].Title)
}
