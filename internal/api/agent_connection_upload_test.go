package api_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/testkit"
)

// sentContent reads the file of a send the fake connection took.
func sentContent(t *testing.T, sent connectors.Outbound) []byte {
	t.Helper()
	require.Len(t, sent.Files, 1)
	r, err := sent.Files[0].Open()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return data
}

// spooled lists the temporary files of sends left in the uploads directory.
func (f *replyFixture) spooled(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.env.App.Config.DataDir, app.UploadsDir))
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".send-") {
			names = append(names, e.Name())
		}
	}
	return names
}

// LF-04: a send through a connection attaches a finished upload session of the AI (upload_ids) as its
// file, the text as its caption: the platform gets the file whole, with the upload's name and type, read
// from the blob store; the upload is used up by the send. The multipart message part may name it too.
func TestSendAnUploadThroughTheConnection(t *testing.T) {
	f := newReplyFixture(t)
	agent := f.token.AsAgent("Atlas")
	data := randomBytes(3<<20 + 17)
	u := uploadInChunks(agent, agentUploads, "clip.mp4", "video/mp4", data, 1<<20)

	res := agent.Post(connectionChatPath(f.conn.ID, f.group.ID), dto.SendToChatRequest{Text: "the clip", UploadIDs: []string{u.ID}}).
		RequireStatus(http.StatusCreated)
	require.Equal(t, dto.PlatformSendStatusSent, testkit.Decode[dto.PlatformSendResult](res).Status)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []any{"the clip", "clip.mp4", "video/mp4", int64(len(data))}, []any{sent.Text, sent.Files[0].Name, sent.Files[0].Mime, sent.Files[0].Size})
	require.True(t, bytes.Equal(data, sentContent(t, sent)), "the platform got the file")
	agent.Get(agentUploads+"/"+u.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	v := uploadInChunks(agent, agentUploads, "notes.txt", "text/plain", []byte("synthetic notes"), 1<<20)
	contentType, body := chatUpload{caption: dto.SendToChatRequest{Text: "notes", UploadIDs: []string{v.ID}}}.body(t)
	agent.DoRaw(http.MethodPost, connectionChatPath(f.conn.ID, f.dm.ID), contentType, body).RequireStatus(http.StatusCreated)
	sent = f.inst.WaitSend(t)
	require.Equal(t, []any{"notes", "notes.txt", "synthetic notes"}, []any{sent.Text, sent.Files[0].Name, string(sentContent(t, sent))})
}

// LF-04, CID-32: one file a send: two uploads, or an upload and a file part, are refused; an upload that is
// not finished, or not the AI's, is refused as for a room; nothing is sent and the uploads stay.
func TestAnUploadSendIsValidated(t *testing.T) {
	f := newReplyFixture(t)
	agent := f.token.AsAgent("Atlas")
	a := uploadInChunks(agent, agentUploads, "a.txt", "text/plain", []byte("a"), 10)
	b := uploadInChunks(agent, agentUploads, "b.txt", "text/plain", []byte("b"), 10)
	path := connectionChatPath(f.conn.ID, f.group.ID)

	e := agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{a.ID, b.ID}}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"file": "send one file at a time"}, e.Fields)
	contentType, body := chatUpload{caption: dto.SendToChatRequest{UploadIDs: []string{a.ID}}, files: []chatUploadFile{reportPDF}}.body(t)
	e = agent.DoRaw(http.MethodPost, path, contentType, body).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"file": "send one file at a time"}, e.Fields)

	open := createUpload(agent, agentUploads, dto.CreateUploadRequest{Name: "c.txt", Size: 10})
	agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{open.ID}}).RequireError(http.StatusConflict, dto.ErrorCodeUploadIncomplete)
	theirs := uploadInChunks(f.alice, userUploads, "d.txt", "text/plain", []byte("d"), 10)
	agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{theirs.ID}}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	require.Empty(t, f.inst.Sent())
	agent.Get(agentUploads + "/" + a.ID).RequireStatus(http.StatusOK)
	agent.Get(agentUploads + "/" + b.ID).RequireStatus(http.StatusOK)
}

