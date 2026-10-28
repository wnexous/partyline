package webpush

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// The notifier's constants.
const (
	// notifierBuffer is the bus backlog of the notifier's subscription. It is larger than the
	// dispatcher's because one message can produce several notifications; a consumer that falls
	// behind misses events, which is fine for an ephemeral notification (PUSH-14).
	notifierBuffer = 512
	// notificationLimit and notificationWindow are the per-user notification rate (PUSH-13).
	notificationLimit  = 10
	notificationWindow = time.Minute
	// payloadVersion is the v field of every notification payload.
	payloadVersion = 1
	// groupRoomKind is the rooms.kind of a group, whose notification follows push_groups.
	groupRoomKind = "group"
	// systemAuthorKind is the message_views.author_kind of a system notice (PUSH-08).
	systemAuthorKind = "system"
)

// Payload is the JSON a service worker receives. It carries the conversation title, the author's
// display name, a short preview, the absolute URL of the conversation and a per-room collapse
// tag, and nothing else (PUSH-10).
type Payload struct {
	V       int    `json:"v"`
	Title   string `json:"title"`
	Author  string `json:"author"`
	Preview string `json:"preview"`
	URL     string `json:"url"`
	Tag     string `json:"tag"`
}

// Delivery sends one encrypted-ready payload to one subscription. The Deliverer implements it in
// the app's wiring; the notifier never talks to a push service itself (PUSH-14).
type Delivery interface {
	Deliver(ctx context.Context, sub Subscription, payload []byte)
}

// NotifierDeps configure a Notifier; Store, Bus and Delivery are required.
type NotifierDeps struct {
	Store *store.Store
	Bus   *events.Bus
	// Delivery sends each queued job; tests inject a recorder.
	Delivery Delivery
	// PublicURL builds the payload's absolute conversation URL.
	PublicURL *url.URL
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// QueueSize is the bounded queue length; zero means 256 and tests override it (PUSH-27).
	QueueSize int
	// Workers is how many jobs run at once; zero means 4.
	Workers int
}

// Notifier turns events.MessageCreated into push jobs and hands them to the bounded worker. It
// subscribes to the bus in the constructor, before Run, so a message posted between the two is
// not missed (PUSH-14).
type Notifier struct {
	store     *store.Store
	delivery  Delivery
	publicURL *url.URL
	logger    *slog.Logger
	limiter   *limiter
	worker    *worker

	events   <-chan events.Event
	cancel   func() // the bus subscription
	closed   chan struct{}
	closeOne sync.Once
}

// NewNotifier returns a notifier that is already subscribed to the bus; call Run to process the
// events and Close to cancel the subscription.
func NewNotifier(deps NotifierDeps) *Notifier {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.PublicURL == nil {
		deps.PublicURL = &url.URL{}
	}
	ch, cancel := deps.Bus.Subscribe(notifierBuffer)
	return &Notifier{
		store:     deps.Store,
		delivery:  deps.Delivery,
		publicURL: deps.PublicURL,
		logger:    deps.Logger,
		limiter:   newLimiter(notificationLimit, notificationWindow, time.Now),
		worker:    newWorker(deps.QueueSize, deps.Workers, deps.Logger),
		events:    ch,
		cancel:    cancel,
		closed:    make(chan struct{}),
	}
}

// Run processes the bus events and runs the worker until ctx ends, the notifier is closed, or its
// subscription ends. It returns nil in every one of those cases and waits for the worker's
// goroutines, dropping the jobs still queued (PUSH-14).
func (n *Notifier) Run(ctx context.Context) error {
	defer n.Close()
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = n.worker.run(workerCtx)
	}()
	defer func() { <-done }()
	defer cancelWorkers()

	for {
		// A notifier closed before Run must not process the events its subscription buffered.
		select {
		case <-n.closed:
			return nil
		default:
		}
		select {
		case <-ctx.Done():
			return nil
		case <-n.closed:
			return nil
		case e, ok := <-n.events:
			if !ok {
				return nil
			}
			switch e := e.(type) {
			case events.MessageCreated:
				n.handle(ctx, e)
			case events.Lagged:
				// Notifications are ephemeral: refetching would notify about old messages.
				n.logger.Debug("webpush: notifier lagged; notifications may be missed")
			}
		}
	}
}

// Close cancels the bus subscription, also when Run never started (app.Options.DisablePush).
func (n *Notifier) Close() {
	n.closeOne.Do(func() {
		n.cancel()
		close(n.closed)
	})
}

// EnqueueTest queues the fixed test payload, in each recipient's language, for every subscription
// and returns how many jobs it queued: 0 means the queue was full (PUSH-15, PUSH-27).
func (n *Notifier) EnqueueTest(ctx context.Context, subs []db.PushSubscription) int {
	locales := map[string]string{}
	payloads := map[string][]byte{}
	queued := 0
	for _, row := range subs {
		locale, known := locales[row.UserID]
		if !known {
			locale = n.recipientLocale(ctx, row.UserID)
			locales[row.UserID] = locale
		}
		payload, built := payloads[locale]
		if !built {
			var err error
			payload, err = n.testPayload(locale)
			if err != nil {
				n.logger.Error("webpush: cannot build the test payload")
				return queued
			}
			payloads[locale] = payload
		}
		sub := Subscription{ID: row.ID, Endpoint: row.Endpoint, P256dh: row.P256dh, Auth: row.Auth}
		if !n.submit(sub, payload) {
			continue
		}
		queued++
	}
	return queued
}

