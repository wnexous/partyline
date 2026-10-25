package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/testkit"
)

// chatUpload is a multipart body to a chat of a connection: the caption as the message part and each file
// as a file part, with the name and type the sender gives.
type chatUpload struct {
	caption any // the message part: a dto.SendToChatRequest or nil for none
	files   []chatUploadFile
}

type chatUploadFile struct{ name, mime, content string }

func (u chatUpload) body(t *testing.T) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if u.caption != nil {
		part, err := w.CreateFormField(dto.MultipartMessageField)
		require.NoError(t, err)
		require.NoError(t, json.NewEncoder(part).Encode(u.caption))
	}
	for _, f := range u.files {
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{`form-data; name="` + dto.MultipartFileField + `"; filename="` + f.name + `"`}
		if f.mime != "" {
			h["Content-Type"] = []string{f.mime}
		}
		part, err := w.CreatePart(h)
		require.NoError(t, err)
		_, err = io.WriteString(part, f.content)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return w.FormDataContentType(), &buf
}

// sendFileToChat uploads a file with a caption to the chat of the connection as the AI of the folder name.
func (f *replyFixture) sendFileToChat(t *testing.T, name, connection, chat, caption string, files ...chatUploadFile) *testkit.Response {
	t.Helper()
	contentType, body := chatUpload{caption: dto.SendToChatRequest{Text: caption}, files: files}.body(t)
	return f.token.AsAgent(name).DoRaw(http.MethodPost, connectionChatPath(connection, chat), contentType, body)
}

var reportPDF = chatUploadFile{name: "report.pdf", mime: "application/pdf", content: "synthetic report"}

// CID-32: the operator sends a file to a chat of its connection with a multipart request, the same
// endpoint and the same ids as a text: the platform gets the file's name, type and content and the
// caption as the text, as the account itself and unsigned; the answer is 201 with the state of the send,
// the record is marked as attempted once and its caption is gone.
func TestSendAFileToAChatOfTheConnection(t *testing.T) {
	f := newReplyFixture(t)
	res := f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "the report", reportPDF).RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"id", "status"}, keysOf(t, res.Body))
	got := testkit.Decode[dto.PlatformSendResult](res)
	require.Equal(t, dto.PlatformSendStatusSent, got.Status)

	sent := f.inst.WaitSend(t)
	require.Equal(t, []any{"120363000000000001@g.us", "the report", true, ""}, []any{sent.ChatID, sent.Text, sent.Unsigned, sent.Signature()})
	require.Len(t, sent.Files, 1)
	r, err := sent.Files[0].Open()
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, []any{"report.pdf", "application/pdf", int64(16), "synthetic report"},
		[]any{sent.Files[0].Name, sent.Files[0].Mime, sent.Files[0].Size, string(data)})
	row := f.sendRow(t, got.ID)
	require.Equal(t, []any{"sent", true, "", f.group.ID}, []any{row.Status, row.Once, row.Text, row.ChatID})

	f.sendFileToChat(t, "Atlas", f.conn.ID, f.dm.ID, "", chatUploadFile{name: "photo.png", mime: "image/png", content: "png"}).RequireStatus(http.StatusCreated)
	sent = f.inst.WaitSend(t)
	require.Equal(t, []any{"5511999990000", "", "photo.png"}, []any{sent.ChatID, sent.Text, sent.Files[0].Name}, "a direct chat, and no caption")
}

// CID-32: what the sender says about the file is bounded and cleaned by the server: a path in the name
// is cut to the file's name, and a type that is missing is a binary one.
func TestAnUploadedFileKeepsOnlyItsNameAndGetsADefaultType(t *testing.T) {
	f := newReplyFixture(t)
	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "", chatUploadFile{name: "../../etc/notes.txt", content: "x"}).RequireStatus(http.StatusCreated)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []string{"notes.txt", "application/octet-stream"}, []string{sent.Files[0].Name, sent.Files[0].Mime})
}

// CID-32, CID-31: a file starts a chat like a text.
func TestSendAFileToANewAddress(t *testing.T) {
	f := newReplyFixture(t)
	f.sendFileToChat(t, "Atlas", f.conn.ID, newNumber, "the report", reportPDF).RequireStatus(http.StatusCreated)
	require.Equal(t, newNumber, f.inst.WaitSend(t).ChatID)
	require.Contains(t, f.chatsOf(t), newNumber)
}

