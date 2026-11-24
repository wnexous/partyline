package connectors

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
)

// foldKinds are the events that change the message they target instead of being messages of their
// own in the chat's history (CH-02).
var foldKinds = []dto.MessageKind{dto.MessageKindEdit, dto.MessageKindRevoke, dto.MessageKindReaction}

// folds tells whether a message of the kind changes the message it targets.
func folds(kind string) bool { return slices.Contains(foldKinds, dto.MessageKind(kind)) }

// keeps tells whether the connection keeps its chat history (CH-01).
func (p *Pipeline) keeps(conn db.Connector) bool {
	return p.d.History != nil && conn.HistoryDays != platform.HistoryOff
}

// fold applies an edit, a delete or a reaction to the kept message it targets (CH-02). A target the
// history does not have (older than the retention, never kept) changes nothing. Failures are logged
// without content: the history never stops a delivery (CH-04).
func (p *Pipeline) fold(ctx context.Context, src Source, chat db.Chat, m Inbound) {
	var err error
	switch dto.MessageKind(m.Kind) {
	case dto.MessageKindEdit:
		_, err = p.d.History.Edit(ctx, src.ConnectorID, chat.ID, m.ReplyToID, truncate(m.Text))
	case dto.MessageKindRevoke:
		_, err = p.d.History.Delete(ctx, src.ConnectorID, chat.ID, m.ReplyToID)
	case dto.MessageKindReaction:
		emoji := strings.TrimSpace(m.Text)
		if emoji == ReactionRemoved {
			emoji = ""
		}
		_, err = p.d.History.React(ctx, src.ConnectorID, chat.ID, m.ReplyToID, platform.HistoryReaction{
			SenderID: m.SenderID, SenderName: m.SenderName, FromSelf: m.FromSelf, Emoji: emoji, At: m.At,
		})
	}
	if err != nil {
		p.d.Logger.Warn("connectors: chat history change failed", "connector_id", src.ConnectorID, "err", err)
	}
}

// keep adds the message to its chat's history (CH-02): under the id of the item it was queued as, with
// that item's files, or, when it was not delivered, under an id of its own, with its files named but not
// downloaded. What d leaves out (a message not queued) is decided here as the queue would have.
// Failures are logged without content.
func (p *Pipeline) keep(ctx context.Context, src Source, conn db.Connector, chat db.Chat, m Inbound, item string, d *decided) {
	if err := p.record(ctx, src, conn, chat, m, item, d); err != nil && ctx.Err() == nil {
		p.d.Logger.Warn("connectors: chat history failed", "connector_id", src.ConnectorID, "err", err)
	}
}

func (p *Pipeline) record(ctx context.Context, src Source, conn db.Connector, chat db.Chat, m Inbound, item string, d *decided) error {
	if d == nil {
		got, err := p.decide(ctx, src, conn.OperatorIid.String, chat, m)
		if err != nil {
			return err
		}
		d = &got
	}
	h := platform.HistoryMessage{
		ID: item, ConnectorID: src.ConnectorID, ChatID: chat.ID, ExternalID: m.MessageID,
		SenderID: m.SenderID, SenderName: m.SenderName, FromSelf: m.FromSelf, Trusted: d.trusted,
		Kind: d.kind, Text: truncate(m.Text), ReplyTo: d.q.replyTo, Quoted: d.q.preview, QuotedFromSelf: d.q.fromSelf,
		At: m.At,
	}
	if item != "" {
		files, err := p.d.Store.R().ListPlatformAttachmentsForItems(ctx, []string{item})
		if err != nil {
			return err
		}
		for _, f := range files {
			h.Attachments = append(h.Attachments, platform.HistoryAttachment{
				ID: f.ID, Name: f.Name, Mime: f.Mime, Size: f.Size, Status: dto.AttachmentStatus(f.Status),
			})
		}
	} else {
		h.Attachments = namedOnly(m.Media)
	}
	_, err := p.d.History.Record(ctx, h)
	return err
}

// namedOnly lists media by name and type only: nothing downloaded them.
func namedOnly(media []Media) []platform.HistoryAttachment {
	out := make([]platform.HistoryAttachment, 0, len(media))
	for _, md := range media {
		a := platform.HistoryAttachment{Name: md.Name, Mime: md.Mime, Size: md.Size, Status: dto.AttachmentStatusUnavailable}
		if a.Name == "" {
			a.Name = "file"
		}
		if a.Mime == "" {
			a.Mime = "application/octet-stream"
		}
		out = append(out, a)
	}
	return out
}

