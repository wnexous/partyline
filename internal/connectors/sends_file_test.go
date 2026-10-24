package connectors_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// reportFile is a file an AI sends: synthetic content.
func reportFile() *connectors.SendFile {
	return &connectors.SendFile{Name: "report.pdf", Mime: "application/pdf", Data: []byte("synthetic report")}
}

// fileInput is a send of a file with a caption to the group as Atlas.
func (f *sendsFixture) fileInput(caption string, file *connectors.SendFile) connectors.SendInput {
	in := f.input(caption)
	in.File = file
	return in
}

// content reads what an outbound file holds.
func content(t *testing.T, file connectors.OutFile) string {
	t.Helper()
	r, err := file.Open()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}

// CID-32: a file goes through the connection as the account itself, unsigned: the platform gets its
// name, type, size and content and the caption as the text, and the platform's id of what it created is
// recorded so that the echo is recognized (CID-14). The send is marked as attempted once, and the
// caption is gone from the row when it is over.
func TestAFileGoesOutThroughTheConnectionOnce(t *testing.T) {
	f := newSendsFixture(t)
	res, err := f.sends.Send(context.Background(), f.fileInput("the report", reportFile()))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusSent}, res)

	sent := f.inst.Sent()
	require.Len(t, sent, 1)
	got := sent[0]
	require.Equal(t, []any{groupID, "the report", true, ""}, []any{got.ChatID, got.Text, got.Unsigned, got.Signature()})
	require.Len(t, got.Files, 1)
	file := got.Files[0]
	require.Equal(t, []any{"report.pdf", "application/pdf", int64(16), "synthetic report"}, []any{file.Name, file.Mime, file.Size, content(t, file)})

	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"sent", true, int64(1), "ext-1", ""}, []any{row.Status, row.Once, row.Attempts, row.ExternalID, row.Text})
	require.True(t, f.sent.Has(f.chat.ID, "ext-1"))
}

// CID-32: a file needs no caption, and an ordinary send is not marked as attempted once.
func TestAFileNeedsNoCaptionAndATextSendIsRetriable(t *testing.T) {
	f := newSendsFixture(t)
	res, err := f.sends.Send(context.Background(), f.fileInput("", reportFile()))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendStatusSent, res.Status)
	require.Empty(t, f.inst.Sent()[0].Text)

	text, err := f.send("a text")
	require.NoError(t, err)
	require.False(t, f.sendRow(t, text.ID).Once)
}

// CID-32: what is wrong with a file is refused before anything is stored or sent: no content, and a
// caption over the message limit; no text and no file is still a refusal of the text.
func TestAFileSendIsValidated(t *testing.T) {
	f := newSendsFixture(t)
	for name, tc := range map[string]struct {
		in    connectors.SendInput
		field string
	}{
		"an empty file":          {f.fileInput("x", &connectors.SendFile{Name: "empty.txt", Mime: "text/plain"}), "file"},
		"a caption over 16000":   {f.fileInput(strings.Repeat("a", dto.MaxTextLength+1), reportFile()), "text"},
		"no text and no file":    {f.input(" "), "text"},
		"a file without a name":  {f.fileInput("x", &connectors.SendFile{Mime: "text/plain", Data: []byte("x")}), "file"},
		"a name that is a path":  {f.fileInput("x", &connectors.SendFile{Name: "../etc/passwd", Mime: "text/plain", Data: []byte("x")}), "file"},
		"a name over 255 bytes":  {f.fileInput("x", &connectors.SendFile{Name: strings.Repeat("n", 256), Mime: "text/plain", Data: []byte("x")}), "file"},
		"a name with a new line": {f.fileInput("x", &connectors.SendFile{Name: "a\nb.txt", Mime: "text/plain", Data: []byte("x")}), "file"},
	} {
		err := f.sends.Check(context.Background(), tc.in)
		var invalid *validation.Error
		require.ErrorAs(t, err, &invalid, name)
		require.Contains(t, invalid.Fields, tc.field, name)
		_, err = f.sends.Send(context.Background(), tc.in)
		require.ErrorAs(t, err, &invalid, name)
	}
	require.Zero(t, f.sendRows(t))
	require.Empty(t, f.inst.Sent())
}

// CID-32, CID-18: a platform that refuses a file fails the send at once, with the reason, and nothing
// tries again: the file is not kept, so there is nothing to send. The caption is gone from the row.
func TestAFileThePlatformRefusesIsNotRetried(t *testing.T) {
	f := newSendsFixture(t)
	f.inst.SetSendError(errors.New("sendDocument: error 413"))
	res, err := f.sends.Send(context.Background(), f.fileInput("the report", reportFile()))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusFailed, Error: "sendDocument: error 413"}, res)
	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"failed", true, int64(1), "sendDocument: error 413", ""}, []any{row.Status, row.Once, row.Attempts, row.LastError, row.Text})

	f.inst.SetSendError(nil)
	f.clock.Advance(time.Hour)
	require.NoError(t, f.sends.Flush(context.Background()))
	require.Empty(t, f.inst.Sent(), "a failed file is never sent by the worker")
	require.Equal(t, "failed", f.sendRow(t, res.ID).Status)
}

