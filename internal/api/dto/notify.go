package dto

// NotifyOption is a chat that can receive a copy of the DMs AIs write to the caller (NOTI-01):
// the DM chat of an account the caller linked, on a connector they own or may use.
type NotifyOption struct {
	ChatID string `json:"chat_id"`
	// Title is the chat's title on the platform.
	Title     string       `json:"title"`
	Connector ConnectorRef `json:"connector"`
	// Account is the linked account the chat belongs to, as the identities page shows it.
	Account string `json:"account"`
}

// SetNotifyRequest is the body of PUT /me/notify: the chat to notify, one of the chats of GET
// /me/notify-options, or null for none.
type SetNotifyRequest struct {
	ChatID *string `json:"chat_id" tstype:"string | null"`
}
