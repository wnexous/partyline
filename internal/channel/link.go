package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel/mcp"
)

// Close codes the agent gateway uses (agent-channel contract notes).
const (
	closeReplaced websocket.StatusCode = 4000
	closeDeleted  websocket.StatusCode = 4001
	closeLagged                        = websocket.StatusTryAgainLater // 1013
)

const (
	// dedupeSize is how many delivered message ids the link remembers (AGT-10).
	dedupeSize   = 1000
	ackTimeout   = 10 * time.Second
	closeTimeout = 1500 * time.Millisecond
)

// timing holds the link's waits; tests shorten them.
type timing struct {
	minBackoff    time.Duration // first reconnect delay (AGT-03)
	maxBackoff    time.Duration // reconnect delay cap (AGT-03)
	invalidRetry  time.Duration // retry interval while the token is rejected
	replacedPause time.Duration // quiet period after another session took the agent name
}

var defaultTiming = timing{
	minBackoff:    time.Second,
	maxBackoff:    30 * time.Second,
	invalidRetry:  60 * time.Second,
	replacedPause: 5 * time.Minute,
}

func (t timing) withDefaults() timing {
	if t.minBackoff <= 0 {
		t.minBackoff = defaultTiming.minBackoff
	}
	if t.maxBackoff <= 0 {
		t.maxBackoff = defaultTiming.maxBackoff
	}
	if t.invalidRetry <= 0 {
		t.invalidRetry = defaultTiming.invalidRetry
	}
	if t.replacedPause <= 0 {
		t.replacedPause = defaultTiming.replacedPause
	}
	return t
}

// nextBackoff doubles d up to max.
func nextBackoff(d, maxDelay time.Duration) time.Duration {
	d *= 2
	if d > maxDelay {
		d = maxDelay
	}
	return d
}

// notifier is where the link sends channel notifications (the MCP server).
type notifier interface {
	Notify(method string, params any) error
}

// linkState is the connection state the tools report.
type linkState int

const (
	stateConnecting linkState = iota
	stateConnected
	stateUnreachable
	stateInvalidToken
	stateReplaced
	stateDeleted
)

// link holds the agent WebSocket: it reconnects with backoff, turns pushed messages into
// notifications, acknowledges them after they are written and drops duplicates.
type link struct {
	base   *url.URL
	token  string
	agent  string
	http   *http.Client
	notify notifier
	stderr io.Writer
	t      timing

	// sleep waits d between connection attempts; it reports false when ctx ended first.
	// Tests replace it to record the backoff without waiting.
	sleep func(ctx context.Context, d time.Duration) bool

	mu    sync.Mutex
	state linkState
	seen  *idRing
}

func newLink(base *url.URL, token, agent string, hc *http.Client, n notifier, stderr io.Writer, t timing) *link {
	return &link{base: base, token: token, agent: agent, http: hc, notify: n, stderr: stderr, t: t.withDefaults(), sleep: sleepCtx, seen: newIDRing(dedupeSize)}
}

// sleepCtx waits d or until ctx ends; it reports whether the full wait passed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Err returns the error tools report instead of calling the server, or nil when tools may
// proceed (connected, or (re)connecting after a dropped connection).
func (l *link) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch l.state {
	case stateUnreachable:
		return errors.New("partyline: server unreachable")
	case stateInvalidToken:
		return errInvalidToken
	case stateReplaced:
		return fmt.Errorf("partyline: replaced by another session using agent name %s", l.agent)
	case stateDeleted:
		return fmt.Errorf("partyline: agent %s was deleted; restart Claude Code to register it again", l.agent)
	default:
		return nil
	}
}

var errInvalidToken = errors.New("partyline: not connected (invalid token)")

// setState records s and reports whether it changed.
func (l *link) setState(s linkState) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	changed := l.state != s
	l.state = s
	return changed
}

// outcome is how one connection attempt ended.
type outcome int

const (
	outDialFailed outcome = iota
	outInvalidToken
	outDropped // was connected, then the connection ended
	outLagged
	outReplaced
	outDeleted
)

// run connects and reconnects until ctx ends (nil) or writing a notification fails.
func (l *link) run(ctx context.Context) error {
	backoff := l.t.minBackoff
	for {
		out, err := l.session(ctx)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		var wait time.Duration
		switch out {
		case outDialFailed:
			l.setState(stateUnreachable)
			wait, backoff = backoff, nextBackoff(backoff, l.t.maxBackoff)
		case outInvalidToken:
			if l.setState(stateInvalidToken) {
				fmt.Fprintf(l.stderr, "partyline: the server rejected PARTYLINE_TOKEN; tools are disabled, retrying every %s\n", l.t.invalidRetry)
			}
			wait = l.t.invalidRetry
		case outDropped:
			l.setState(stateConnecting)
			wait, backoff = l.t.minBackoff, nextBackoff(l.t.minBackoff, l.t.maxBackoff)
		case outLagged:
			l.setState(stateConnecting)
			wait, backoff = 0, l.t.minBackoff
		case outReplaced:
			l.setState(stateReplaced)
			fmt.Fprintf(l.stderr, "partyline: another session connected as agent %s; pausing for %s\n", l.agent, l.t.replacedPause)
			wait, backoff = l.t.replacedPause, l.t.minBackoff
		case outDeleted:
			l.setState(stateDeleted)
			fmt.Fprintf(l.stderr, "partyline: agent %s was deleted; not reconnecting until restart\n", l.agent)
			<-ctx.Done()
			return nil
		}
		if !l.sleep(ctx, wait) {
			return nil
		}
	}
}

