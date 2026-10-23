package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func createRoom(c *testkit.Client, name string) dto.Room {
	c.T.Helper()
	return testkit.Decode[dto.Room](c.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: name}).RequireStatus(http.StatusCreated))
}

func shareRoom(c *testkit.Client, roomID string, users ...dto.SharingUser) {
	c.T.Helper()
	c.Put("/api/v1/rooms/"+roomID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: users}).RequireStatus(http.StatusOK)
}

// storeAgent inserts an agent row with its profile (the display name and handle are name).
func storeAgent(t *testing.T, env *testkit.Env, ownerID, name string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: id, OwnerID: ownerID, Name: name}); err != nil {
			return err
		}
		_, err := intelligences.Create(ctx, q, intelligences.NewParams{ID: id, Kind: handles.AI, DisplayName: name, HandleBase: name, Now: time.Now()})
		return err
	}))
	return id
}

// storeChat inserts a connector owned by ownerID with one chat and returns (connectorID, chatID).
func storeChat(t *testing.T, env *testkit.Env, ownerID, title string) (string, string) {
	t.Helper()
	conn, chat := store.NewID(), store.NewID()
	require.NoError(t, env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		if err := q.CreateConnector(context.Background(), db.CreateConnectorParams{ID: conn, OwnerID: ownerID, Kind: "telegram", Name: "tg", ConfigJson: "{}", Enabled: true}); err != nil {
			return err
		}
		return q.CreateChat(context.Background(), db.CreateChatParams{ID: chat, ConnectorID: conn, ExternalID: "-100" + chat[len(chat)-6:], Title: title, Kind: "group"})
	}))
	return conn, chat
}

type upload struct {
	name, mime, content string
}

// postMultipart posts a message with files as multipart/form-data.
func postMultipart(c *testkit.Client, roomID string, msg dto.PostMessageRequest, files ...upload) *testkit.Response {
	c.T.Helper()
	return postMultipartTo(c, "/api/v1/rooms/"+roomID+"/messages", msg, files...)
}

// postMultipartTo posts a message with files as multipart/form-data to path.
func postMultipartTo(c *testkit.Client, path string, msg dto.PostMessageRequest, files ...upload) *testkit.Response {
	c.T.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormField(dto.MultipartMessageField)
	require.NoError(c.T, err)
	require.NoError(c.T, json.NewEncoder(part).Encode(msg))
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, dto.MultipartFileField, f.name))
		h.Set("Content-Type", f.mime)
		w, err := mw.CreatePart(h)
		require.NoError(c.T, err)
		_, err = w.Write([]byte(f.content))
		require.NoError(c.T, err)
	}
	require.NoError(c.T, mw.Close())
	return c.DoRaw(http.MethodPost, path, mw.FormDataContentType(), &buf)
}

// roomSetup creates, as the owner, a room shared with the viewer (view) and the poster (post),
// with a chat member and a message carrying an attachment.
func roomSetup(t *testing.T, m *matrix) matrixResource {
	owner := m.as(t, roleOwner)
	r := createRoom(owner, "matrix")
	shareRoom(owner, r.ID,
		dto.SharingUser{UserID: m.clients[roleViewer].User.ID, Level: dto.LevelView},
		dto.SharingUser{UserID: m.clients[rolePoster].User.ID, Level: dto.LevelPost})
	_, chat := storeChat(t, m.env, owner.User.ID, "group")
	member := owner.SeedChatMember(r.ID, chat, true)
	msg := testkit.Decode[dto.Message](postMultipart(owner, r.ID, dto.PostMessageRequest{Text: "file"},
		upload{"a.txt", "text/plain", "hello"}).RequireStatus(http.StatusCreated))
	return matrixResource{"room": r.ID, "member": member.ID, "attachment": msg.Attachments[0].ID}
}