// CID-32: a multipart request without a file is a text, and one with neither is refused; one file at a
// time, a file with no content is refused, and nothing is sent for any of them.
func TestUploadsAreValidated(t *testing.T) {
	f := newReplyFixture(t)
	post := func(u chatUpload) *testkit.Response {
		contentType, body := u.body(t)
		return f.token.AsAgent("Atlas").DoRaw(http.MethodPost, connectionChatPath(f.conn.ID, f.group.ID), contentType, body)
	}

	post(chatUpload{caption: dto.SendToChatRequest{Text: "only text"}}).RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"only text"}, outboundTexts(f.inst.Sent()))

	e := post(chatUpload{caption: dto.SendToChatRequest{Text: "x"}, files: []chatUploadFile{reportPDF, reportPDF}}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"file": "send one file at a time"}, e.Fields)
	e = post(chatUpload{caption: dto.SendToChatRequest{Text: "x"}, files: []chatUploadFile{{name: "empty.txt", mime: "text/plain"}}}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"file": "must not be empty"}, e.Fields)
	e = post(chatUpload{}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "text", "neither a text nor a file")
	e = post(chatUpload{caption: dto.SendToChatRequest{Text: strings.Repeat("a", dto.MaxTextLength+1)}, files: []chatUploadFile{reportPDF}}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "text")

	f.token.AsAgent("Atlas").DoRaw(http.MethodPost, connectionChatPath(f.conn.ID, f.group.ID), "multipart/form-data; boundary=x",
		strings.NewReader("--x\r\nthis header has no colon\r\n\r\nbody\r\n--x--\r\n")).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	f.token.AsAgent("Atlas").DoRaw(http.MethodPost, connectionChatPath(f.conn.ID, f.group.ID), "multipart/form-data; boundary=x",
		strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"message\"\r\n\r\nnot json\r\n--x--\r\n")).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Len(t, f.inst.Sent(), 1, "only the text went out")
}

// CID-32, SEC-03: the existing upload limit (PARTYLINE_MAX_UPLOAD_MB) holds for a file sent through a
// connection: over it the request is 413 too_large and nothing is sent; at it the file goes.
func TestAFileOverTheUploadLimitIsRefused(t *testing.T) {
	f := newReplyFixture(t, testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", chatUploadFile{name: "big.bin", content: strings.Repeat("a", 2<<20)}).
		RequireError(http.StatusRequestEntityTooLarge, dto.ErrorCodeTooLarge)
	require.Empty(t, f.inst.Sent())
	f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", chatUploadFile{name: "ok.bin", content: strings.Repeat("a", 900<<10)}).RequireStatus(http.StatusCreated)
	require.Len(t, f.inst.Sent(), 1)
}

// CID-03, CID-32: a file is sent by the operator alone, with the refusals of any send: another AI of
// the organization is not the operator, an AI outside it does not see the connection, users and anonymous
// callers have no agent token; and a chat of another connection is an unknown chat.
func TestOnlyTheOperatorSendsAFileThroughTheAPI(t *testing.T) {
	f := newReplyFixture(t)
	carol := f.env.NewUser("carol")
	_, foreignChat := f.secondConnection(t)
	contentType, body := chatUpload{caption: dto.SendToChatRequest{Text: "x"}, files: []chatUploadFile{reportPDF}}.body(t)
	path := connectionChatPath(f.conn.ID, f.group.ID)

	f.sendFileToChat(t, "Notes", f.conn.ID, f.group.ID, "x", reportPDF).RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	carol.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").DoRaw(http.MethodPost, path, contentType, bytes.NewReader(body.Bytes())).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.alice.DoRaw(http.MethodPost, path, contentType, bytes.NewReader(body.Bytes())).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").DoRaw(http.MethodPost, path, contentType, bytes.NewReader(body.Bytes())).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	f.sendFileToChat(t, "Atlas", f.conn.ID, foreignChat, "x", reportPDF).RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
	require.Empty(t, f.inst.Sent())
}

// CID-32, CID-18: a platform that refuses the file is 502 with the reason; the send is failed at once
// and nothing sends it again, because the file is not kept. A refused request does not go that far.
func TestAFileThePlatformRefusesIsFailedAndNotRetried(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	f := newReplyFixture(t, testkit.WithOutbox(), testkit.WithClock(clock.Now))
	f.inst.SetSendError(errors.New("sendDocument: error 413"))
	e := f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "the report", reportPDF).RequireError(http.StatusBadGateway, dto.ErrorCodeUnavailable)
	require.Equal(t, "the platform did not take the file: sendDocument: error 413", e.Message)

	f.inst.SetSendError(nil)
	clock.Advance(time.Minute)
	require.Never(t, func() bool { return len(f.inst.Sent()) > 0 }, 300*time.Millisecond, 20*time.Millisecond, "nobody sends it again")
}

// CID-32: a connection whose platform has no media send (the webhook) answers 422 unsupported to a file,
// in words that say what to do, and sends nothing; its texts are unaffected.
func TestAFileThroughAKindWithoutMediaIsUnsupported(t *testing.T) {
	f := newReplyFixtureOfKind(t, func(k *connectors.Kind) { k.CanSendFiles = false })
	e := f.sendFileToChat(t, "Atlas", f.conn.ID, f.group.ID, "x", reportPDF).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
	require.Equal(t, "the platform of this connection cannot be sent files: write to the chat with send_message", e.Message)
	require.Empty(t, f.inst.Sent())
	f.sendToChat("Atlas", f.conn.ID, f.group.ID, "a text still goes").RequireStatus(http.StatusCreated)
}
