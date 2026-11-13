package agents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
)

// MinSecretClientVersion is the oldest client version that knows the secret frame (SR-10): v0.32.0. An older
// client would ignore the frame and never answer, so the server refuses to send it the value (409
// client_outdated) instead of waiting for nothing; a local "dev" build is current.
const MinSecretClientVersion = "v0.32.0"

// DefaultSecretTimeout is how long a secret waits for the session's acknowledgement (SR-06).
const DefaultSecretTimeout = 10 * time.Second

// maxSecretReason bounds the reason a session gives for a failure, as the person reads it.
const maxSecretReason = 200

// ErrSecretOffline is a secret for a session that is not live (409 ai_offline, SR-06): the AI's session
// with the label the request was posted from has no connection now.
var ErrSecretOffline = errors.New("the session that asked for the secret is not online")

// ErrSecretOutdated is a secret for a session whose client is older than MinSecretClientVersion (409
// client_outdated, SR-10); the value was not sent.
var ErrSecretOutdated = errors.New("the session that asked for the secret runs a client too old to receive it")

// SecretNotSavedError is a secret the session did not confirm (504 not_saved, SR-06): no acknowledgement in
// time, a connection that ended first, or the session's own failure. Reason says which, never the value.
type SecretNotSavedError struct {
	Reason string
}

func (e *SecretNotSavedError) Error() string { return "the secret was not saved: " + e.Reason }

// SecretDelivery is one secret for one live session: the AI, the label of its session ("" for the principal)
// and what the frame carries.
type SecretDelivery struct {
	AgentID   string
	Label     string
	RoomID    string
	MessageID string
	Name      string
	Value     string
}

// secretCurrent reports whether a client of version v knows the secret frame. It fails closed like
// heartbeatCurrent: a missing version and one that is no release version are old, "dev" is current.
func secretCurrent(v string) bool {
	if v == "dev" {
		return true
	}
	if _, ok := parseVersion(v); !ok || v == "" {
		return false
	}
	return !versionOlder(v, MinSecretClientVersion)
}

// secretWaits are the acknowledgements a connection's secret frames wait for, by request id (SR-06).
type secretWaits struct {
	mu   sync.Mutex
	acks map[string]chan dto.AgentSecretAckFrame
}

// await registers a request before its frame is written: the acknowledgement can come as soon as it is.
func (w *secretWaits) await(requestID string) <-chan dto.AgentSecretAckFrame {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.acks == nil {
		w.acks = map[string]chan dto.AgentSecretAckFrame{}
	}
	ch := make(chan dto.AgentSecretAckFrame, 1)
	w.acks[requestID] = ch
	return ch
}

// forget drops a request that was answered, timed out or whose connection ended.
func (w *secretWaits) forget(requestID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.acks, requestID)
}

// answer hands an acknowledgement to the request waiting for it; one for no request (late, repeated or
// made up) is dropped.
func (w *secretWaits) answer(ack dto.AgentSecretAckFrame) {
	w.mu.Lock()
	ch, ok := w.acks[ack.RequestID]
	delete(w.acks, ack.RequestID)
	w.mu.Unlock()
	if ok {
		ch <- ack // buffered: never blocks the reader
	}
}

// DeliverSecret writes one secret frame to the AI's live session with the label d.Label and waits for its
// acknowledgement (SR-06, SR-08, SR-10): ErrSecretOffline when that session is not live, ErrSecretOutdated
// when its client is too old to know the frame (the value is then never sent), a *SecretNotSavedError when no
// acknowledgement arrives within the timeout, the connection ends first or the session reports a failure. It
// returns the path the session wrote. The value is in the frame and nowhere else: never logged, never kept.
func (g *Gateway) DeliverSecret(ctx context.Context, d SecretDelivery) (string, error) {
	s, ok := g.d.Presence.SessionByLabel(d.AgentID, d.Label)
	if !ok {
		return "", ErrSecretOffline
	}
	c, ok := s.(*conn)
	if !ok || c.closing() {
		return "", ErrSecretOffline
	}
	if !secretCurrent(c.clientVersion) {
		return "", ErrSecretOutdated
	}
	requestID := store.NewID()
	wait := c.secretAcks.await(requestID)
	defer c.secretAcks.forget(requestID)
	ctx, cancel := context.WithTimeout(ctx, g.d.SecretTimeout)
	defer cancel()
	err := c.write(ctx, dto.AgentSecretFrame{
		Type: dto.AgentFrameTypeSecret, RequestID: requestID, MessageID: d.MessageID, RoomID: d.RoomID,
		Name: d.Name, Value: d.Value,
	})
	if err != nil {
		return "", &SecretNotSavedError{Reason: "the connection to the session failed before it got the secret"}
	}
	select {
	case ack := <-wait:
		switch {
		case !ack.OK:
			return "", &SecretNotSavedError{Reason: secretReason(ack.Error, d.Value)}
		case strings.TrimSpace(ack.Path) == "":
			return "", &SecretNotSavedError{Reason: "the session did not say where it saved the secret"}
		}
		return ack.Path, nil
	case <-c.done:
		return "", &SecretNotSavedError{Reason: "the session disconnected before it confirmed"}
	case <-ctx.Done():
		return "", &SecretNotSavedError{Reason: "the session did not confirm in " + g.d.SecretTimeout.String()}
	}
}

// secretReason is the session's reason for a failure as a person reads it: one line of at most
// maxSecretReason characters, or a plain sentence when the session gave none or when the reason holds the
// value itself, which no error text may carry (SR-07).
func secretReason(reason, value string) string {
	if value != "" && strings.Contains(reason, value) {
		reason = ""
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return "the session could not save it"
	}
	if utf8.RuneCountInString(reason) > maxSecretReason {
		reason = string([]rune(reason)[:maxSecretReason]) + "…"
	}
	return reason
}
