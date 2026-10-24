package api_test

import (
	"bytes"
	"context"
	"database/sql"
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
	"github.com/wnexous/partyline/internal/orgs"
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

// storeAgent inserts an agent row with its profile (the display name and handle are name), placed as the app
// places a new AI: in its owner's default organization, which is its home, as a member the owner created.
func storeAgent(t *testing.T, env *testkit.Env, ownerID, name string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		orgID, err := orgs.DefaultFor(ctx, q, ownerID)
		if err != nil {
			return err
		}
		if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: id, OwnerID: ownerID, Name: name}); err != nil {
			return err
		}
		if _, err := intelligences.Create(ctx, q, intelligences.NewParams{ID: id, Kind: handles.AI, DisplayName: name, HandleBase: name, Now: time.Now()}); err != nil {
			return err
		}
		if err := q.SetAgentHome(ctx, db.SetAgentHomeParams{OrgID: sql.NullString{String: orgID, Valid: true}, ID: id}); err != nil {
			return err
		}
		if err := q.SetAgentCreator(ctx, db.SetAgentCreatorParams{CreatorIid: sql.NullString{String: ownerID, Valid: true}, ID: id}); err != nil {
			return err
		}
		return orgs.AddMember(ctx, q, orgs.AddMemberInput{OrgID: orgID, IID: id, Permission: orgs.PermissionMember, Now: time.Now()})
	}))
	return id
}