// CID-32: an upload sent once is used up whether the platform takes it or not: nothing sends it again.
func TestAnUploadThePlatformRefusesIsUsedUp(t *testing.T) {
	f := newReplyFixture(t)
	agent := f.token.AsAgent("Atlas")
	u := uploadInChunks(agent, agentUploads, "a.pdf", "application/pdf", []byte("synthetic pdf"), 10)
	f.inst.SetSendError(errors.New("sendDocument: error 400"))
	e := agent.Post(connectionChatPath(f.conn.ID, f.group.ID), dto.SendToChatRequest{UploadIDs: []string{u.ID}}).
		RequireError(http.StatusBadGateway, dto.ErrorCodeUnavailable)
	require.Equal(t, "the platform did not take the file: sendDocument: error 400", e.Message)
	agent.Get(agentUploads+"/"+u.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// tinyPlatform is a fake platform that takes files of at most 1000 bytes.
func tinyPlatform(k *connectors.Kind) { k.MaxFileBytes = 1000 }

// LF-05: a file over the platform's own limit is refused with 413 file_too_large, the limit and the
// platform in the details, before anything goes out: as a multipart file part, as an upload, and when the
// upload session is created for the connection, before any chunk is sent. A file at the limit goes.
func TestAFileOverThePlatformLimitIsRefusedBeforeItGoes(t *testing.T) {
	f := newReplyFixtureOfKind(t, tinyPlatform)
	agent := f.token.AsAgent("Atlas")
	path := connectionChatPath(f.conn.ID, f.group.ID)
	wantDetails := map[string]string{"max_size": "1000", "platform": "Fake"}

	e := f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", chatUploadFile{name: "big.bin", content: strings.Repeat("a", 1001)}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge)
	require.Equal(t, wantDetails, e.Details)
	require.Contains(t, e.Message, "Fake")

	big := uploadInChunks(agent, agentUploads, "big.bin", "", bytes.Repeat([]byte("a"), 1001), 1<<20)
	e = agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{big.ID}}).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge)
	require.Equal(t, wantDetails, e.Details)
	agent.Get(agentUploads + "/" + big.ID).RequireStatus(http.StatusOK) // refused before the send: the AI deletes it

	e = agent.Post(agentUploads, dto.CreateUploadRequest{Name: "big.bin", Size: 1001, Connection: f.conn.ID}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge)
	require.Equal(t, wantDetails, e.Details)
	require.Empty(t, f.inst.Sent())

	fits := createUpload(agent, agentUploads, dto.CreateUploadRequest{Name: "ok.bin", Size: 1000, Connection: f.conn.ID})
	putChunk(agent, agentUploads, fits.ID, 0, bytes.Repeat([]byte("b"), 1000)).RequireStatus(http.StatusOK)
	agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{fits.ID}}).RequireStatus(http.StatusCreated)
	require.Len(t, sentContent(t, f.inst.WaitSend(t)), 1000)
	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "", chatUploadFile{name: "ok.bin", content: strings.Repeat("c", 1000)}).RequireStatus(http.StatusCreated)
}

