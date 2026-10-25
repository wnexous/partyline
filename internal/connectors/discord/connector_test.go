package discord

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
)

// Synthetic ids.
const (
	guildID   = "100000000000000001"
	channelID = "200000000000000001"
	userID    = "300000000000000001"
	wait      = 30 * time.Second
)

// sent is one ChannelMessageSendComplex call with its files read.
type sent struct {
	ChannelID string
	Msg       *discordgo.MessageSend
	Files     map[string]string
}

// fakeSession stands in for *discordgo.Session.
type fakeSession struct {
	mu       sync.Mutex
	handlers []func(*discordgo.Session, *discordgo.MessageCreate)
	readies  []func(*discordgo.Session, *discordgo.Ready)
	openErr  error
	opened   chan struct{}
	closed   chan struct{}
	sends    []sent
	nextID   int
	// reactions records MessageReactionAdd calls; reactErr fails them.
	reactions []reaction
	reactErr  error
}

func newFakeSession() *fakeSession {
	return &fakeSession{opened: make(chan struct{}, 4), closed: make(chan struct{}, 4)}
}

func (f *fakeSession) Open() error {
	if f.openErr != nil {
		return f.openErr
	}
	f.opened <- struct{}{}
	return nil
}

func (f *fakeSession) Close() error { f.closed <- struct{}{}; return nil }

func (f *fakeSession) AddHandler(h any) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if mh, ok := h.(func(*discordgo.Session, *discordgo.MessageCreate)); ok {
		f.handlers = append(f.handlers, mh)
	}
	if rh, ok := h.(func(*discordgo.Session, *discordgo.Ready)); ok {
		f.readies = append(f.readies, rh)
	}
	return func() {}
}

// emitReady delivers the gateway's READY event naming the bot user.
func (f *fakeSession) emitReady(bot *discordgo.User) {
	f.mu.Lock()
	hs := append([]func(*discordgo.Session, *discordgo.Ready){}, f.readies...)
	f.mu.Unlock()
	for _, h := range hs {
		h(nil, &discordgo.Ready{Version: 10, SessionID: "synthetic-session", User: bot})
	}
}

func (f *fakeSession) emit(m *discordgo.Message) {
	f.mu.Lock()
	hs := append([]func(*discordgo.Session, *discordgo.MessageCreate){}, f.handlers...)
	f.mu.Unlock()
	for _, h := range hs {
		h(nil, &discordgo.MessageCreate{Message: m})
	}
}

func (f *fakeSession) ChannelMessageSendComplex(ch string, m *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	files := map[string]string{}
	for _, file := range m.Files {
		data, _ := io.ReadAll(file.Reader)
		files[file.Name] = string(data)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.sends = append(f.sends, sent{ChannelID: ch, Msg: m, Files: files})
	return &discordgo.Message{ID: "MSG" + string(rune('0'+f.nextID))}, nil
}

func (f *fakeSession) UserGuilds(int, string, string, bool, ...discordgo.RequestOption) ([]*discordgo.UserGuild, error) {
	return []*discordgo.UserGuild{{ID: guildID, Name: "Test Guild"}, {ID: "100000000000000002", Name: "Other Guild"}}, nil
}

func (f *fakeSession) GuildChannels(g string, _ ...discordgo.RequestOption) ([]*discordgo.Channel, error) {
	if g == guildID {
		return []*discordgo.Channel{
			{ID: channelID, Name: "general", Type: discordgo.ChannelTypeGuildText},
			{ID: "200000000000000002", Name: "voice", Type: discordgo.ChannelTypeGuildVoice},
			{ID: "200000000000000003", Name: "news", Type: discordgo.ChannelTypeGuildNews},
		}, nil
	}
	return []*discordgo.Channel{{ID: "200000000000000004", Name: "lobby", Type: discordgo.ChannelTypeGuildText}}, nil
}

func (f *fakeSession) Channel(id string, _ ...discordgo.RequestOption) (*discordgo.Channel, error) {
	if id == channelID {
		return &discordgo.Channel{ID: id, Name: "general", GuildID: guildID}, nil
	}
	return nil, errors.New("unknown channel")
}

func (f *fakeSession) Guild(id string, _ ...discordgo.RequestOption) (*discordgo.Guild, error) {
	if id == guildID {
		return &discordgo.Guild{ID: id, Name: "Test Guild"}, nil
	}
	return nil, errors.New("unknown guild")
}

// reaction is one MessageReactionAdd call.
type reaction struct{ ChannelID, MessageID, Emoji string }

func (f *fakeSession) MessageReactionAdd(ch, msg, emoji string, _ ...discordgo.RequestOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reactErr != nil {
		return f.reactErr
	}
	f.reactions = append(f.reactions, reaction{ChannelID: ch, MessageID: msg, Emoji: emoji})
	return nil
}

func (f *fakeSession) sent() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent(nil), f.sends...)
}