func (l *link) wsURL() string {
	u := *l.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/agent/ws"
	return u.String()
}

// session runs one connection. A non-nil error is fatal (the notification output failed).
func (l *link) session(ctx context.Context) (outcome, error) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+l.token)
	h.Set(dto.HeaderAgent, l.agent)
	ws, resp, err := websocket.Dial(ctx, l.wsURL(), &websocket.DialOptions{HTTPClient: l.http, HTTPHeader: h})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return outInvalidToken, nil // a bad, revoked or non-agent token
		}
		return outDialFailed, nil
	}
	ws.SetReadLimit(-1) // messages carry up to 16k characters plus metadata; the server is trusted
	l.setState(stateConnected)

	// Closing on ctx end (stdin EOF, signal) sends a normal close so the server marks the
	// agent offline at once (AGT-03).
	readCtx, stopRead := context.WithCancel(context.Background())
	defer stopRead()
	stop := context.AfterFunc(ctx, func() {
		_ = ws.Close(websocket.StatusNormalClosure, "bye")
		stopRead()
	})
	defer stop()

	for {
		_, data, err := ws.Read(readCtx)
		if err != nil {
			_ = ws.CloseNow()
			switch websocket.CloseStatus(err) {
			case closeReplaced:
				return outReplaced, nil
			case closeDeleted:
				return outDeleted, nil
			case closeLagged:
				return outLagged, nil
			}
			return outDropped, nil
		}
		if err := l.handle(readCtx, ws, data); err != nil {
			if errors.Is(err, mcp.ErrClosed) {
				return outDropped, nil // shutting down: ctx ends next
			}
			if errors.Is(err, errAck) {
				_ = ws.CloseNow()
				return outDropped, nil
			}
			_ = ws.CloseNow()
			return outDropped, err
		}
	}
}

var errAck = errors.New("ack failed")

// handle processes one server frame.
func (l *link) handle(ctx context.Context, ws *websocket.Conn, data []byte) error {
	var head struct {
		Type dto.AgentFrameType `json:"type"`
	}
	if json.Unmarshal(data, &head) != nil {
		return nil
	}
	switch head.Type {
	case dto.AgentFrameTypeMessage:
		var f dto.AgentMessageFrame
		if json.Unmarshal(data, &f) != nil || f.Message.ID == "" {
			return nil
		}
		// At-least-once delivery: a repeated id is acknowledged without a second
		// notification (AGT-10).
		if !l.seenBefore(f.Message.ID) {
			if err := l.notify.Notify(NotificationMethod, Format(f.AgentMessage)); err != nil {
				return err
			}
			l.remember(f.Message.ID)
		}
		// The ack follows the write to Claude Code (AGT-07).
		ack, _ := json.Marshal(dto.AgentAckFrame{Type: dto.AgentFrameTypeAck, MessageID: f.Message.ID})
		actx, cancel := context.WithTimeout(ctx, ackTimeout)
		defer cancel()
		if err := ws.Write(actx, websocket.MessageText, ack); err != nil {
			return errAck
		}
	case dto.AgentFrameTypeNotice:
		var f dto.AgentNoticeFrame
		if json.Unmarshal(data, &f) != nil {
			return nil
		}
		return l.notify.Notify(NotificationMethod, FormatNotice(f))
	}
	return nil // welcome and unknown frames need nothing
}

func (l *link) seenBefore(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seen.has(id)
}

func (l *link) remember(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen.add(id)
}

// idRing remembers the last n ids.
type idRing struct {
	ids []string
	pos int
	set map[string]struct{}
}

func newIDRing(n int) *idRing {
	return &idRing{ids: make([]string, n), set: make(map[string]struct{}, n)}
}

func (r *idRing) has(id string) bool { _, ok := r.set[id]; return ok }

func (r *idRing) add(id string) {
	if r.has(id) {
		return
	}
	if old := r.ids[r.pos]; old != "" {
		delete(r.set, old)
	}
	r.ids[r.pos] = id
	r.set[id] = struct{}{}
	r.pos = (r.pos + 1) % len(r.ids)
}