// Auth matrix for every room route (ACL-01..04, ACL-07, ACL-08, ROOM-01).
func TestRoomRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	room := func(res matrixResource, suffix string) string { return "/api/v1/rooms/" + res["room"] + suffix }
	routes := []matrixRoute{
		{"get room", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get(room(res, ""))
		}, statuses(200, 200, 200, 404, 404)},
		{"update room", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(room(res, ""), dto.UpdateRoomRequest{Name: ptr("renamed")})
		}, statuses(200, 403, 403, 404, 404)},
		{"delete room", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Delete(room(res, ""))
		}, statuses(204, 403, 403, 404, 404)},
		{"get sharing", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get(room(res, "/sharing"))
		}, statuses(200, 200, 200, 404, 404)},
		{"put sharing", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Put(room(res, "/sharing"), dto.Sharing{Mode: dto.VisibilityPrivate})
		}, statuses(200, 403, 403, 404, 404)},
		{"list members", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get(room(res, "/members"))
		}, statuses(200, 200, 200, 404, 404)},
		{"add member", func(_ *testing.T, m *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Post(room(res, "/members"), dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: m.clients[roleStranger].User.ID})
		}, statuses(201, 403, 403, 404, 404)},
		{"update member", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Patch(room(res, "/members/"+res["member"]), dto.UpdateMemberRequest{Outbound: ptr(false)})
		}, statuses(200, 403, 403, 404, 404)},
		{"remove member", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Delete(room(res, "/members/"+res["member"]))
		}, statuses(204, 403, 403, 404, 404)},
		{"list messages", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get(room(res, "/messages"))
		}, statuses(200, 200, 200, 404, 404)},
		{"post message", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Post(room(res, "/messages"), dto.PostMessageRequest{Text: "hello"})
		}, statuses(201, 403, 201, 404, 404)},
		{"post multipart message", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return postMultipart(c, res["room"], dto.PostMessageRequest{Text: "file"}, upload{"b.txt", "text/plain", "data"})
		}, statuses(201, 403, 201, 404, 404)},
		{"resume", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Post(room(res, "/resume"), nil)
		}, statuses(200, 403, 403, 404, 404)},
		{"download attachment", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return c.Get("/api/v1/attachments/" + res["attachment"])
		}, statuses(200, 200, 200, 404, 404)},
	}
	m.run(t, roomSetup, routes)

	// Listing: everyone gets 200, but only owner and grantees see the room.
	res := roomSetup(t, m)
	for _, role := range matrixRoles {
		list := testkit.Decode[dto.List[dto.Room]](m.clients[role].Get("/api/v1/rooms").RequireStatus(http.StatusOK))
		ids := []string{}
		for _, r := range list.Items {
			ids = append(ids, r.ID)
		}
		visible := role == roleOwner || role == roleViewer || role == rolePoster
		require.Equal(t, visible, contains(ids, res["room"]), string(role))
	}
}

func contains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }

// ACL-01: the creator owns the room with manage; invalid input is 422.
func TestCreateRoomEndpoint(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")

	r := createRoom(alice, "Ops")
	require.Equal(t, "Ops", r.Name)
	require.Equal(t, alice.User.ID, r.Owner.ID)
	require.Equal(t, "alice", r.Owner.Username)
	require.Equal(t, dto.LevelManage, r.MyLevel)
	require.Equal(t, dto.VisibilityPrivate, r.Visibility)
	require.Equal(t, 90, r.RetentionDays)
	require.Zero(t, r.LastSeq)
	require.False(t, r.AgentsPaused)

	e := alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "", RetentionDays: ptr(-1)}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "name")
	require.Contains(t, e.Fields, "retention_days")
	env.Anon().Get("/api/v1/rooms").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	got := testkit.Decode[dto.Room](alice.Patch("/api/v1/rooms/"+r.ID, dto.UpdateRoomRequest{BridgeChats: ptr(true), RetentionDays: ptr(0)}).RequireStatus(http.StatusOK))
	require.True(t, got.BridgeChats)
	require.Zero(t, got.RetentionDays)
	require.Equal(t, "Ops", got.Name)
}

// ACL-07 over HTTP: presets replace grants; ACL-04: view cannot post, public post can.
func TestRoomSharingEndpoint(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	r := createRoom(alice, "ops")
	path := "/api/v1/rooms/" + r.ID

	shareRoom(alice, r.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelView})
	s := testkit.Decode[dto.Sharing](bob.Get(path + "/sharing").RequireStatus(http.StatusOK))
	require.Equal(t, dto.VisibilityShared, s.Mode)
	require.Equal(t, []dto.SharingUser{{UserID: bob.User.ID, Level: dto.LevelView, Username: "bob", DisplayName: "bob"}}, s.Users,
		"an account that chose no name goes by its username")
	require.Equal(t, dto.LevelView, testkit.Decode[dto.Room](bob.Get(path).RequireStatus(http.StatusOK)).MyLevel)
	bob.Post(path+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	carol.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	s = testkit.Decode[dto.Sharing](alice.Put(path+"/sharing", dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost, Users: []dto.SharingUser{}}, s)
	carol.Post(path+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireStatus(http.StatusCreated)
	require.Equal(t, dto.VisibilityPublic, testkit.Decode[dto.Room](carol.Get(path).RequireStatus(http.StatusOK)).Visibility)

	alice.Put(path+"/sharing", dto.Sharing{Mode: dto.VisibilityPrivate}).RequireStatus(http.StatusOK)
	bob.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	carol.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	e := alice.Put(path+"/sharing", dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelManage}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "public_level")
}

