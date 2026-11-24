package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/connectors"
)

// The events of evolution-go an instance must be subscribed to (CH-10): MESSAGE for what arrives on the
// number, HISTORY_SYNC for the chats' past while the connection keeps its history (CH-09).
const (
	eventMessage     = "MESSAGE"
	eventHistorySync = "HISTORY_SYNC"
)

// Bounds of the history (CH-09).
const (
	// minHistoryFrame is the smallest read limit of the WebSocket: a chunk of WhatsApp's history sync is
	// one frame, which can be tens of MB whatever the upload limit.
	minHistoryFrame = 128 << 20
	// maxHistoryInbox bounds the history frames waiting for the history worker, the one it works on
	// included; historyPutWait is how long the reader waits for room before it drops a history frame,
	// so that a slow sync never stops the live messages behind it for long.
	maxHistoryInbox = 128 << 20
	historyPutWait  = 15 * time.Second
	// subscribeRetry is how long the connector waits before it asks evolution-go again for the same
	// subscription that did not take.
	subscribeRetry = 10 * time.Minute
)

// historyMarker is how the event name of a history sync reads in a raw frame: the payload is a JSON
// string inside the frame, so its own quotes are escaped once. A quote of a message's text is escaped
// twice, so no text can look like it.
var historyMarker = []byte(`\"event\":\"HistorySync\"`)

// isHistoryFrame tells whether a raw frame looks like a history sync; handleHistory checks it.
func isHistoryFrame(data []byte) bool { return bytes.Contains(data, historyMarker) }

// historyEvent is a HistorySync event of evolution-go: whatsmeow's events.HistorySync, whose Data is
// the protobuf HistorySync serialized with its JSON names. Each conversation is decoded when its turn
// comes.
type historyEvent struct {
	Event        string `json:"event"`
	InstanceName string `json:"instanceName"`
	Data         struct {
		Data *struct {
			Conversations []json.RawMessage `json:"conversations"`
		} `json:"Data"`
	} `json:"data"`
}

