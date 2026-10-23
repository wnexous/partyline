package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/testkit"
)

// file queues a message of Atlas's with one stored file whose blob holds content, and returns the
// item's id and the file's id.
func (f *platformWS) file(t *testing.T, name, mime, content string) (item, file string) {
	t.Helper()
	sha, _, err := f.env.App.Blobs.Put(strings.NewReader(content), 1<<20)
	require.NoError(t, err)
	item = f.enqueue(t, f.atlas, f.group, "file-"+name, "see the file", func(it *platform.Item) {
		it.Attachments = []platform.Attachment{{Name: name, Mime: mime, Size: int64(len(content)), SHA256: sha, Status: dto.AttachmentStatusStored}}
	})
	e := f.item(t, f.atlas, item)
	require.Len(t, e.Attachments, 1)
	return item, e.Attachments[0].ID
}

// CID-09: the files of a platform message are downloadable through the agent attachments route by
// the AI that holds the message, before and after it acknowledged the message; documents download,
// images show inline, and a file that was not stored has nothing to download.
func TestOperatorDownloadsTheFilesOfItsPlatformMessages(t *testing.T) {
	f := newPlatformWS(t)
	atlas := f.token.AsAgent("Atlas")
	item, report := f.file(t, "report.txt", "text/plain", "quarterly numbers")
	_, photo := f.file(t, "photo.png", "image/png", "png bytes")

	got := atlas.Get("/api/v1/agent/attachments/" + report).RequireStatus(http.StatusOK)
	require.Equal(t, "quarterly numbers", string(got.Body))
	require.Equal(t, "attachment; filename=report.txt", got.Header.Get("Content-Disposition"))
	require.Equal(t, "application/octet-stream", got.Header.Get("Content-Type"))
	img := atlas.Get("/api/v1/agent/attachments/" + photo).RequireStatus(http.StatusOK)
	require.Equal(t, "png bytes", string(img.Body))
	require.Equal(t, "inline; filename=photo.png", img.Header.Get("Content-Disposition"))
	require.Equal(t, "image/png", img.Header.Get("Content-Type"))

	require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.atlas, item))
	after := atlas.Get("/api/v1/agent/attachments/" + report).RequireStatus(http.StatusOK)
	require.Equal(t, "quarterly numbers", string(after.Body), "the file outlives the acknowledgement")

	big := f.enqueue(t, f.atlas, f.group, "big", "too big", func(it *platform.Item) {
		it.Attachments = []platform.Attachment{{Name: "movie.mov", Mime: "video/quicktime", Size: 9 << 30, Status: dto.AttachmentStatusTooLarge}}
	})
	missing := f.item(t, f.atlas, big).Attachments[0].ID
	atlas.Get("/api/v1/agent/attachments/"+missing).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	atlas.Get("/api/v1/agent/attachments/unknown").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// CID-09: 24 hours after the acknowledgement the file is gone from the route.
func TestPlatformFileIsGoneAfterTheAcknowledgementWindow(t *testing.T) {
	f := newPlatformWS(t)
	item, file := f.file(t, "report.txt", "text/plain", "quarterly numbers")
	require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.atlas, item))
	atlas := f.token.AsAgent("Atlas")

	_, err := f.env.App.Platform.Purge(context.Background(), time.Now().Add(23*time.Hour))
	require.NoError(t, err)
	atlas.Get("/api/v1/agent/attachments/" + file).RequireStatus(http.StatusOK)
	_, err = f.env.App.Platform.Purge(context.Background(), time.Now().Add(25*time.Hour))
	require.NoError(t, err)
	atlas.Get("/api/v1/agent/attachments/"+file).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// Authorization of the download: only the AI that holds the message. Another AI of the same owner,
// the AI of a user granted use of Atlas and the AI of a stranger get 404, as for an unknown file;
// users, with a session or a token, get 403 on the agent route and 404 on the user route, which
// never serves a platform file; anonymous callers get 401.
func TestPlatformFilesAreForTheOperatorOnly(t *testing.T) {
	f := newPlatformWS(t)
	bob, carol := f.env.NewUser("bob"), f.env.NewUser("carol")
	shareAgent(f.alice, f.atlas, bob.User.ID)
	_, file := f.file(t, "report.txt", "text/plain", "quarterly numbers")
	path := "/api/v1/agent/attachments/" + file

	f.token.AsAgent("Notes").Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	for name, u := range map[string]*testkit.Client{"use grantee": bob, "stranger": carol} {
		u.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Get(path).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		_ = name
	}
	f.alice.Get(path).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Token(dto.TokenKindUser, "script").Get(path).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").Get(path).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	for _, u := range []*testkit.Client{f.alice, bob, carol} {
		u.Get("/api/v1/attachments/"+file).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}

	f.token.AsAgent("Atlas").Get(path).RequireStatus(http.StatusOK)
}

// A file of a room stays as it was: an AI downloads the attachments of its rooms through the same
// route, and a file of a platform message is not one of them (it is looked up by the AI's queue
// only after the rooms).
func TestRoomFilesStillDownloadThroughTheAgentRoute(t *testing.T) {
	f := newPlatformWS(t)
	room := createRoom(f.alice, "Support")
	f.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	m := testkit.Decode[dto.Message](postMultipart(f.alice, room.ID, dto.PostMessageRequest{Text: "the plan"}, upload{"plan.txt", "text/plain", "step one"}).RequireStatus(http.StatusCreated))
	_, file := f.file(t, "report.txt", "text/plain", "quarterly numbers")

	atlas := f.token.AsAgent("Atlas")
	require.Equal(t, "step one", string(atlas.Get("/api/v1/agent/attachments/"+m.Attachments[0].ID).RequireStatus(http.StatusOK).Body))
	require.Equal(t, "quarterly numbers", string(atlas.Get("/api/v1/agent/attachments/"+file).RequireStatus(http.StatusOK).Body))
	f.token.AsAgent("Notes").Get("/api/v1/agent/attachments/"+m.Attachments[0].ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}
