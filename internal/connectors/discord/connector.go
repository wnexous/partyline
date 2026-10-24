// Package discord is the Discord connector: a bot on the Discord gateway (discordgo).
package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// KindName is the connector kind name.
const KindName = "discord"

// MaxText is the longest message content in characters (DC-02).
const MaxText = 2000

// intents are the gateway events the bot subscribes to.
const intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages |
	discordgo.IntentsMessageContent

// Kind returns the Discord connector kind.
func Kind() connectors.Kind {
	return connectors.Kind{
		Name: KindName, Label: "Discord bot", CanListChats: true, CanSendFiles: true,
		Fields: []connectors.Field{
			{Name: "bot_token", Label: "Bot token", Type: dto.FieldTypeSecret, Required: true,
				Help: "Developer Portal → Bot; enable the Message Content intent"},
		},
		Address: address,
		New: func(cfg connectors.Config, d connectors.Deps) (connectors.Connector, error) {
			return New(cfg, d)
		},
	}
}

// session is the part of *discordgo.Session the connector uses; tests pass a fake.
type session interface {
	Open() error
	Close() error
	AddHandler(handler any) func()
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)
	UserGuilds(limit int, beforeID, afterID string, withCounts bool, options ...discordgo.RequestOption) ([]*discordgo.UserGuild, error)
	GuildChannels(guildID string, options ...discordgo.RequestOption) ([]*discordgo.Channel, error)
	Channel(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	Guild(guildID string, options ...discordgo.RequestOption) (*discordgo.Guild, error)
	MessageReactionAdd(channelID, messageID, emojiID string, options ...discordgo.RequestOption) error
}

var _ session = (*discordgo.Session)(nil)

// Connector is one Discord bot.
type Connector struct {
	s         session
	userAgent string
	log       *slog.Logger
	client    *http.Client

	mu     sync.Mutex
	run    *running
	titles map[string]string // channel id → title
	self   string            // the bot's user id, once the gateway said it (READY)
}

// running is the handler and context of the current Run.
type running struct {
	ctx context.Context
	h   connectors.Handler
}

var (
	_ connectors.Connector  = (*Connector)(nil)
	_ connectors.ChatLister = (*Connector)(nil)
)

// New returns a connector for a validated config.
func New(cfg connectors.Config, d connectors.Deps) (*Connector, error) {
	s, err := discordgo.New("Bot " + cfg.String("bot_token"))
	if err != nil {
		return nil, errors.New("invalid bot token")
	}
	s.Identify.Intents = intents
	if d.UserAgent != "" {
		s.UserAgent = d.UserAgent
	}
	return newWithSession(s, d), nil
}

func newWithSession(s session, d connectors.Deps) *Connector {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	c := &Connector{s: s, userAgent: d.UserAgent, log: log, client: d.MediaClient(60 * time.Second), titles: map[string]string{}}
	s.AddHandler(c.onMessage)
	s.AddHandler(c.onReady)
	return c
}

// Run opens the gateway and hands messages to h until ctx ends; discordgo reconnects by itself.
func (c *Connector) Run(ctx context.Context, h connectors.Handler) error {
	c.mu.Lock()
	c.run = &running{ctx: ctx, h: h}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.run = nil
		c.mu.Unlock()
	}()
	if err := c.s.Open(); err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	h.Status(connectors.StatusConnected, "")
	<-ctx.Done()
	_ = c.s.Close()
	return ctx.Err()
}

// onReady remembers the bot's user id, to tell its own messages (ACC-05), and reports its
// @username as the connector's account (ACC-07); the gateway sends READY again after a reconnect.
func (c *Connector) onReady(_ *discordgo.Session, r *discordgo.Ready) {
	if r == nil || r.User == nil {
		return
	}
	c.mu.Lock()
	if r.User.ID != "" {
		c.self = r.User.ID
	}
	run := c.run
	c.mu.Unlock()
	if run != nil && r.User.Username != "" {
		run.h.Account("@" + r.User.Username)
	}
}

// onMessage emits messages of users (DC-01) and of the bot itself (ACC-05); messages of other
// bots are ignored. The chat is the channel.
func (c *Connector) onMessage(_ *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Message == nil || m.Author == nil {
		return
	}
	c.mu.Lock()
	run, self := c.run, c.self
	c.mu.Unlock()
	fromSelf := self != "" && m.Author.ID == self
	if run == nil || (m.Author.Bot && !fromSelf) {
		return
	}
	in := connectors.Inbound{
		ChatID: m.ChannelID, ChatKind: "channel", MessageID: m.ID, FromSelf: fromSelf,
		SenderID: m.Author.ID, SenderName: displayName(m), Kind: string(dto.MessageKindText), Text: m.Content, At: m.Timestamp,
		MentionsAccount: mentions(m.Mentions, self),
	}
	if m.GuildID == "" {
		in.ChatKind = "dm"
		if !fromSelf { // the title of a DM is the other party's name, which the bot's own message does not carry
			in.ChatTitle = in.SenderName
		}
	} else {
		in.ChatTitle = c.title(m.GuildID, m.ChannelID)
	}
	if m.MessageReference != nil {
		in.ReplyToID = m.MessageReference.MessageID
	}
	for i, a := range m.Attachments {
		if i == 0 {
			in.Kind = string(kindOf(a.ContentType))
		}
		in.Media = append(in.Media, connectors.Media{
			Name: a.Filename, Mime: a.ContentType, Size: int64(a.Size), Open: c.opener(a.URL),
		})
	}
	run.h.Inbound(run.ctx, in)
}

