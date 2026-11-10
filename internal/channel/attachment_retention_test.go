package channel

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// RET-08: an expired attachment is listed without an id to download; the others keep theirs.
func TestContentOfExpiredAttachment(t *testing.T) {
	n := Format(msg(dto.AuthorKindUser, func(m *dto.Message) {
		m.Text = "old files"
		m.Attachments = []dto.Attachment{
			{ID: "att-1", Name: "photo.jpg", Mime: "image/jpeg", Size: 10, Status: dto.AttachmentStatusExpired},
			{ID: "att-2", Name: "report.pdf", Mime: "application/pdf", Size: 20, Status: dto.AttachmentStatusStored},
		}
	}))
	require.Equal(t, "old files\n[attachment expired: photo.jpg, image/jpeg]\n[attachment att-2: report.pdf, application/pdf]", n.Content)
	require.NotContains(t, n.Content, "att-1")
}

// RET-08: download_attachment of an expired attachment fails with the attachment_expired error and
// saves nothing.
func TestDownloadExpiredAttachment(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	s := e.connected(o)
	m := e.postFile("old.txt", "gone soon")
	require.NoError(t, e.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		_, err := q.ExpireAttachments(context.Background(), []string{m.Attachments[0].ID})
		return err
	}))

	text, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
	require.True(t, isErr, text)
	require.True(t, strings.Contains(text, string(dto.ErrorCodeAttachmentExpired)), text)
	require.Contains(t, text, "expired")
}
