package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"
)

// reply is a reply to the message ref, as the gateway sends it: type 19, a default reference and the
// referenced message along.
func reply(id, content string, ref *discordgo.Message) *discordgo.Message {
	m := userMessage(id, content)
	m.Type = discordgo.MessageTypeReply
	m.MessageReference = &discordgo.MessageReference{MessageID: ref.ID, ChannelID: channelID, GuildID: guildID}
	m.ReferencedMessage = ref
	return m
}

// QC-01, QC-02, QC-03: a reply quotes the referenced message: its id, a preview of its content or of
// its attachment and caption, and whether the bot wrote it. A deleted referenced message still names
// the quote, and a forward references a message without quoting it.
func TestQC02RepliesQuoteTheReferencedMessage(t *testing.T) {
	f := newFakeSession()
	_, rec := start(t, f)
	bot := &discordgo.User{ID: botID, Username: "atlas-bot", Bot: true}
	f.emitReady(bot)
	require.Equal(t, "@atlas-bot", rec.NextAccount(t))

	image := &discordgo.Message{ID: "M-IMG", Author: bot, Content: "option B",
		Attachments: []*discordgo.MessageAttachment{{Filename: "b.png", ContentType: "image/png"}}}
	f.emit(reply("R1", "that one", image))
	bare := &discordgo.Message{ID: "M-IMG2", Author: bot, Attachments: []*discordgo.MessageAttachment{{Filename: "c.png", ContentType: "image/png"}}}
	f.emit(reply("R2", "or this", bare))
	doc := &discordgo.Message{ID: "M-DOC", Author: &discordgo.User{ID: userID}, Attachments: []*discordgo.MessageAttachment{{Filename: "plan.pdf", ContentType: "application/pdf"}}}
	f.emit(reply("R3", "read it", doc))
	text := &discordgo.Message{ID: "M-TXT", Author: &discordgo.User{ID: userID}, Content: strings.Repeat("c", 220)}
	f.emit(reply("R4", "agreed", text))
	deleted := reply("R5", "?", &discordgo.Message{ID: "M-GONE"})
	deleted.ReferencedMessage = nil
	f.emit(deleted)
	forward := userMessage("R6", "")
	forward.Content = "fwd"
	forward.MessageReference = &discordgo.MessageReference{Type: discordgo.MessageReferenceTypeForward, MessageID: "M-FWD", ChannelID: channelID}
	f.emit(forward)

	in := rec.NextInbound(t)
	require.Equal(t, "M-IMG", in.ReplyToID)
	require.Equal(t, "M-IMG", in.Quote.ID)
	require.Equal(t, "[image] option B", in.Quote.Preview)
	require.True(t, in.Quote.FromSelf, "the bot wrote it")

	in = rec.NextInbound(t)
	require.Equal(t, "[image]", in.Quote.Preview, "an image without a caption")
	require.True(t, in.Quote.FromSelf)

	in = rec.NextInbound(t)
	require.Equal(t, "[document: plan.pdf]", in.Quote.Preview)
	require.False(t, in.Quote.FromSelf, "a person wrote it")

	require.Equal(t, strings.Repeat("c", 199)+"…", rec.NextInbound(t).Quote.Preview, "at most 200 characters")

	in = rec.NextInbound(t)
	require.Equal(t, "M-GONE", in.Quote.ID, "QC-04: the quote is named without a preview")
	require.Empty(t, in.Quote.Preview)

	in = rec.NextInbound(t)
	require.Equal(t, "M-FWD", in.ReplyToID)
	require.Nil(t, in.Quote, "a forward quotes nothing")
}