// LF-05: an upload session created for a connection is refused as a send through it would be: by an AI
// that does not operate it (403), one that does not see it (404), through a platform without files (422),
// and for a user, who sends nothing through a connection.
func TestAnUploadForAConnectionIsCheckedLikeTheSend(t *testing.T) {
	f := newReplyFixture(t)
	req := dto.CreateUploadRequest{Name: "a.bin", Size: 10, Connection: f.conn.ID}
	f.token.AsAgent("Notes").Post(agentUploads, req).RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	f.env.NewUser("carol").Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Post(agentUploads, req).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	e := f.alice.Post(userUploads, req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "connection")
	createUpload(f.token.AsAgent("Atlas"), agentUploads, req)

	g := newReplyFixtureOfKind(t, func(k *connectors.Kind) { k.CanSendFiles = false })
	g.token.AsAgent("Atlas").Post(agentUploads, dto.CreateUploadRequest{Name: "a.bin", Size: 10, Connection: g.conn.ID}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
}

// LF-04, CID-32: the file part of a multipart send is written to a temporary file on the data disk, not
// held in memory, and removed once the send is over: sent, refused by the platform, refused before it
// went, or over the upload limit.
func TestTheFilePartOfASendLeavesNothingBehind(t *testing.T) {
	f := newReplyFixtureOfKind(t, tinyPlatform, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", reportPDF).RequireStatus(http.StatusCreated)
	require.Equal(t, "synthetic report", string(sentContent(t, f.inst.WaitSend(t))), "read while the request went on")
	require.Empty(t, f.spooled(t), "sent")

	f.inst.SetSendError(errors.New("sendDocument: error 400"))
	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", reportPDF).RequireError(http.StatusBadGateway, dto.ErrorCodeUnavailable)
	require.Empty(t, f.spooled(t), "refused by the platform")
	f.inst.SetSendError(nil)

	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", chatUploadFile{name: "big.bin", content: strings.Repeat("a", 1001)}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge)
	require.Empty(t, f.spooled(t), "over the platform's limit")
	f.sendFileToChat(t, "Notes", f.conn.ID, f.group.ID, "x", reportPDF).RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	require.Empty(t, f.spooled(t), "not the operator")
	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", chatUploadFile{name: "huge.bin", content: strings.Repeat("a", 2<<20)}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	require.Empty(t, f.spooled(t), "over the upload limit")
}

// LF-04: an upload goes out once. A second send of it while the first is still sending (a client whose
// answer a proxy cut off, trying again) is 409 upload_busy, and one after the first is 409 upload_sent,
// saying to check the chat: never a second copy, nor a 404 that reads as if nothing happened. A send
// refused before it went (over the platform's limit) is no use of the upload.
func TestAnUploadIsSentOnce(t *testing.T) {
	f := newReplyFixtureOfKind(t, tinyPlatform)
	agent := f.token.AsAgent("Atlas")
	path := connectionChatPath(f.conn.ID, f.group.ID)
	u := uploadInChunks(agent, agentUploads, "a.pdf", "application/pdf", []byte("synthetic pdf"), 1<<20)

	f.inst.HoldSends()
	t.Cleanup(f.inst.ReleaseSends)
	first := make(chan *testkit.Response, 1)
	go func() { first <- agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{u.ID}}) }()
	f.inst.WaitSending(t)
	e := agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{u.ID}}).RequireError(http.StatusConflict, dto.ErrorCodeUploadBusy)
	require.Contains(t, e.Message, "check the chat")
	f.inst.ReleaseSends()
	(<-first).RequireStatus(http.StatusCreated)
	e = agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{u.ID}}).RequireError(http.StatusConflict, dto.ErrorCodeUploadSent)
	require.Contains(t, e.Message, "check the chat")
	require.Len(t, f.inst.Sent(), 1, "one copy went out")

	big := uploadInChunks(agent, agentUploads, "big.bin", "", bytes.Repeat([]byte("a"), 1001), 1<<20)
	for range 2 {
		agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{big.ID}}).RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge)
	}
}

// readCounter counts the bytes read from r.
type readCounter struct {
	r io.Reader
	n atomic.Int64
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// LF-05: the platform's limit is known before the file part arrives: a part over it is refused as soon
// as it goes past the limit, not written whole to the disk first, and a part for a platform without
// files is refused when it shows.
func TestAFilePartOverThePlatformLimitIsNotReadWhole(t *testing.T) {
	f := newReplyFixtureOfKind(t, tinyPlatform)
	// The part is far bigger than what the kernel's socket buffers take after the server stops reading,
	// so a request read to its end is told apart from one refused early even on a slow runner.
	const size = 32 << 20
	contentType, body := chatUpload{caption: dto.SendToChatRequest{Text: "x"}, files: []chatUploadFile{{name: "big.bin", content: strings.Repeat("a", size)}}}.body(t)
	counted := &readCounter{r: body}
	e := f.token.AsAgent("Atlas").DoRaw(http.MethodPost, connectionChatPath(f.conn.ID, f.group.ID), contentType, counted).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeFileTooLarge)
	require.Equal(t, map[string]string{"max_size": "1000", "platform": "Fake"}, e.Details)
	require.Less(t, counted.n.Load(), int64(size/2), "the request was not read to its end")
	require.Empty(t, f.spooled(t))
	require.Empty(t, f.inst.Sent())

	g := newReplyFixtureOfKind(t, func(k *connectors.Kind) { k.CanSendFiles = false })
	g.sendFileToChat(t, "Atlas", g.conn.ID, g.group.ID, "x", reportPDF).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
	require.Empty(t, g.spooled(t))
}