func start(t *testing.T, f *fakeSession) (*Connector, *connectortest.Recorder) {
	t.Helper()
	c := newWithSession(f, connectors.Deps{AllowPrivateNetworks: true, ID: "c1", UserAgent: "partyline/test"})
	rec := connectortest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, rec) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled, "Run returns on cancel")
		case <-time.After(wait):
			t.Error("Run did not return after cancel")
		}
		select {
		case <-f.closed:
		case <-time.After(wait):
			t.Error("the gateway was not closed")
		}
	})
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	return c, rec
}

func userMessage(id, content string) *discordgo.Message {
	return &discordgo.Message{
		ID: id, ChannelID: channelID, GuildID: guildID, Content: content,
		Author:    &discordgo.User{ID: userID, Username: "testperson", GlobalName: "Test Person"},
		Timestamp: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}
}

// panickingHandler is a Handler whose message and account calls panic, like a bug in the code behind them.
type panickingHandler struct{ *connectortest.Recorder }

func (panickingHandler) Inbound(context.Context, connectors.Inbound) { panic("pipeline bug") }
func (panickingHandler) Account(string)                              { panic("account bug") }

// CON-15: discordgo runs each handler in a goroutine of its own and does not recover, so a panic in
// one would end the whole process. The connector recovers it and ends Run with it instead, for the
// supervisor to show the error and start the connector again.
func TestAPanicInAGatewayHandlerEndsRun(t *testing.T) {
	bot := &discordgo.User{ID: botID, Username: "atlas-bot", Discriminator: "0", Bot: true}
	for name, trigger := range map[string]func(*fakeSession){
		"message": func(f *fakeSession) { f.emit(userMessage("M1", "hello")) },
		"ready":   func(f *fakeSession) { f.emitReady(bot) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeSession()
			c := newWithSession(f, connectors.Deps{AllowPrivateNetworks: true, ID: "c1", UserAgent: "partyline/test"})
			h := panickingHandler{connectortest.NewRecorder()}
			done := make(chan error, 1)
			go func() { done <- c.Run(context.Background(), h) }()
			require.Equal(t, connectors.StatusConnected, h.NextStatus(t))

			trigger(f)

			select {
			case err := <-done:
				require.ErrorIs(t, err, connectors.ErrPanic)
			case <-time.After(wait):
				t.Fatal("Run did not end")
			}
			select {
			case <-f.closed:
			case <-time.After(wait):
				t.Error("the gateway was not closed")
			}
		})
	}
}

// ACC-07: the gateway's READY event names the bot; its @username is the account label, a
// READY after a reconnect reports it again, and a READY without a username reports nothing.
func TestReadyReportsTheBotAccount(t *testing.T) {
	f := newFakeSession()
	_, rec := start(t, f)
	require.True(t, rec.NoAccount(), "nothing before the gateway says who the bot is")

	f.emitReady(&discordgo.User{ID: "300000000000000077", Username: "atlas-bot", Discriminator: "0", Bot: true})
	require.Equal(t, "@atlas-bot", rec.NextAccount(t))
	f.emitReady(&discordgo.User{ID: "300000000000000077", Username: "atlas-helper", Discriminator: "0", Bot: true})
	require.Equal(t, "@atlas-helper", rec.NextAccount(t), "a renamed bot is reported again")

	f.emitReady(nil)
	f.emitReady(&discordgo.User{ID: "300000000000000077", Bot: true})
	require.True(t, rec.NoAccount(), "a READY without a bot username reports nothing")
}

const botID = "300000000000000077"