// mentions tells whether the message mentions the bot (CID-12): its user id is in mentions.
// An @everyone, @here or role mention puts no user in mentions, so it is not a mention of the
// account.
func mentions(users []*discordgo.User, self string) bool {
	if self == "" {
		return false
	}
	for _, u := range users {
		if u != nil && u.ID == self {
			return true
		}
	}
	return false
}

func displayName(m *discordgo.MessageCreate) string {
	if m.Member != nil && m.Member.Nick != "" {
		return m.Member.Nick
	}
	if m.Author.GlobalName != "" {
		return m.Author.GlobalName
	}
	return m.Author.Username
}

func kindOf(mimeType string) dto.MessageKind {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return dto.MessageKindImage
	case strings.HasPrefix(mimeType, "video/"):
		return dto.MessageKindVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return dto.MessageKindAudio
	}
	return dto.MessageKindDocument
}

// title returns "Guild #channel" for a guild channel, the title ListChats gives it
// (DC-01, DC-03), cached; empty when unknown, which keeps the stored title.
func (c *Connector) title(guildID, channelID string) string {
	c.mu.Lock()
	t, ok := c.titles[channelID]
	c.mu.Unlock()
	if ok {
		return t
	}
	ch, err := c.s.Channel(channelID)
	if err != nil || ch == nil {
		return ""
	}
	g, err := c.s.Guild(guildID)
	if err != nil || g == nil {
		return ""
	}
	t = channelTitle(g.Name, ch.Name)
	c.mu.Lock()
	c.titles[channelID] = t
	c.mu.Unlock()
	return t
}

// opener downloads an attachment from its CDN URL.
func (c *Connector) opener(rawURL string) func(ctx context.Context) (io.ReadCloser, error) {
	return func(ctx context.Context) (io.ReadCloser, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, errors.New("attachment: invalid url")
		}
		req.Header.Set("User-Agent", c.userAgent)
		resp, err := c.client.Do(req)
		if err != nil {
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return nil, fmt.Errorf("attachment download: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("attachment download: status %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
}

// Send posts the text in parts of at most 2,000 characters, the first referencing the
// replied message (DC-02), and attaches the files to the last part. Mentions in the text
// are not resolved, so a bridged message cannot ping anyone. It returns the first part's id and
// tells m.Created the id of every part, as the gateway echoes each of them (ACC-05).
func (c *Connector) Send(ctx context.Context, m connectors.Outbound) (string, error) {
	parts := connectors.SplitText(connectors.AuthorLine(m.Signature(), "**", "**: ", m.Text), MaxText)
	if len(parts) == 0 && len(m.Files) > 0 {
		parts = []string{""}
	}
	first := ""
	for i, part := range parts {
		msg := &discordgo.MessageSend{Content: part, AllowedMentions: &discordgo.MessageAllowedMentions{}}
		if i == 0 && m.ReplyToID != "" {
			fail := false
			msg.Reference = &discordgo.MessageReference{MessageID: m.ReplyToID, ChannelID: m.ChatID, FailIfNotExists: &fail}
		}
		var closers []io.Closer
		if i == len(parts)-1 {
			for _, f := range m.Files {
				r, err := f.Open()
				if err != nil {
					closeAll(closers)
					return "", fmt.Errorf("open attachment: %w", err)
				}
				closers = append(closers, r)
				msg.Files = append(msg.Files, &discordgo.File{Name: f.Name, ContentType: f.Mime, Reader: r})
			}
		}
		sent, err := c.s.ChannelMessageSendComplex(m.ChatID, msg, discordgo.WithContext(ctx))
		closeAll(closers)
		if err != nil {
			return "", fmt.Errorf("send: %w", err)
		}
		if sent == nil {
			continue
		}
		if first == "" {
			first = sent.ID
		}
		if m.Created != nil {
			m.Created(sent.ID)
		}
	}
	return first, nil
}

// channelTitle is the chat title of a guild channel.
func channelTitle(guild, channel string) string { return guild + " #" + channel }

func closeAll(cs []io.Closer) {
	for _, c := range cs {
		_ = c.Close()
	}
}

// ListChats lists the text channels of every guild the bot is in (DC-03).
func (c *Connector) ListChats(ctx context.Context) ([]connectors.ChatInfo, error) {
	guilds, err := c.s.UserGuilds(200, "", "", false, discordgo.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list guilds: %w", err)
	}
	var out []connectors.ChatInfo
	for _, g := range guilds {
		channels, err := c.s.GuildChannels(g.ID, discordgo.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("list channels: %w", err)
		}
		for _, ch := range channels {
			if ch.Type != discordgo.ChannelTypeGuildText && ch.Type != discordgo.ChannelTypeGuildNews {
				continue
			}
			out = append(out, connectors.ChatInfo{ID: ch.ID, Title: channelTitle(g.Name, ch.Name), Kind: "channel"})
		}
	}
	return out, nil
}