// storeChat inserts a connector created by ownerID with one chat and returns (connectorID, chatID). The
// connector belongs to the creator's default organization, as the upgrade places the connections it finds.
func storeChat(t *testing.T, env *testkit.Env, ownerID, title string) (string, string) {
	t.Helper()
	conn, chat := store.NewID(), store.NewID()
	require.NoError(t, env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		orgID, err := orgs.DefaultFor(context.Background(), q, ownerID)
		if err != nil {
			return err
		}
		if err := q.CreateConnector(context.Background(), db.CreateConnectorParams{
			ID: conn, OwnerID: ownerID, OrgID: sql.NullString{String: orgID, Valid: orgID != ""}, Kind: "telegram", Name: "tg", ConfigJson: "{}", Enabled: true,
		}); err != nil {
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

// roomSetup returns the setup of the group matrices: a fresh group of the organization created by its
// creator (the role "member" of roles), with the roles "target" and "guest" in it as room members, a
// chat member, an AI of the creator and a message carrying an attachment. It returns the ids the routes
// use: "room", "chat" (the member row of the chat), "ai" and "user" (the member rows of the AI and of
// the role "target") and "attachment".
func roomSetup(env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	return func(t *testing.T) map[string]string {
		creator := *roles.Member
		creator.T = t
		r := groupIn(t, env, &creator, roles.Org.ID, "matrix")
		target := addToRoom(t, env, &creator, r.ID, roles.Target)
		addToRoom(t, env, &creator, r.ID, roles.Guest)
		_, chat := storeChat(t, env, creator.User.ID, "group")
		member := creator.SeedChatMember(r.ID, chat)
		ai := testkit.Decode[dto.RoomMember](creator.Post("/api/v1/rooms/"+r.ID+"/members",
			dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: env.NewAIIn(&creator, roles.Org.ID, "builder")}).RequireStatus(http.StatusCreated))
		msg := testkit.Decode[dto.Message](postMultipart(&creator, r.ID, dto.PostMessageRequest{Text: "file"},
			upload{"a.txt", "text/plain", "hello"}).RequireStatus(http.StatusCreated))
		return map[string]string{"room": r.ID, "chat": member.ID, "ai": ai.ID, "user": target.ID, "attachment": msg.Attachments[0].ID}
	}
}

// Auth matrix for every room route (ACL-01..04, ACL-07, ACL-08, ROOM-01) over the organization roles
// (AD-016): the creator and the admins of the organization manage a group, the people in the room post, and
// everybody else - a manager above the creator, a peer, an inviter, an outsider, an instance admin - gets 404.
func TestRoomRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	room := func(res map[string]string, suffix string) string { return "/api/v1/rooms/" + res["room"] + suffix }
	routes := []roleRoute{
		{"get room", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(room(res, ""))
		}, groupWant(200, 200, 200, 404)},
		{"update room", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(room(res, ""), dto.UpdateRoomRequest{Name: ptr("renamed")})
		}, groupWant(200, 200, 403, 404)},
		{"delete room", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Delete(room(res, ""))
		}, groupWant(204, 204, 403, 404)},
		{"get sharing", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(room(res, "/sharing"))
		}, groupWant(200, 200, 200, 404)},
		{"put sharing", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Put(room(res, "/sharing"), dto.Sharing{Mode: dto.VisibilityPrivate})
		}, groupWant(200, 200, 403, 404)},
		{"list members", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(room(res, "/members"))
		}, groupWant(200, 200, 200, 404)},
		{"add member", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(room(res, "/members"), dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: roles.Peer.User.ID})
		}, groupWant(201, 201, 403, 404)},
		{"update member", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Patch(room(res, "/members/"+res["ai"]), dto.UpdateMemberRequest{DisplayName: ptr("Builder bot")})
		}, groupWant(200, 200, 403, 404)},
		{"remove member", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Delete(room(res, "/members/"+res["user"]))
		}, groupWant(204, 204, 403, 404)},
		{"remove a chat member", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Delete(room(res, "/members/"+res["chat"]))
		}, groupWant(204, 204, 403, 404)},
		{"list messages", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get(room(res, "/messages"))
		}, groupWant(200, 200, 200, 404)},
		{"post message", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(room(res, "/messages"), dto.PostMessageRequest{Text: "hello"})
		}, groupWant(201, 201, 201, 404)},
		{"post multipart message", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return postMultipart(c, res["room"], dto.PostMessageRequest{Text: "file"}, upload{"b.txt", "text/plain", "data"})
		}, groupWant(201, 201, 201, 404)},
		{"resume", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Post(room(res, "/resume"), nil)
		}, groupWant(200, 200, 403, 404)},
		{"download attachment", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get("/api/v1/attachments/" + res["attachment"])
		}, groupWant(200, 200, 200, 404)},
		{"mark read", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Put(room(res, "/read"), dto.MarkReadRequest{})
		}, groupWant(200, 200, 200, 404)},
	}
	runRoleRoutes(t, roles, roomSetup(env, roles), routes)

	// Listing: everyone gets 200, but only the people who can view the group list it.
	res := roomSetup(env, roles)(t)
	for _, role := range roles.All() {
		list := testkit.Decode[dto.List[dto.Room]](role.Client.Get("/api/v1/rooms").RequireStatus(http.StatusOK))
		ids := []string{}
		for _, r := range list.Items {
			ids = append(ids, r.ID)
		}
		visible := role.Name == "member" || role.Name == "admin" || role.Name == "target" || role.Name == "guest"
		require.Equal(t, visible, contains(ids, res["room"]), role.Name)
	}

	// The tools of an AI reach agent routes only: its token is no way into the user routes of a group.
	ai := roles.Manager.Token(dto.TokenKindAgent, "tools").AsAgent("acme-ai")
	ai.Get("/api/v1/rooms/"+res["room"]).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	ai.Patch("/api/v1/rooms/"+res["room"], dto.UpdateRoomRequest{Name: ptr("renamed")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
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

	got := testkit.Decode[dto.Room](alice.Patch("/api/v1/rooms/"+r.ID, dto.UpdateRoomRequest{RetentionDays: ptr(0)}).RequireStatus(http.StatusOK))
	require.Zero(t, got.RetentionDays)
	require.Equal(t, "Ops", got.Name)
}

// ACL-07 over HTTP (inert since AD-016): a visibility preset still replaces the room's grants and reads back
// as a preset, but a grant gives no access to a group: the user it names and everybody, for a public preset,
// still get 404.
func TestRoomSharingGivesNoAccess(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	r := createRoom(alice, "ops")
	path := "/api/v1/rooms/" + r.ID

	shareRoom(alice, r.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelView})
	s := testkit.Decode[dto.Sharing](alice.Get(path + "/sharing").RequireStatus(http.StatusOK))
	require.Equal(t, dto.VisibilityShared, s.Mode)
	require.Equal(t, []dto.SharingUser{{UserID: bob.User.ID, Level: dto.LevelView, Username: "bob", DisplayName: "bob"}}, s.Users,
		"an account that chose no name goes by its username")
	bob.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	bob.Get(path+"/sharing").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	carol.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, testkit.Decode[dto.List[dto.Room]](bob.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items)

	s = testkit.Decode[dto.Sharing](alice.Put(path+"/sharing", dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelPost, Users: []dto.SharingUser{}}, s)
	carol.Post(path+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	carol.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, dto.VisibilityPublic, testkit.Decode[dto.Room](alice.Get(path).RequireStatus(http.StatusOK)).Visibility)

	alice.Put(path+"/sharing", dto.Sharing{Mode: dto.VisibilityPrivate}).RequireStatus(http.StatusOK)
	bob.Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	e := alice.Put(path+"/sharing", dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelManage}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "public_level")
}