// ACC-05: once the gateway has said who the bot is, its own messages (another tool writing with
// the same token, or the echo of what Partyline sent) are emitted as messages of the account; the
// messages of other bots stay ignored, and so do bot messages before the bot is known.
func TestOwnBotMessagesAreFromSelf(t *testing.T) {
	f := newFakeSession()
	_, rec := start(t, f)
	bot := &discordgo.User{ID: botID, Username: "atlas-bot", Discriminator: "0", Bot: true}
	from := func(id, content string, author *discordgo.User) *discordgo.Message {
		m := userMessage(id, content)
		m.Author = author
		return m
	}

	f.emit(from("M0", "before READY", bot))
	f.emitReady(bot)
	require.Equal(t, "@atlas-bot", rec.NextAccount(t))
	f.emit(from("M1", "beep", &discordgo.User{ID: "300000000000000009", Username: "somebot", Bot: true}))
	f.emit(from("M2", "typed with the bot's token", bot))
	f.emit(userMessage("M3", "hello"))
	dm := from("M4", "in the bot's DM channel", bot)
	dm.GuildID, dm.ChannelID = "", "200000000000000099"
	f.emit(dm)

	in := rec.NextInbound(t)
	require.Equal(t, "M2", in.MessageID, "neither the early bot message nor another bot's was emitted")
	require.True(t, in.FromSelf)
	require.Equal(t, botID, in.SenderID)
	require.Equal(t, "typed with the bot's token", in.Text)
	require.Equal(t, channelID, in.ChatID)
	in = rec.NextInbound(t)
	require.Equal(t, "M3", in.MessageID)
	require.False(t, in.FromSelf, "a person is not the account")
	in = rec.NextInbound(t)
	require.Equal(t, "M4", in.MessageID)
	require.True(t, in.FromSelf)
	require.Equal(t, "dm", in.ChatKind)
	require.Empty(t, in.ChatTitle, "the bot's name is not the title of its DM channel")
}

// CID-12: the bot is mentioned when its user id is in the message's mentions; another user's
// mention and an @everyone alone are not mentions of the account.
func TestMentionsReportTheBot(t *testing.T) {
	f := newFakeSession()
	_, rec := start(t, f)
	bot := &discordgo.User{ID: botID, Username: "atlas-bot", Discriminator: "0", Bot: true}
	f.emitReady(bot)
	require.Equal(t, "@atlas-bot", rec.NextAccount(t))

	mentioned := userMessage("M1", "hey bot")
	mentioned.Mentions = []*discordgo.User{bot}
	f.emit(mentioned)

	other := userMessage("M2", "hey person")
	other.Mentions = []*discordgo.User{{ID: "300000000000000002", Username: "otherperson"}}
	f.emit(other)

	everyone := userMessage("M3", "@everyone deploy day")
	everyone.MentionEveryone = true
	f.emit(everyone)

	require.True(t, rec.NextInbound(t).MentionsAccount, "the bot is in mentions")
	require.False(t, rec.NextInbound(t).MentionsAccount, "another user is mentioned")
	require.False(t, rec.NextInbound(t).MentionsAccount, "only @everyone")
}

// ACC-05: a send reports the id of every message it created, as the gateway echoes each.
func TestSendReportsEveryCreatedMessage(t *testing.T) {
	f := newFakeSession()
	c := newWithSession(f, connectors.Deps{})
	var created []string
	id, err := c.Send(context.Background(), connectors.Outbound{
		ChatID: channelID, Text: strings.Repeat("y", 4500), Created: func(id string) { created = append(created, id) },
	})
	require.NoError(t, err)
	require.Equal(t, "MSG1", id)
	require.Equal(t, []string{"MSG1", "MSG2", "MSG3"}, created)

	// A send that fails after some parts reported the parts that exist.
	created = nil
	_, err = c.Send(context.Background(), connectors.Outbound{
		ChatID: channelID, Text: strings.Repeat("y", 4500), Created: func(id string) { created = append(created, id) },
		Files: []connectors.OutFile{{Name: "a.bin", Open: func() (io.ReadCloser, error) { return nil, errors.New("gone") }}},
	})
	require.ErrorContains(t, err, "open attachment")
	require.Equal(t, []string{"MSG4", "MSG5"}, created, "the parts before the failure were created")
}