// backfill adds a message of a chat's past, which the platform sent on its own (WhatsApp's history
// sync when the number is paired) or because the AI asked for it, to the chat's history, and delivers
// nothing (CH-08, CH-09). It is kept while the connection's retention is on, for a chat of a kind its
// deliver setting takes, which is recorded when the connection did not have it yet; a message the
// history has (by its platform id) or an AI's send made (its first or a later message) is not added
// again. Its files are named, never downloaded.
func (p *Pipeline) backfill(ctx context.Context, src Source, m Inbound) error {
	if p.d.History == nil || (strings.TrimSpace(m.Text) == "" && len(m.Media) == 0) {
		return nil
	}
	conn, err := p.d.Store.R().GetConnector(ctx, src.ConnectorID)
	if err != nil || !conn.OperatorIid.Valid || !p.keeps(conn) {
		return err
	}
	if !m.At.IsZero() && !platform.Within(m.At, conn.HistoryDays, p.d.Now()) {
		return nil // older than the retention: neither the message nor its chat
	}
	chat, err := p.d.Store.R().GetChatByExternalID(ctx, db.GetChatByExternalIDParams{ConnectorID: src.ConnectorID, ExternalID: m.ChatID})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if !delivers(dto.ConnectorDeliver(conn.Deliver), m.ChatKind) {
			return nil
		}
		chat, err = UpsertChat(ctx, p.d.Store, p.d.Bus, src.ConnectorID, ChatInfo{ID: m.ChatID, Title: m.ChatTitle, Kind: m.ChatKind}, m.At, p.d.Now())
		if err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if !delivers(dto.ConnectorDeliver(conn.Deliver), chat.Kind) {
		return nil
	}
	if m.FromSelf {
		if sent, err := p.sentByPartyline(ctx, chat.ID, m.MessageID); err != nil || sent {
			return err
		}
	}
	if folds(m.Kind) {
		p.fold(ctx, src, chat, m)
		return nil
	}
	p.keep(ctx, src, conn, chat, m, "", nil)
	return nil
}

// keep adds what a send put on the platform to the chat's history once it is sent (CH-02): a message
// under the send's id, from the account, with its first platform id, the file it carried named (it was
// not kept) and the message it quotes when the history has it; a reaction on the message it targets. A
// send that failed was never in the chat and is not kept. Failures are logged without content: the
// send is over either way (CH-04).
func (s *Sends) keep(ctx context.Context, send db.PlatformSend, platformID string, d delivery) {
	if s.d.History == nil {
		return
	}
	var err error
	if d.react != nil {
		_, err = s.d.History.React(ctx, send.ConnectorID, send.ChatID, d.react.TargetID, platform.HistoryReaction{
			FromSelf: true, Emoji: d.react.Emoji, At: s.d.Now(),
		})
	} else {
		m := platform.HistoryMessage{
			ID: send.ID, ConnectorID: send.ConnectorID, ChatID: send.ChatID, ExternalID: platformID,
			FromSelf: true, Kind: dto.MessageKindText, Text: send.Text, At: s.d.Now(),
		}
		if d.file != nil {
			m.Kind = fileKind(d.file.Mime)
			m.Attachments = []platform.HistoryAttachment{{
				Name: d.file.Name, Mime: outFile(d.file).Mime, Size: d.file.Size, Status: dto.AttachmentStatusUnavailable,
			}}
		}
		if send.ReplyToExternalID != "" {
			id, text, found, ferr := s.d.History.Find(ctx, send.ConnectorID, send.ChatID, send.ReplyToExternalID)
			if ferr != nil {
				err = ferr
			} else if found {
				m.ReplyTo, m.Quoted = id, QuotePreview(dto.MessageKindText, text, "")
			}
		}
		if err == nil {
			_, err = s.d.History.Record(ctx, m)
		}
	}
	if err != nil {
		s.d.Logger.Warn("connectors: chat history failed", "connector_id", send.ConnectorID, "send_id", send.ID, "err", err)
	}
}

// fileKind is the message kind of a file sent with the given type.
func fileKind(mime string) dto.MessageKind {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return dto.MessageKindImage
	case strings.HasPrefix(mime, "video/"):
		return dto.MessageKindVideo
	case strings.HasPrefix(mime, "audio/"):
		return dto.MessageKindAudio
	}
	return dto.MessageKindDocument
}