// testPayload builds the fixed test notification in the recipient's language (PUSH-15).
func (n *Notifier) testPayload(locale string) ([]byte, error) {
	return json.Marshal(Payload{
		V:       payloadVersion,
		Title:   testTitle,
		Preview: textsFor(locale).testBody,
		URL:     n.publicURL.String(),
		Tag:     "test",
	})
}

// recipientLocale returns the account's language, which picks the language of the server-written
// texts; an account that is gone or whose row cannot be read gets English, like an account that
// never chose a locale.
func (n *Notifier) recipientLocale(ctx context.Context, userID string) string {
	locale, err := n.store.R().GetPushRecipientLocale(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		// Neither the account nor the raw error is logged: the kind of the error says enough.
		n.logger.Warn("webpush: cannot read the notification recipient's language", "err_kind", fmt.Sprintf("%T", err))
	}
	return locale
}

// submit queues one delivery, logging a full queue with a count only: never an id, an endpoint or
// a text (PUSH-22, PUSH-27).
func (n *Notifier) submit(sub Subscription, payload []byte) bool {
	ok := n.worker.submit(func(ctx context.Context) {
		n.delivery.Deliver(ctx, sub, payload)
	})
	if !ok {
		n.logger.Warn("webpush: queue full", "dropped", n.worker.dropped.Load())
	}
	return ok
}

// handle processes one message event; a failure only costs the notifications of that message.
func (n *Notifier) handle(ctx context.Context, e events.MessageCreated) {
	if err := n.notify(ctx, e); err != nil && ctx.Err() == nil {
		n.logger.Error("webpush: cannot resolve the notification targets",
			"room_id", e.RoomID, "message_id", e.MessageID, "err", err)
	}
}

// notify notifies every user the message reaches (PUSH-08, PUSH-09, PUSH-10, PUSH-24).
func (n *Notifier) notify(ctx context.Context, e events.MessageCreated) error {
	q := n.store.R()
	room, err := q.GetRoom(ctx, e.RoomID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // the room is gone
	}
	if err != nil {
		return err
	}
	view, err := q.GetMessageView(ctx, e.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if view.DeletedAt.Valid || view.AuthorKind == systemAuthorKind {
		return nil // deleted before handled, or a system notice (PUSH-08, PUSH-23)
	}
	targets, err := n.targets(ctx, q, room, e.Seq)
	if err != nil {
		return err
	}
	users := make([]string, 0, len(targets))
	mode := map[string]string{}
	handle := map[string]string{}
	unread := map[string]bool{}
	subs := map[string][]Subscription{}
	for _, t := range targets {
		if _, seen := mode[t.userID]; !seen {
			users = append(users, t.userID)
			mode[t.userID], handle[t.userID], unread[t.userID] = t.mode, t.handle, t.unread
		}
		subs[t.userID] = append(subs[t.userID], t.sub)
	}
	for _, userID := range users {
		if userID == view.AuthorIid {
			continue // own messages never notify (PUSH-08)
		}
		mentioned := Mentions(view.Text, handle[userID])
		if !Decide(mode[userID], room.Kind, view.AuthorIid, userID, unread[userID], mentioned) {
			continue
		}
		if !n.limiter.allow(userID) {
			continue // PUSH-13
		}
		payload, err := n.messagePayload(room, view)
		if err != nil {
			return err
		}
		for _, sub := range subs[userID] {
			n.submit(sub, payload) // one slot for the user, one job per device (PUSH-24)
		}
	}
	return nil
}

// target is one row of the push target queries: a subscription of a user who may be notified.
type target struct {
	userID string
	handle string
	mode   string
	unread bool
	sub    Subscription
}

// targets loads the subscriptions of the users the room's messages can reach.
func (n *Notifier) targets(ctx context.Context, q *db.Queries, room db.Room, seq int64) ([]target, error) {
	switch room.Kind {
	case groupRoomKind:
		rows, err := q.ListGroupPushTargets(ctx, db.ListGroupPushTargetsParams{Seq: seq, RoomID: room.ID})
		if err != nil {
			return nil, err
		}
		out := make([]target, len(rows))
		for i, r := range rows {
			out[i] = target{
				userID: r.UserID, handle: r.Handle, mode: r.PushGroups, unread: r.Unread != 0,
				sub: Subscription{ID: r.SubscriptionID, Endpoint: r.Endpoint, P256dh: r.P256dh, Auth: r.Auth},
			}
		}
		return out, nil
	case dmRoomKind:
		rows, err := q.ListDMPushTargets(ctx, db.ListDMPushTargetsParams{Seq: seq, RoomID: room.ID})
		if err != nil {
			return nil, err
		}
		out := make([]target, len(rows))
		for i, r := range rows {
			out[i] = target{
				userID: r.UserID, handle: r.Handle, mode: r.PushGroups, unread: r.Unread != 0,
				sub: Subscription{ID: r.SubscriptionID, Endpoint: r.Endpoint, P256dh: r.P256dh, Auth: r.Auth},
			}
		}
		return out, nil
	default:
		return nil, nil
	}
}

// messagePayload builds one notification: the group's name, or the other participant's name in a
// DM, which is the author of a message that notifies (PUSH-10).
func (n *Notifier) messagePayload(room db.Room, view db.MessageView) ([]byte, error) {
	title := room.Name
	if room.Kind == dmRoomKind {
		title = view.AuthorName
	}
	return json.Marshal(Payload{
		V:       payloadVersion,
		Title:   title,
		Author:  view.AuthorName,
		Preview: Preview(view.Text),
		URL:     n.publicURL.JoinPath("rooms", room.ID).String(),
		Tag:     "room:" + room.ID,
	})
}