// ACL-05 over HTTP. A chat is no member whoever owns its connection (CID-19; the chat branch of
// ACL-06 is retired with it). A chat that a room built before the connection proxy still holds is
// planted: it is listed, updated and removed, and the room sends it nothing (CID-22).
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
	member := alice.SeedChatMember(r.ID, chat)
	require.Equal(t, "telegram", member.ConnectorKind)
	require.Equal(t, dto.ChatKindGroup, member.ChatKind)
	alice.Post("/api/v1/rooms/"+r.ID+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireStatus(http.StatusCreated)

	got := testkit.Decode[dto.RoomMember](alice.Patch(path+"/"+agent.ID, dto.UpdateMemberRequest{DisplayName: ptr("Builder bot")}).RequireStatus(http.StatusOK))
	require.Equal(t, "Builder bot", got.DisplayName)
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
	alice.Post("/api/v1/rooms/"+r.ID+"/messages", dto.PostMessageRequest{Text: "after"}).RequireStatus(http.StatusCreated)
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
	alice, bob, carol, dave := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol"), env.NewUser("dave")
	r := createRoom(alice, "ops")
	addToRoom(t, env, alice, r.ID, bob)
	joinOrgOf(t, env, r.ID, dave, orgs.PermissionMember) // of the organization, not of the room

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
	dave.Get("/api/v1/attachments/"+msg.Attachments[0].ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Get("/api/v1/attachments/"+store.NewID()).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Only a file: no text needed (MSG-02).
	postMultipart(alice, r.ID, dto.PostMessageRequest{}, upload{"a.txt", "text/plain", "x"}).RequireStatus(http.StatusCreated)
	postMultipart(alice, r.ID, dto.PostMessageRequest{}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)

	big := strings.Repeat("b", 1<<20+1)
	postMultipart(alice, r.ID, dto.PostMessageRequest{Text: "big"}, upload{"big.bin", "application/octet-stream", big}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	page := testkit.Decode[dto.Page[dto.Message]](alice.Get("/api/v1/rooms/" + r.ID + "/messages").RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 2, "the oversized upload stored no message")
	postMultipart(dave, r.ID, dto.PostMessageRequest{Text: "nope"}, upload{"a.txt", "text/plain", "x"}).
		RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	postMultipart(bob, r.ID, dto.PostMessageRequest{Text: "a room member posts"}, upload{"b.txt", "text/plain", "x"}).
		RequireStatus(http.StatusCreated)
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
