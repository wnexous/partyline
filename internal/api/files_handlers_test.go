package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/testkit"
)

func roomFiles(c *testkit.Client, roomID, query string) dto.Page[dto.RoomFile] {
	c.T.Helper()
	return testkit.Decode[dto.Page[dto.RoomFile]](c.Get("/api/v1/rooms/" + roomID + "/attachments" + query).RequireStatus(http.StatusOK))
}

func fileNames(files []dto.RoomFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

// PROF-09: a file names its author as a message does, so the list can put the kind badge next
// to the name: a user, the AI that wrote it, and a chat sender that resolved to nobody has no
// intelligence, only the name that shows.
func TestRoomFilesCarryTheirAuthor(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	atlas := env.NewAI(alice, "atlas")
	r := createRoom(alice, "ops")
	alice.Post("/api/v1/rooms/"+r.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: atlas}).RequireStatus(http.StatusCreated)
	ctx := context.Background()

	postMultipart(alice, r.ID, dto.PostMessageRequest{Text: "from alice"}, upload{"alice.pdf", "application/pdf", "pdf"}).RequireStatus(http.StatusCreated)
	_, err := env.App.Rooms.Post(ctx, authn.Principal{Kind: authn.KindAgent, UserID: alice.User.ID, AgentID: atlas}, r.ID, rooms.PostInput{
		Text: "from atlas", Files: []rooms.File{{Name: "atlas.log", Mime: "text/plain", Status: dto.AttachmentStatusUnavailable}},
	})
	require.NoError(t, err)
	_, chat := storeChat(t, env, alice.User.ID, "Customers")
	alice.SeedChatMember(r.ID, chat)
	_, err = env.App.Rooms.PostFromChat(ctx, r.ID, rooms.ChatMessage{
		ChatID: chat, SenderID: "100000001", SenderName: "Customer", Text: "photo",
		Files: []rooms.File{{Name: "lost.jpg", Mime: "image/jpeg", Status: dto.AttachmentStatusUnavailable}},
	})
	require.NoError(t, err)

	authors := map[string]dto.IntelligenceRef{}
	for _, f := range roomFiles(alice, r.ID, "").Items {
		authors[f.Name] = f.Author
	}
	require.Equal(t, map[string]dto.IntelligenceRef{
		"alice.pdf": {ID: alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "alice", Handle: "alice"},
		"atlas.log": {ID: atlas, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "atlas", Handle: "atlas"},
		"lost.jpg":  {DisplayName: "Customer"},
	}, authors)
}

// FILES-01, FILES-03, FILES-06: a room member gets every file of the room newest first with its
// message, author and kind; kind filters by MIME family (document = everything else); files
// a connector could not store are listed with their status; other rooms' files are not.
func TestRoomFilesList(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	r := createRoom(alice, "ops")
	addToRoom(t, env, alice, r.ID, bob)
	other := createRoom(alice, "elsewhere")
	postMultipart(alice, other.ID, dto.PostMessageRequest{Text: "not here"}, upload{"other.pdf", "application/pdf", "x"}).RequireStatus(http.StatusCreated)

	first := testkit.Decode[dto.Message](postMultipart(alice, r.ID, dto.PostMessageRequest{Text: "media"},
		upload{"pic.png", "image/png", "png-bytes"}, upload{"voice.ogg", "audio/ogg", "ogg"}).RequireStatus(http.StatusCreated))
	second := testkit.Decode[dto.Message](postMultipart(alice, r.ID, dto.PostMessageRequest{Text: "docs"},
		upload{"clip.mp4", "video/mp4", "mp4"}, upload{"report.pdf", "application/pdf", "pdf-bytes"},
		upload{"notes.txt", "text/plain", "txt"}, upload{"SHOUT.JPG", "IMAGE/JPEG", "jpg"}).RequireStatus(http.StatusCreated))

	_, chat := storeChat(t, env, alice.User.ID, "Customers")
	alice.SeedChatMember(r.ID, chat)
	fromChat, err := env.App.Rooms.PostFromChat(context.Background(), r.ID, rooms.ChatMessage{
		ChatID: chat, SenderID: "100000001", SenderName: "Customer", Text: "big video",
		Files: []rooms.File{
			{Name: "huge.mov", Mime: "video/quicktime", Size: 900 << 20, Status: dto.AttachmentStatusTooLarge},
			{Name: "lost.jpg", Mime: "image/jpeg", Status: dto.AttachmentStatusUnavailable},
		},
	})
	require.NoError(t, err)

	page := roomFiles(bob, r.ID, "")
	require.False(t, page.HasMore)
	require.Equal(t, []string{"lost.jpg", "huge.mov", "SHOUT.JPG", "notes.txt", "report.pdf", "clip.mp4", "voice.ogg", "pic.png"}, fileNames(page.Items))

	pdf := page.Items[4]
	require.Equal(t, second.Attachments[1].ID, pdf.ID)
	require.Equal(t, dto.RoomFile{
		ID: pdf.ID, Name: "report.pdf", Mime: "application/pdf", Size: int64(len("pdf-bytes")), Status: dto.AttachmentStatusStored,
		Kind: dto.FileKindDocument, MessageID: second.ID, MessageSeq: second.Seq, AuthorKind: dto.AuthorKindUser,
		AuthorName: "alice", CreatedAt: pdf.CreatedAt,
		Author: dto.IntelligenceRef{ID: alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "alice", Handle: "alice"},
	}, pdf)
	require.WithinDuration(t, second.CreatedAt, pdf.CreatedAt, time.Second)
	require.Equal(t, first.ID, page.Items[7].MessageID)
	require.Equal(t, first.Seq, page.Items[7].MessageSeq)

	huge, lost := page.Items[1], page.Items[0]
	require.Equal(t, dto.AttachmentStatusTooLarge, huge.Status)
	require.Equal(t, dto.FileKindVideo, huge.Kind)
	require.Equal(t, dto.AttachmentStatusUnavailable, lost.Status)
	require.Equal(t, fromChat.ID, lost.MessageID)
	require.Equal(t, dto.AuthorKindChat, lost.AuthorKind)
	require.Equal(t, "Customer", lost.AuthorName)

	kinds := map[string]dto.FileKind{}
	for _, f := range page.Items {
		kinds[f.Name] = f.Kind
	}
	require.Equal(t, map[string]dto.FileKind{
		"pic.png": dto.FileKindImage, "SHOUT.JPG": dto.FileKindImage, "lost.jpg": dto.FileKindImage,
		"voice.ogg": dto.FileKindAudio, "clip.mp4": dto.FileKindVideo, "huge.mov": dto.FileKindVideo,
		"report.pdf": dto.FileKindDocument, "notes.txt": dto.FileKindDocument,
	}, kinds)

	require.Equal(t, []string{"lost.jpg", "SHOUT.JPG", "pic.png"}, fileNames(roomFiles(bob, r.ID, "?kind=image").Items))
	require.Equal(t, []string{"voice.ogg"}, fileNames(roomFiles(bob, r.ID, "?kind=audio").Items))
	require.Equal(t, []string{"huge.mov", "clip.mp4"}, fileNames(roomFiles(bob, r.ID, "?kind=video").Items))
	require.Equal(t, []string{"notes.txt", "report.pdf"}, fileNames(roomFiles(bob, r.ID, "?kind=document").Items))

	e := bob.Get("/api/v1/rooms/"+r.ID+"/attachments?kind=spreadsheet").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "kind")

	empty := roomFiles(alice, createRoom(alice, "quiet").ID, "")
	require.NotNil(t, empty.Items)
	require.Empty(t, empty.Items)
	require.False(t, empty.HasMore)
}