// DC-01: messages of non-bot users are emitted with the channel id as chat id; bots are ignored.
func TestBotMessagesAreIgnored(t *testing.T) {
	f := newFakeSession()
	_, rec := start(t, f)

	bot := userMessage("M0", "beep")
	bot.Author = &discordgo.User{ID: "300000000000000009", Username: "somebot", Bot: true}
	f.emit(bot)
	reply := userMessage("M1", "hello")
	reply.MessageReference = &discordgo.MessageReference{MessageID: "M-ORIG", ChannelID: channelID}
	f.emit(reply)

	in := rec.NextInbound(t)
	require.Equal(t, "M1", in.MessageID, "the bot's message was not emitted")
	require.Equal(t, channelID, in.ChatID)
	require.Equal(t, "channel", in.ChatKind)
	require.Equal(t, "Test Guild #general", in.ChatTitle, "DC-01: the title ListChats gives the channel")
	require.Equal(t, userID, in.SenderID)
	require.Equal(t, "Test Person", in.SenderName)
	require.Equal(t, "hello", in.Text)
	require.Equal(t, "M-ORIG", in.ReplyToID)
	require.Equal(t, string(dto.MessageKindText), in.Kind)
	require.Equal(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), in.At)
}

func TestDirectMessage(t *testing.T) {
	f := newFakeSession()
	_, rec := start(t, f)
	dm := userMessage("M2", "psst")
	dm.GuildID, dm.ChannelID = "", "200000000000000099"
	f.emit(dm)
	in := rec.NextInbound(t)
	require.Equal(t, "dm", in.ChatKind)
	require.Equal(t, "200000000000000099", in.ChatID)
	require.Equal(t, "Test Person", in.ChatTitle)
}

// Attachments are media downloaded through Media.Open, with their known size.
func TestAttachmentsAreMedia(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "png-bytes")
	}))
	t.Cleanup(cdn.Close)
	f := newFakeSession()
	_, rec := start(t, f)
	m := userMessage("M3", "")
	m.Attachments = []*discordgo.MessageAttachment{{Filename: "a.png", ContentType: "image/png", Size: 9, URL: cdn.URL + "/a.png"}}
	f.emit(m)

	in := rec.NextInbound(t)
	require.Equal(t, string(dto.MessageKindImage), in.Kind)
	require.Len(t, in.Media, 1)
	require.Equal(t, "a.png", in.Media[0].Name)
	require.Equal(t, "image/png", in.Media[0].Mime)
	require.Equal(t, int64(9), in.Media[0].Size)
	r, err := in.Media[0].Open(context.Background())
	require.NoError(t, err)
	data, _ := io.ReadAll(r)
	_ = r.Close()
	require.Equal(t, "png-bytes", string(data))
}

// DC-02: text above 2,000 characters is split; the first part references the replied
// message; files ride on the last part.
func TestSendSplitsWithReferenceOnFirst(t *testing.T) {
	f := newFakeSession()
	c := newWithSession(f, connectors.Deps{})
	text := strings.Repeat("y", 4500)
	id, err := c.Send(context.Background(), connectors.Outbound{
		ChatID: channelID, Text: text, ReplyToID: "M-ORIG",
		Files: []connectors.OutFile{{Name: "r.pdf", Mime: "application/pdf", Size: 3, Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("pdf")), nil
		}}},
	})
	require.NoError(t, err)
	require.Equal(t, "MSG1", id)

	sends := f.sent()
	require.Len(t, sends, 3)
	joined := ""
	for i, s := range sends {
		require.Equal(t, channelID, s.ChannelID)
		require.LessOrEqual(t, len([]rune(s.Msg.Content)), MaxText)
		require.NotNil(t, s.Msg.AllowedMentions, "bridged text pings nobody")
		require.Empty(t, s.Msg.AllowedMentions.Parse)
		if i == 0 {
			require.NotNil(t, s.Msg.Reference)
			require.Equal(t, "M-ORIG", s.Msg.Reference.MessageID)
			require.Equal(t, channelID, s.Msg.Reference.ChannelID)
		} else {
			require.Nil(t, s.Msg.Reference)
		}
		joined += s.Msg.Content
	}
	require.Equal(t, text, joined)
	require.Empty(t, sends[0].Files)
	require.Equal(t, map[string]string{"r.pdf": "pdf"}, sends[2].Files)
}