// CID-32: when the platform took the caption and refused the file the AI is told the caption is out,
// so it does not send it again with the file.
func TestAFailedFileSendSaysWhatWentOut(t *testing.T) {
	f := newSendsFixture(t)
	f.inst.SetSendErrorAfterFirst(errors.New("sendDocument: error 413"))
	res, err := f.sends.Send(context.Background(), f.fileInput("the report", reportFile()))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendStatusFailed, res.Status)
	require.Equal(t, "sendDocument: error 413; the caption was sent, the file was not", res.Error)
	require.True(t, f.sent.Has(f.chat.ID, "ext-1"), "what went out is remembered all the same")

	plain, err := f.sends.Send(context.Background(), f.fileInput("", reportFile()))
	require.NoError(t, err)
	require.Equal(t, "sendDocument: error 413", plain.Error, "without a caption nothing went out")
}

// CID-32, CID-18: a platform that never answers fails the attempt after FileAttemptTimeout (here 100 ms)
// and, like any refusal of a file, nothing tries again.
func TestAFileSendThatNeverAnswersTimesOut(t *testing.T) {
	require.Equal(t, 90*time.Second, connectors.SendFileTimeout, "an upload gets a minute and a half")
	f := newSendsFixture(t, func(d *connectors.SendsDeps) { d.FileAttemptTimeout = 100 * time.Millisecond })
	f.inst.HoldSends()
	defer f.inst.ReleaseSends()
	res, err := f.sends.Send(context.Background(), f.fileInput("the report", reportFile()))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusFailed, Error: "the platform did not answer within 100ms; the file may or may not have arrived"}, res)
	require.Equal(t, "failed", f.sendRow(t, res.ID).Status)
}

// CID-32: a connection whose platform has no media send (the webhook) refuses a file, whatever else is
// true of the request; the same kind sends text, and nothing was created for the refused file, not even
// the chat of a new address.
func TestAKindThatCannotSendFilesRefusesThem(t *testing.T) {
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		kind := connectortest.New().Kind()
		kind.CanSendFiles = false
		d.Kinds = connectors.NewRegistry(kind)
	})
	count := len(f.chats(t))
	_, err := f.sends.Send(context.Background(), f.fileInput("the report", reportFile()))
	require.ErrorIs(t, err, connectors.ErrFilesUnsupported)
	_, err = f.sends.Send(context.Background(), connectors.SendInput{AgentID: f.atlas, ConnectorID: f.conn, ChatID: newNumber, Text: "x", File: reportFile()})
	require.ErrorIs(t, err, connectors.ErrFilesUnsupported)
	require.Len(t, f.chats(t), count)
	require.Empty(t, f.inst.Sent())
	require.Zero(t, f.sendRows(t))

	_, err = f.send("text is fine")
	require.NoError(t, err)
}

// CID-03, CID-32: only the operator sends a file; an AI that is not refused as for any send, before
// the kind or the file are looked at.
func TestOnlyTheOperatorSendsAFile(t *testing.T) {
	f := newSendsFixture(t)
	in := f.fileInput("x", reportFile())
	in.AgentID = f.ai(t)
	_, err := f.sends.Send(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrNotOperator)
	require.Empty(t, f.inst.Sent())
}

// CID-32, CID-31: a file may start a chat like a text: the address is created, the allowance for new
// chats is spent once, and a refusal of the allowance creates nothing.
func TestAFileMayStartAChat(t *testing.T) {
	allow := false
	asked := 0
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		d.AllowNewChat = func(string) bool { asked++; return allow }
	})
	count := len(f.chats(t))
	in := connectors.SendInput{AgentID: f.atlas, ConnectorID: f.conn, ChatID: newNumber, Text: "the report", File: reportFile()}
	_, err := f.sends.Send(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrNewChatRateLimited)
	require.Len(t, f.chats(t), count)

	allow = true
	res, err := f.sends.Send(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendStatusSent, res.Status)
	require.Equal(t, []string{newNumber}, []string{f.inst.Sent()[0].ChatID})
	require.Len(t, f.chats(t), count+1)
	require.Equal(t, 2, asked)
}

// CID-32: a process that dies while it sends a file leaves the send in flight; at the next start it is
// failed, not sent again: its file is gone, and sending its caption alone would be another message than
// the one asked. An ordinary send in flight is due again as before.
func TestAFileSendLeftInFlightIsFailedAtTheNextStart(t *testing.T) {
	f := newSendsFixture(t)
	ctx := context.Background()
	insert := func(once bool, text string) string {
		id := store.NewID()
		require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
			return q.InsertPlatformSend(ctx, db.InsertPlatformSendParams{
				ID: id, ConnectorID: f.conn, ChatID: f.chat.ID, AgentID: f.atlas, Text: text, Once: once,
				NextAttemptAt: f.clock.Now().UnixMilli(), CreatedAt: f.clock.Now().UnixMilli(), UpdatedAt: f.clock.Now().UnixMilli(),
			})
		}))
		return id
	}
	file, plain := insert(true, "the report"), insert(false, "a text")

	require.NoError(t, f.sends.Recover(ctx))
	row := f.sendRow(t, file)
	require.Equal(t, []any{"failed", int64(1), "interrupted by a restart: the file was not kept", ""}, []any{row.Status, row.Attempts, row.LastError, row.Text})
	require.Equal(t, "pending", f.sendRow(t, plain).Status)

	require.NoError(t, f.sends.Flush(ctx))
	require.Equal(t, []string{"a text"}, outboundTexts(f.inst.Sent()), "only the ordinary send goes out")
}
