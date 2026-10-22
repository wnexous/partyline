package conversations

import (
	"context"
	"database/sql"
	"slices"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// NotifyOptions lists the chats that can receive a copy of the DMs AIs write to the user
// (NOTI-01): the DM chats of the accounts the user linked, on the connectors they own or may use.
// It never lists an account another user linked.
func (s *Service) NotifyOptions(ctx context.Context, userID string) ([]dto.NotifyOption, error) {
	rows, err := s.store.R().ListNotifyOptions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]dto.NotifyOption, len(rows))
	for i, r := range rows {
		out[i] = dto.NotifyOption{
			ChatID: r.Chat.ID, Title: r.Chat.Title, Account: r.Account,
			Connector: dto.ConnectorRef{ID: r.Connector.ID, Kind: r.Connector.Kind, Name: r.Connector.Name},
		}
	}
	return out, nil
}

// SetNotify chooses the chat that receives a copy of the DMs AIs write to the user, or none when
// chatID is nil (NOTI-01). The chat must be one of the user's NotifyOptions: any other is a
// validation error on chat_id and changes nothing. The check and the write are one transaction.
func (s *Service) SetNotify(ctx context.Context, userID string, chatID *string) error {
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		var chosen sql.NullString
		if chatID != nil {
			ok, err := NotifyUsable(ctx, q, userID, *chatID)
			if err != nil {
				return err
			}
			if !ok {
				return &validation.Error{Fields: map[string]string{"chat_id": "must be one of your notify options"}}
			}
			chosen = sql.NullString{String: *chatID, Valid: true}
		}
		return q.SetUserNotifyChat(ctx, db.SetUserNotifyChatParams{NotifyChatID: chosen, ID: userID})
	})
}

// NotifyUsable says whether the chat is one of the user's notify options now: the account is still
// linked to them and they still own or may use the connector. The sender asks it when a copy
// is due, since both can change after the user chose.
func NotifyUsable(ctx context.Context, q *db.Queries, userID, chatID string) (bool, error) {
	rows, err := q.ListNotifyOptions(ctx, userID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(rows, func(r db.ListNotifyOptionsRow) bool { return r.Chat.ID == chatID }), nil
}