// historyConversation is a chat of a history sync with its messages.
type historyConversation struct {
	ID          string `json:"ID"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Messages    []struct {
		Message *webMessage `json:"message"`
	} `json:"messages"`
}

// webMessage is a WebMessageInfo: a message as WhatsApp stores it.
type webMessage struct {
	Key struct {
		RemoteJID   string `json:"remoteJID"`
		FromMe      bool   `json:"fromMe"`
		ID          string `json:"ID"`
		Participant string `json:"participant"`
	} `json:"key"`
	Message          json.RawMessage `json:"message"`
	MessageTimestamp json.RawMessage `json:"messageTimestamp"`
	Participant      string          `json:"participant"`
	PushName         string          `json:"pushName"`
}

// ParseHistory decodes a HistorySync payload of the instance and calls fn with each chat message of it,
// a conversation at a time and oldest first within it, marked Backfill and with its files named only
// (CH-09). Messages that are no chat messages (protocol, status broadcasts) are left out. It returns
// ErrNotMessage for a payload that is no history sync of the instance.
func ParseHistory(payload []byte, instance string, fn func(Parsed)) error {
	var ev historyEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("decode history: %w", err)
	}
	if ev.Event != "HistorySync" || ev.InstanceName != instance || ev.Data.Data == nil {
		return ErrNotMessage
	}
	for i, raw := range ev.Data.Data.Conversations {
		ev.Data.Data.Conversations[i] = nil // what was handled can go
		var conv historyConversation
		if err := json.Unmarshal(raw, &conv); err != nil {
			continue
		}
		for _, p := range conversationMessages(instance, conv) {
			fn(p)
		}
	}
	return nil
}

// conversationMessages parses the messages of a conversation, oldest first.
func conversationMessages(instance string, conv historyConversation) []Parsed {
	type timed struct {
		at int64
		p  Parsed
	}
	var out []timed
	for _, m := range conv.Messages {
		w := m.Message
		if w == nil || w.Key.ID == "" {
			continue
		}
		chat := conv.ID
		if chat == "" {
			chat = w.Key.RemoteJID
		}
		group := strings.HasSuffix(BareJID(chat), "@g.us")
		sender := ""
		switch {
		case group:
			sender = w.Key.Participant
			if sender == "" {
				sender = w.Participant
			}
		case !w.Key.FromMe:
			sender = chat
		}
		ts := num(w.MessageTimestamp)
		info := Info{
			ID: w.Key.ID, Chat: chat, Sender: sender, IsFromMe: w.Key.FromMe, IsGroup: group,
			PushName: w.PushName,
		}
		if ts > 0 {
			info.Timestamp = time.Unix(ts, 0).UTC().Format(time.RFC3339)
		}
		ev := Event{Event: "Message", InstanceName: instance}
		ev.Data.Info, ev.Data.Message = info, w.Message
		p, err := Parse(ev)
		if err != nil {
			continue
		}
		p.Inbound.Backfill = true
		if group {
			p.Inbound.ChatTitle = strings.TrimSpace(conv.Name)
		} else if name := strings.TrimSpace(conv.DisplayName); name != "" && p.Inbound.FromSelf {
			p.Inbound.ChatTitle = name
		}
		if p.Media != nil { // the history names the file; nothing downloads it
			p.Inbound.Media = []connectors.Media{{Name: p.Media.Name, Mime: p.Media.Mime, Size: p.Media.Size}}
		}
		out = append(out, timed{at: ts, p: p})
	}
	slices.SortStableFunc(out, func(a, b timed) int {
		switch {
		case a.at < b.at:
			return -1
		case a.at > b.at:
			return 1
		}
		return 0
	})
	parsed := make([]Parsed, len(out))
	for i, t := range out {
		parsed[i] = t.p
	}
	return parsed
}

// handleHistory hands the messages of a history sync frame to h, never delivered as new messages
// (CH-09); a connection that keeps no history skips it whole. A frame that turns out not to be a
// history sync is handled as any other frame.
func (c *Connector) handleHistory(ctx, life context.Context, h connectors.Handler, data []byte) {
	if !c.keeps(ctx) {
		return
	}
	var f Frame
	if err := json.Unmarshal(data, &f); err != nil {
		c.log.Warn("whatsapp: undecodable frame")
		return
	}
	payload := []byte(f.Payload)
	f.Payload = ""
	n := 0
	err := ParseHistory(payload, c.instance, func(p Parsed) {
		if ctx.Err() == nil {
			h.Inbound(ctx, p.Inbound)
			n++
		}
	})
	switch {
	case errors.Is(err, ErrNotMessage):
		c.handle(ctx, life, h, data)
	case err != nil:
		c.log.Warn("whatsapp: undecodable history sync", "instance", c.instance)
	default:
		c.log.Info("whatsapp: history sync received", "instance", c.instance, "messages", n)
	}
}

// keeps tells whether the connection keeps its chat history now.
func (c *Connector) keeps(ctx context.Context) bool {
	return c.keepsHistory != nil && c.keepsHistory(ctx)
}

// FetchHistory asks WhatsApp, through evolution-go's POST /chat/history-sync, for up to r.Count
// messages of the chat older than the anchor (CH-08). The phone answers later with a HistorySync event
// of type ON_DEMAND, which comes as any history sync (CH-09). The instance must be subscribed to
// HISTORY_SYNC; the connector subscribes it first when it is not (CH-10).
func (c *Connector) FetchHistory(ctx context.Context, r connectors.HistoryRequest) error {
	inst, jid, ok := SplitChatID(r.ChatID)
	if !ok || !c.uses(inst) {
		return ErrOtherInstance
	}
	insts, err := c.refreshInstances(ctx)
	if err != nil {
		return err
	}
	cur, found := findInstance(insts, c.instance)
	if !found || cur.Connected == nil || !bool(*cur.Connected) {
		return errors.New("the WhatsApp number is not connected")
	}
	if !bool(cur.WebsocketEnable) || !slices.Contains(eventsOf(cur.Events), eventHistorySync) {
		if err := c.subscribe(ctx, cur, true); err != nil {
			return err
		}
	}
	body := map[string]any{
		"messageInfo": map[string]any{
			"Chat": jid, "IsFromMe": r.AnchorFromSelf, "IsGroup": strings.HasSuffix(jid, "@g.us"),
			"ID": r.AnchorID, "Timestamp": r.AnchorAt.UTC().Format(time.RFC3339),
		},
		"count": r.Count,
	}
	return c.instanceCall(ctx, inst, http.MethodPost, "/chat/history-sync", body, nil)
}

func findInstance(insts []instance, name string) (instance, bool) {
	for _, inst := range insts {
		if inst.Name == name {
			return inst, true
		}
	}
	return instance{}, false
}

// eventsOf splits evolution-go's list of the events an instance is subscribed to.
func eventsOf(list string) []string {
	var out []string
	for e := range strings.SplitSeq(list, ",") {
		if e = strings.ToUpper(strings.TrimSpace(e)); e != "" && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out
}

// wantedEvents is the subscription the connection needs (CH-10): the instance's own list, which other
// consumers of evolution-go may rely on, with MESSAGE, and with HISTORY_SYNC exactly while the connection
// keeps its history.
func wantedEvents(current string, history bool) []string {
	events := slices.DeleteFunc(eventsOf(current), func(e string) bool { return e == eventHistorySync || e == "ALL" })
	if !slices.Contains(events, eventMessage) {
		events = append([]string{eventMessage}, events...)
	}
	if history {
		events = append(events, eventHistorySync)
	}
	return events
}

// ensureEvents keeps the connection's instance sending what Partyline needs on the WebSocket (CH-10):
// the WebSocket on and the events of wantedEvents. A re-pair through a QR code turns the WebSocket off,
// and the messages then stop arriving without a word. Only a number that is connected is changed:
// evolution-go's POST /instance/connect updates a running instance in place, but starts a QR pairing for
// one that is not logged in, which nothing must start on its own. Failures are logged; the connection
// goes on.
func (c *Connector) ensureEvents(ctx context.Context, insts []instance) {
	cur, found := findInstance(insts, c.instance)
	if !found || cur.Connected == nil || !bool(*cur.Connected) {
		return
	}
	history := c.keeps(ctx)
	if bool(cur.WebsocketEnable) && slices.Equal(eventsOf(cur.Events), wantedEvents(cur.Events, history)) {
		return
	}
	if err := c.subscribe(ctx, cur, history); err != nil && ctx.Err() == nil {
		c.log.Warn("whatsapp: could not subscribe the instance to its events", "instance", c.instance, "err", err)
	}
}

// subscribe sets the instance's subscription to wantedEvents with the WebSocket on, through POST
// /instance/connect, keeping its webhook, RabbitMQ and NATS settings as they are (the call sets them
// all). The same request for the same state of the instance is not made again within subscribeRetry,
// so an evolution-go that does not take it is not asked every minute; a state that changed (a re-pair
// turned the WebSocket off again) is asked at once.
func (c *Connector) subscribe(ctx context.Context, cur instance, history bool) error {
	events := wantedEvents(cur.Events, history)
	key := fmt.Sprintf("%s|%t|%s", cur.Events, bool(cur.WebsocketEnable), strings.Join(events, ","))
	c.mu.Lock()
	recent := c.subscribed == key && c.now().Sub(c.subscribedAt) < subscribeRetry
	c.mu.Unlock()
	if recent {
		return nil
	}
	body := map[string]any{
		"subscribe": events, "websocketEnable": "enabled", "webhookUrl": cur.Webhook,
		"rabbitmqEnable": string(cur.RabbitmqEnable), "natsEnable": string(cur.NatsEnable),
	}
	if err := c.instanceCall(ctx, c.instance, http.MethodPost, "/instance/connect", body, nil); err != nil {
		return err
	}
	c.mu.Lock()
	c.subscribed, c.subscribedAt = key, c.now()
	c.mu.Unlock()
	c.log.Info("whatsapp: instance subscribed to its events", "instance", c.instance, "history_sync", history)
	return nil
}

// setting reads an evolution-go setting that it sends as a string ("enabled") or, in some versions, as
// a boolean, and gives it back as the string POST /instance/connect takes.
type setting string

func (s *setting) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		*s = setting(str)
		return nil
	}
	var on bool
	if json.Unmarshal(b, &on) == nil && on {
		*s = "enabled"
		return nil
	}
	*s = ""
	return nil
}
