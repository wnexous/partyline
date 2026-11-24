package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// replyTo is the reply_to_message of an update: a message of the group written by from.
func replyTo(messageID int, from map[string]any, extra map[string]any) map[string]any {
	msg := map[string]any{
		"message_id": messageID, "date": 1792497500, "from": from,
		"chat": map[string]any{"id": groupChat, "type": "supergroup", "title": "Test Group"},
	}
	for k, v := range extra {
		msg[k] = v
	}
	return msg
}

// QC-01, QC-02, QC-03: a reply quotes the message of reply_to_message: its id, a preview of its text
// or of its media and caption, and whether the bot wrote it. A message of a forum topic names the
// topic's opening message without quoting it, and a message that answers nothing quotes nothing.
func TestQC02RepliesQuoteTheRepliedMessage(t *testing.T) {
	f := newFakeBotAPI(t)
	bot := map[string]any{"id": botID, "is_bot": true, "first_name": "Test Bot"}
	person := map[string]any{"id": 5550002, "is_bot": false, "first_name": "Other"}
	f.queue(
		groupMessage(1, 30, map[string]any{"text": "that one", "reply_to_message": replyTo(10, bot, map[string]any{
			"caption": "option B", "photo": []map[string]any{{"file_id": "small", "file_size": 100}},
		})}),
		groupMessage(2, 31, map[string]any{"text": "yes", "reply_to_message": replyTo(11, person, map[string]any{"text": "lunch at noon?"})}),
		groupMessage(3, 32, map[string]any{"text": "ok", "reply_to_message": replyTo(12, bot, map[string]any{
			"photo": []map[string]any{{"file_id": "small", "file_size": 100}},
		})}),
		groupMessage(4, 33, map[string]any{"text": "got it", "reply_to_message": replyTo(13, person, map[string]any{
			"document": map[string]any{"file_id": "doc", "file_name": "plan.pdf"},
		})}),
		groupMessage(5, 34, map[string]any{"text": "long", "reply_to_message": replyTo(14, person, map[string]any{"text": strings.Repeat("b", 250)})}),
		groupMessage(6, 35, map[string]any{"text": "in a topic", "is_topic_message": true, "reply_to_message": replyTo(15, person, map[string]any{
			"forum_topic_created": map[string]any{"name": "Topic"},
		})}),
		groupMessage(7, 36, map[string]any{"text": "plain"}),
	)
	rec := start(t, newConnector(t, f))

	in := rec.NextInbound(t)
	require.Equal(t, "10", in.ReplyToID)
	require.Equal(t, "10", in.Quote.ID)
	require.Equal(t, "[image] option B", in.Quote.Preview)
	require.True(t, in.Quote.FromSelf, "the bot wrote it")

	in = rec.NextInbound(t)
	require.Equal(t, "lunch at noon?", in.Quote.Preview)
	require.False(t, in.Quote.FromSelf)

	require.Equal(t, "[image]", rec.NextInbound(t).Quote.Preview, "an image without a caption")
	require.Equal(t, "[document: plan.pdf]", rec.NextInbound(t).Quote.Preview)
	require.Equal(t, strings.Repeat("b", 199)+"…", rec.NextInbound(t).Quote.Preview, "at most 200 characters")

	in = rec.NextInbound(t)
	require.Equal(t, "15", in.ReplyToID)
	require.Nil(t, in.Quote, "a topic's opening message is not quoted")
	require.Nil(t, rec.NextInbound(t).Quote)
}