// ACL-05, ROOM-01 over HTTP. A chat is no member whoever owns its connection (CID-19; the chat
// branch of ACL-06 is retired with it): the room's outbound delivery to a chat that a room
// built before the connection proxy still holds is planted.
func TestRoomMembersEndpoint(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	mine := storeAgent(t, env, alice.User.ID, "builder")
	bobs := storeAgent(t, env, bob.User.ID, "reviewer")
	_, chat := storeChat(t, env, alice.User.ID, "family")
	_, bobChat := storeChat(t, env, bob.User.ID, "team")
	r := createRoom(alice, "ops")
	path := "/api/v1/rooms/" + r.ID + "/members"

	agent := testkit.Decode[dto.RoomMember](alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: mine}).RequireStatus(http.StatusCreated))
	require.Equal(t, dto.MemberKindAgent, agent.Kind)
	require.Equal(t, "builder", agent.Name)
	require.Equal(t, "alice", agent.Owner)
	alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: bobs}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: bobChat}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeChatsNotMembers)
	alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: mine}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	alice.Post(path, dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chat}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeChatsNotMembers)
	member := alice.SeedChatMember(r.ID, chat, true)
	require.True(t, member.Outbound)
	require.Equal(t, "telegram", member.ConnectorKind)
	require.Equal(t, dto.ChatKindGroup, member.ChatKind)
	msg := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+r.ID+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireStatus(http.StatusCreated))
	require.Len(t, msg.Outbox, 1)
	require.Equal(t, chat, msg.Outbox[0].ChatID)
	require.Equal(t, dto.OutboxStatusPending, msg.Outbox[0].Status)

	got := testkit.Decode[dto.RoomMember](alice.Patch(path+"/"+member.ID, dto.UpdateMemberRequest{Outbound: ptr(false)}).RequireStatus(http.StatusOK))
	require.False(t, got.Outbound)
	list := testkit.Decode[dto.List[dto.RoomMember]](alice.Get(path).RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 2)

	// member_count on the room, in get and list.
	memberCount := func() (int, int) {
		got := testkit.Decode[dto.Room](alice.Get("/api/v1/rooms/" + r.ID).RequireStatus(http.StatusOK))
		rooms := testkit.Decode[dto.List[dto.Room]](alice.Get("/api/v1/rooms").RequireStatus(http.StatusOK))
		require.Len(t, rooms.Items, 1)
		return got.MemberCount, rooms.Items[0].MemberCount
	}
	get, listed := memberCount()
	require.Equal(t, 2, get)
	require.Equal(t, 2, listed)

	alice.Delete(path + "/" + member.ID).RequireStatus(http.StatusNoContent)
	get, listed = memberCount()
	require.Equal(t, 1, get)
	require.Equal(t, 1, listed)
	alice.Delete(path+"/"+member.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	msg = testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+r.ID+"/messages", dto.PostMessageRequest{Text: "after", Recipients: nil}).RequireStatus(http.StatusCreated))
	require.Empty(t, msg.Outbox, "a removed chat gets no delivery")
	alice.Post("/api/v1/rooms/"+r.ID+"/messages", dto.PostMessageRequest{Text: "x", Recipients: []string{chat}}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
}

// MSG-01, MSG-02 and pagination over HTTP.
func TestRoomMessagesEndpoint(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	r := createRoom(alice, "ops")
	path := "/api/v1/rooms/" + r.ID + "/messages"

	for i := 1; i <= 5; i++ {
		m := testkit.Decode[dto.Message](alice.Post(path, dto.PostMessageRequest{Text: fmt.Sprintf("m%d", i)}).RequireStatus(http.StatusCreated))
		require.Equal(t, int64(i), m.Seq)
		require.Equal(t, dto.AuthorKindUser, m.AuthorKind)
		require.Equal(t, alice.User.ID, m.AuthorID)
		require.Equal(t, alice.User.ID, m.AuthorUserID)
		require.Equal(t, "alice", m.AuthorName)
		require.Equal(t, dto.MessageKindText, m.Kind)
		require.NotNil(t, m.Attachments)
	}
	for _, text := range []string{"", "   ", strings.Repeat("x", dto.MaxTextLength+1)} {
		e := alice.Post(path, dto.PostMessageRequest{Text: text}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "text")
	}
	alice.Post(path, dto.PostMessageRequest{Text: strings.Repeat("x", dto.MaxTextLength)}).RequireStatus(http.StatusCreated)

	page := testkit.Decode[dto.Page[dto.Message]](alice.Get(path + "?limit=2").RequireStatus(http.StatusOK))
	require.True(t, page.HasMore)
	require.Equal(t, []int64{6, 5}, seqs(page.Items))
	page = testkit.Decode[dto.Page[dto.Message]](alice.Get(path + "?limit=2&before_seq=5").RequireStatus(http.StatusOK))
	require.Equal(t, []int64{4, 3}, seqs(page.Items))
	page = testkit.Decode[dto.Page[dto.Message]](alice.Get(path + "?after_seq=4").RequireStatus(http.StatusOK))
	require.False(t, page.HasMore)
	require.Equal(t, []int64{5, 6}, seqs(page.Items))
	alice.Get(path+"?limit=0").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)

	require.Equal(t, int64(6), testkit.Decode[dto.Room](alice.Get("/api/v1/rooms/"+r.ID).RequireStatus(http.StatusOK)).LastSeq)
}

