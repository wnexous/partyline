package channel

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/testkit"
)

// AD-01: send_file with connection, chat and as_document passes the flag to the server: a small file in
// its multipart post and a large one with the send that attaches its upload; the connector gets the file
// marked as a document, under its own name and type. Without the flag it is not marked.
func TestSendFileAsDocumentThroughAConnection(t *testing.T) {
	c := newConnectionChan(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "4"}))
	s := c.connected(c.options())

	small := reportFile(t, "photo.jpg", "synthetic photo")
	text, isErr := s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", small, "as_document", true))
	require.False(t, isErr, text)
	sent := c.inst.WaitSend(t)
	require.Equal(t, []any{"photo.jpg", "image/jpeg", true}, []any{sent.Files[0].Name, sent.Files[0].Mime, sent.Files[0].AsDocument})

	large, _ := randomFile(t, "clip.mp4", int64(multipartMaxBytes)+1<<20)
	text, isErr = s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", large, "as_document", true))
	require.False(t, isErr, text)
	sent = c.inst.WaitSend(t)
	require.Equal(t, []any{"clip.mp4", "video/mp4", true}, []any{sent.Files[0].Name, sent.Files[0].Mime, sent.Files[0].AsDocument})

	text, isErr = s.call("send_file", toolArgs("connection", c.conn, "chat", c.group.ID, "path", small))
	require.False(t, isErr, text)
	require.False(t, c.inst.WaitSend(t).Files[0].AsDocument, "without the flag")
}

// AD-02: as_document with a room target is a tool error that says why, before the file is read or
// uploaded; as_document false with a room is a plain upload.
func TestSendFileAsDocumentRoomRefused(t *testing.T) {
	c := newConnectionChan(t)
	s := c.connected(c.options())
	path := reportFile(t, "photo.jpg", "synthetic photo")
	before := len(c.messages(c.room.ID))

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", path, "as_document", true))
	require.True(t, isErr, text)
	require.Equal(t, msgFileDocumentRoom, text)
	require.Contains(t, text, "as_document applies to a connection")
	text, isErr = s.call("send_file", toolArgs("room", "Support", "path", "/no/such/file.jpg", "as_document", true))
	require.True(t, isErr)
	require.Equal(t, msgFileDocumentRoom, text, "refused before the file is opened")
	require.Len(t, c.messages(c.room.ID), before, "nothing was posted")

	text, isErr = s.call("send_file", toolArgs("room", "Support", "path", path, "as_document", false))
	require.False(t, isErr, text)
	require.Len(t, c.messages(c.room.ID), before+1)
	require.Empty(t, c.inst.Sent())
}

// AD-03: the send_file schema has as_document, a boolean that says when to use it, and the description
// says it keeps the original quality and applies to a connection.
func TestSendFileToolSchemaAsDocument(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] != "send_file" {
			continue
		}
		prop := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["as_document"].(map[string]any)
		require.Equal(t, "boolean", prop["type"])
		require.Contains(t, prop["description"], "as a document")
		require.Contains(t, tool["description"], "as_document")
		require.Contains(t, tool["description"], "original quality")
		return
	}
	t.Fatal("send_file is not listed")
}