func TestSendPrefixesAuthorAndSendsFilesAlone(t *testing.T) {
	f := newFakeSession()
	c := newWithSession(f, connectors.Deps{})
	_, err := c.Send(context.Background(), connectors.Outbound{ChatID: channelID, Text: "hi", AuthorName: "Agent"})
	require.NoError(t, err)
	_, err = c.Send(context.Background(), connectors.Outbound{ChatID: channelID, Files: []connectors.OutFile{{
		Name: "a.png", Mime: "image/png", Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("png")), nil },
	}}})
	require.NoError(t, err)
	sends := f.sent()
	require.Len(t, sends, 2)
	require.Equal(t, "**Agent**: hi", sends[0].Msg.Content)
	require.Nil(t, sends[0].Msg.Reference)
	require.Equal(t, map[string]string{"a.png": "png"}, sends[1].Files)
}

// ACC-03, ACC-04: a message of the account's operator carries no author line.
func TestSendLeavesTheAuthorOutForTheOperator(t *testing.T) {
	f := newFakeSession()
	c := newWithSession(f, connectors.Deps{})
	_, err := c.Send(context.Background(), connectors.Outbound{ChatID: channelID, Text: "hi", AuthorName: "Agent", Unsigned: true})
	require.NoError(t, err)
	require.Equal(t, "hi", f.sent()[0].Msg.Content)
}

// DC-03: the text channels of every guild are listed; DC-01: an inbound message of a
// listed channel carries the same title.
func TestListChats(t *testing.T) {
	f := newFakeSession()
	c, rec := start(t, f)
	chats, err := c.ListChats(context.Background())
	require.NoError(t, err)
	f.emit(userMessage("M9", "hi"))
	require.Equal(t, chats[0].Title, rec.NextInbound(t).ChatTitle)
	require.Equal(t, []connectors.ChatInfo{
		{ID: channelID, Title: "Test Guild #general", Kind: "channel"},
		{ID: "200000000000000003", Title: "Test Guild #news", Kind: "channel"},
		{ID: "200000000000000004", Title: "Other Guild #lobby", Kind: "channel"},
	}, chats, "voice channels are not chats")
}

// CON-06: a gateway failure fails Run.
func TestRunOpenFailure(t *testing.T) {
	f := newFakeSession()
	f.openErr = errors.New("websocket: close 4004: authentication failed")
	c := newWithSession(f, connectors.Deps{})
	err := c.Run(context.Background(), connectortest.NewRecorder())
	require.ErrorContains(t, err, "authentication failed")
}

func TestKindSchema(t *testing.T) {
	k := Kind()
	require.Equal(t, KindName, k.Name)
	require.True(t, k.CanListChats)
	_, errs := k.Validate(connectors.Config{})
	require.Equal(t, map[string]string{"bot_token": "required"}, errs)
	conn, err := k.New(connectors.Config{"bot_token": "synthetic.token.value"}, connectors.Deps{UserAgent: "partyline/test"})
	require.NoError(t, err)
	require.Equal(t, intents, conn.(*Connector).s.(*discordgo.Session).Identify.Intents)
}

// CON-13: by default an attachment URL on a private address is not downloaded.
func TestPrivateAttachmentURLIsRefusedByDefault(t *testing.T) {
	var hits atomic.Int32
	cdn := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(cdn.Close)
	c := newWithSession(newFakeSession(), connectors.Deps{})
	_, err := c.opener(cdn.URL + "/a.png")(context.Background())
	require.ErrorIs(t, err, safehttp.ErrBlockedAddress)
	require.Zero(t, hits.Load())
}

// S8: attachment downloads are capped at the upload limit.
func TestAttachmentDownloadIsCappedAtTheUploadLimit(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		_, _ = w.Write(bytes.Repeat([]byte("x"), n))
	}))
	t.Cleanup(cdn.Close)
	c := newWithSession(newFakeSession(), connectors.Deps{AllowPrivateNetworks: true, MaxMediaBytes: 1000})
	read := func(n int) ([]byte, error) {
		r, err := c.opener(cdn.URL + "/a.png?n=" + strconv.Itoa(n))(context.Background())
		require.NoError(t, err)
		defer func() { _ = r.Close() }()
		return io.ReadAll(r)
	}
	data, err := read(1000)
	require.NoError(t, err)
	require.Len(t, data, 1000)
	_, err = read(1001)
	require.ErrorIs(t, err, safehttp.ErrResponseTooLarge)
}