func seqs(list []dto.Message) []int64 {
	out := []int64{}
	for _, m := range list {
		out = append(out, m.Seq)
	}
	return out
}

// SEC-03: multipart uploads over the limit are 413; attachments download only from visible
// rooms; safe images display inline, everything else downloads.
func TestMultipartUploadAndDownload(t *testing.T) {
	env := testkit.New(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	r := createRoom(alice, "ops")
	shareRoom(alice, r.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelView})

	png := "\x89PNG\r\n\x1a\nfake"
	msg := testkit.Decode[dto.Message](postMultipart(alice, r.ID, dto.PostMessageRequest{Text: "files"},
		upload{"pic.png", "image/png", png}, upload{"page.html", "text/html", "<script>alert(1)</script>"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "files", msg.Text)
	require.Len(t, msg.Attachments, 2)
	require.Equal(t, dto.Attachment{ID: msg.Attachments[0].ID, Name: "pic.png", Mime: "image/png", Size: int64(len(png)), Status: dto.AttachmentStatusStored}, msg.Attachments[0])

	img := bob.Get("/api/v1/attachments/" + msg.Attachments[0].ID).RequireStatus(http.StatusOK)
	require.Equal(t, png, string(img.Body))
	require.Equal(t, "image/png", img.Header.Get("Content-Type"))
	require.Equal(t, `inline; filename=pic.png`, img.Header.Get("Content-Disposition"))
	require.Equal(t, "nosniff", img.Header.Get("X-Content-Type-Options"))

	html := bob.Get("/api/v1/attachments/" + msg.Attachments[1].ID).RequireStatus(http.StatusOK)
	require.Equal(t, "application/octet-stream", html.Header.Get("Content-Type"))
	require.Equal(t, `attachment; filename=page.html`, html.Header.Get("Content-Disposition"))

	carol.Get("/api/v1/attachments/"+msg.Attachments[0].ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Get("/api/v1/attachments/"+store.NewID()).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Only a file: no text needed (MSG-02).
	postMultipart(alice, r.ID, dto.PostMessageRequest{}, upload{"a.txt", "text/plain", "x"}).RequireStatus(http.StatusCreated)
	postMultipart(alice, r.ID, dto.PostMessageRequest{}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)

	big := strings.Repeat("b", 1<<20+1)
	postMultipart(alice, r.ID, dto.PostMessageRequest{Text: "big"}, upload{"big.bin", "application/octet-stream", big}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	page := testkit.Decode[dto.Page[dto.Message]](alice.Get("/api/v1/rooms/" + r.ID + "/messages").RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 2, "the oversized upload stored no message")
	postMultipart(bob, r.ID, dto.PostMessageRequest{Text: "nope"}, upload{"a.txt", "text/plain", "x"}).
		RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

// DLV-09 over HTTP: a manager resumes a room paused by the loop guard.
func TestResumeEndpoint(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	agentID := storeAgent(t, env, alice.User.ID, "looper")
	r := createRoom(alice, "ops")
	alice.Post("/api/v1/rooms/"+r.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)
	agent := authn.Principal{Kind: authn.KindAgent, UserID: alice.User.ID, AgentID: agentID}
	for i := 0; i < 25; i++ {
		_, err := env.App.Rooms.Post(ctx, agent, r.ID, rooms.PostInput{Text: "loop"})
		require.NoError(t, err)
	}
	got := testkit.Decode[dto.Room](alice.Get("/api/v1/rooms/" + r.ID).RequireStatus(http.StatusOK))
	require.True(t, got.AgentsPaused)
	require.Equal(t, int64(26), got.LastSeq)
	last := testkit.Decode[dto.Page[dto.Message]](alice.Get("/api/v1/rooms/" + r.ID + "/messages?limit=1").RequireStatus(http.StatusOK))
	require.Equal(t, dto.AuthorKindSystem, last.Items[0].AuthorKind)
	require.Equal(t, "Partyline", last.Items[0].AuthorName)

	got = testkit.Decode[dto.Room](alice.Post("/api/v1/rooms/"+r.ID+"/resume", nil).RequireStatus(http.StatusOK))
	require.False(t, got.AgentsPaused)
}