// FILES-01: 50 files per page by default, at most 100, and before=<last id> continues the list
// without gaps or repeats, also inside a kind filter.
func TestRoomFilesPaging(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	r := createRoom(alice, "archive")
	const total = 130
	for batch := 0; batch < total/10; batch++ {
		files := make([]upload, 10)
		for i := range files {
			n := batch*10 + i
			mime := "application/pdf"
			if n%2 == 0 {
				mime = "image/png"
			}
			files[i] = upload{fmt.Sprintf("f%03d", n), mime, fmt.Sprint(n)}
		}
		postMultipart(alice, r.ID, dto.PostMessageRequest{}, files...).RequireStatus(http.StatusCreated)
	}
	want := make([]string, total)
	for i := range want {
		want[i] = fmt.Sprintf("f%03d", total-1-i)
	}

	first := roomFiles(alice, r.ID, "")
	require.Len(t, first.Items, 50)
	require.True(t, first.HasMore)
	require.Equal(t, want[:50], fileNames(first.Items))

	capped := roomFiles(alice, r.ID, "?limit=500")
	require.Len(t, capped.Items, 100)
	require.True(t, capped.HasMore)

	var got []string
	before := ""
	for {
		page := roomFiles(alice, r.ID, "?limit=40&before="+before)
		got = append(got, fileNames(page.Items)...)
		if !page.HasMore {
			break
		}
		before = page.Items[len(page.Items)-1].ID
	}
	require.Equal(t, want, got)

	images := roomFiles(alice, r.ID, "?kind=image&limit=30")
	require.True(t, images.HasMore)
	next := roomFiles(alice, r.ID, "?kind=image&limit=100&before="+images.Items[29].ID)
	require.False(t, next.HasMore)
	require.Equal(t, evenNames(total), append(fileNames(images.Items), fileNames(next.Items)...))
}

// evenNames lists f<n> for the even n below total, newest first.
func evenNames(total int) []string {
	var out []string
	for n := total - 1; n >= 0; n-- {
		if n%2 == 0 {
			out = append(out, fmt.Sprintf("f%03d", n))
		}
	}
	return out
}

// FILES-02: the files list follows view access: the creator, an admin of the organization and the people in
// the room 200; everybody else, an instance admin included, 404.
func TestRoomFilesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	runRoomRoutes(t, roles, roomSetup(env, roles), []roomRoute{
		{"list files", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get("/api/v1/rooms/" + res["room"] + "/attachments")
		}, groupWant(200, 200, 200, 404)},
		{"list files with an unknown kind", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return c.Get("/api/v1/rooms/" + res["room"] + "/attachments?kind=bogus")
		}, groupWant(422, 422, 422, 404)},
	})
	roles.Member.Get("/api/v1/rooms/"+"00000000-0000-7000-8000-000000000000"+"/attachments").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	env.Anon().Get("/api/v1/rooms/"+roomSetup(env, roles)(t)["room"]+"/attachments").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}
