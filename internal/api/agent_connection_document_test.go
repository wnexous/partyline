package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// AD-01: as_document on a send through a connection reaches the connector with the file, as a multipart
// file part and as an upload; a file sent without it is not marked.
func TestAgentConnectionFileAsDocument(t *testing.T) {
	f := newReplyFixture(t)
	agent := f.token.AsAgent("Atlas")
	path := connectionChatPath(f.conn.ID, f.group.ID)
	photo := chatUploadFile{name: "photo.jpg", mime: "image/jpeg", content: "synthetic photo"}

	contentType, body := chatUpload{caption: dto.SendToChatRequest{Text: "the photo", AsDocument: true}, files: []chatUploadFile{photo}}.body(t)
	agent.DoRaw(http.MethodPost, path, contentType, body).RequireStatus(http.StatusCreated)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []any{"the photo", "photo.jpg", "image/jpeg", true},
		[]any{sent.Text, sent.Files[0].Name, sent.Files[0].Mime, sent.Files[0].AsDocument})

	u := uploadInChunks(agent, agentUploads, "clip.mp4", "video/mp4", []byte("synthetic clip"), 1<<20)
	agent.Post(path, dto.SendToChatRequest{UploadIDs: []string{u.ID}, AsDocument: true}).RequireStatus(http.StatusCreated)
	sent = f.inst.WaitSend(t)
	require.Equal(t, []any{"clip.mp4", "video/mp4", true}, []any{sent.Files[0].Name, sent.Files[0].Mime, sent.Files[0].AsDocument})

	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "", photo).RequireStatus(http.StatusCreated)
	require.False(t, f.inst.WaitSend(t).Files[0].AsDocument, "without the flag")
}

// AD-01: as_document without a file is 422 invalid on as_document, in a JSON body and in a multipart one,
// and nothing is sent.
func TestAgentConnectionFileAsDocumentNeedsAFile(t *testing.T) {
	f := newReplyFixture(t)
	agent := f.token.AsAgent("Atlas")
	path := connectionChatPath(f.conn.ID, f.group.ID)

	e := agent.Post(path, dto.SendToChatRequest{Text: "hi", AsDocument: true}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "as_document")
	contentType, body := chatUpload{caption: dto.SendToChatRequest{Text: "hi", AsDocument: true}}.body(t)
	e = agent.DoRaw(http.MethodPost, path, contentType, body).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "as_document")
	require.Empty(t, f.inst.Sent())
}
