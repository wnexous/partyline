package channel

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel/mcp"
)

// Close codes the agent gateway uses (agent-channel contract notes).
const (
	closeDeleted   websocket.StatusCode = 4001
	closeTakenOver websocket.StatusCode = 4002
	closeBusy      websocket.StatusCode = 4003
	closeLagged                         = websocket.StatusTryAgainLater // 1013
)

const (
	// dedupeSize is how many delivered message ids the link remembers (AGT-10).
	dedupeSize   = 1000
	ackTimeout   = 10 * time.Second
	closeTimeout = 1500 * time.Millisecond
)

// timing holds the link's waits; tests shorten them.
type timing struct {
	minBackoff   time.Duration // first reconnect delay (AGT-03)
	maxBackoff   time.Duration // reconnect delay cap (AGT-03)
	invalidRetry time.Duration // retry interval while the token is rejected
	limitPause   time.Duration // retry interval while the owner is at the agent limit
	// busyRetries are the waits before the retries of a connection refused because the AI is
	// live in another session: that session may be ending (SES-06). Empty means no retry; nil
	// means the default.
	busyRetries []time.Duration
	// busyPoll is the wait between connection attempts once the busy retries are used up: a
	// channel keeps trying at this pace until the session that holds the AI ends (SES-04).
	busyPoll time.Duration
}

var defaultTiming = timing{
	minBackoff:   time.Second,
	maxBackoff:   30 * time.Second,
	invalidRetry: 60 * time.Second,
	limitPause:   5 * time.Minute,
	// The retries add up to 45 s, more than the 35 s SES-06 takes to drop a session that went
	// silent: the old session of a restarted AI is gone before the last retry.
	busyRetries: []time.Duration{2 * time.Second, 4 * time.Second, 4 * time.Second, 10 * time.Second, 10 * time.Second, 15 * time.Second},
	busyPoll:    30 * time.Second,
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
	if t.limitPause <= 0 {
		t.limitPause = defaultTiming.limitPause
	}
	if t.busyRetries == nil {
		t.busyRetries = defaultTiming.busyRetries
	}
	if t.busyPoll <= 0 {
		t.busyPoll = defaultTiming.busyPoll
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
	// Initialized is closed once the client may receive notifications.
	Initialized() <-chan struct{}
}

// linkState is the connection state the tools report.
type linkState int

const (
	stateConnecting linkState = iota
	stateConnected
	stateUnreachable
	stateInvalidToken
	stateBusy
	stateTakenOver
	stateInvalid
	stateDeleted
	stateAgentLimit
)

// link holds the agent WebSocket: it reconnects with backoff, turns pushed messages into
// notifications, acknowledges them after they are written and drops duplicates.
type link struct {
	base   *url.URL
	token  string
	http   *http.Client
	notify notifier
	stderr io.Writer
	t      timing

	// sleep waits d between connection attempts; it reports false when ctx ended first.
	// Tests replace it to record the backoff without waiting.
	sleep func(ctx context.Context, d time.Duration) bool

	// queueCap and readLimit bound memory per connection (maxQueuedFrames, maxFrameBytes);
	// tests lower them.
	queueCap  int
	readLimit int64

	// identity is sent on every connection: machine, folder, home, harness and the name to adopt
	// (AID-07). The server resolves the AI from it; no name is sent.
	identity Identity
	// liveID is the random id of this process, sent on every connection: a reconnect of the same
	// process is not another session of the AI (SES-02).
	liveID string
	// takeover makes the connection take over the AI's live session (SES-02). The first
	// connection that succeeds clears it: a reconnect after a drop never takes over.
	takeover bool
	// zone is where the busy text shows the time the other session connected; nil is the local zone.
	zone *time.Location
	// announce makes the first welcome of the process a notice that says who the AI is and who
	// its owner is (the full channel; listen prints message lines only).
	announce bool
	// keepTryingWhenBusy keeps the link dialing every busyPoll once the busy retries are used up
	// instead of giving up: a channel connects by itself when the session that holds the AI ends
	// (SES-04). listen leaves it false and ends after the retries, as it always did.
	keepTryingWhenBusy bool
	// onLost is called once, from run, when another session holds the AI or took it over. Busy: once,
	// after the retries, whether or not the link keeps trying. The text is what the person is told
	// (SES-04, SES-05). The channel keeps answering its tools; listen prints it and ends. Nil does
	// nothing.
	onLost func(reason lostReason, text string)

	mu     sync.Mutex
	state  linkState
	reason string   // what the server said about a refusal (stateInvalid)
	busy   busyInfo // what the server said about the session that holds the AI (stateBusy)
	// endedBy is the close reason ("ended by @lead") of a session that someone who manages the AI ended;
	// empty when it was taken over by another session (stateTakenOver).
	endedBy string
	seen    *idRing
	// announced is set once the identity notice was sent: a reconnect is the same session.
	announced bool
	// announcedSession is set once the process said which parallel session it became: a reconnect of the
	// same process says it again neither on stderr nor in the identity notice (SESS-15).
	announcedSession bool
	// expectReconnect is set once a channel told onLost that it keeps trying while busy: the welcome of
	// the connection that follows then tells the harness that Partyline is back (SES-04).
	expectReconnect bool
}

// lostReason says why a link gave up on its connection.
type lostReason string

const (
	// lostBusy: another session holds the AI and did not let go during the retries.
	lostBusy lostReason = "busy"
	// lostTakenOver: another session took the AI over from this one.
	lostTakenOver lostReason = "taken_over"
)

// busyInfo is what a refusal says about the session that holds the AI; the zero value when the
// server only closed the socket.
type busyInfo struct {
	name     string    // the AI's display name
	appLabel string    // readable name of the app of the live session
	since    time.Time // when the live session connected
}

func newLink(base *url.URL, token string, id Identity, hc *http.Client, n notifier, stderr io.Writer, t timing) *link {
	return &link{base: base, token: token, identity: id, liveID: NewLiveID(), http: hc, notify: n, stderr: stderr, t: t.withDefaults(), sleep: sleepCtx, seen: newIDRing(dedupeSize),
		queueCap: maxQueuedFrames, readLimit: maxFrameBytes}
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
	case stateBusy:
		return sentence(l.busyTextLocked())
	case stateTakenOver:
		return sentence(l.takenOverTextLocked())
	case stateInvalid:
		return fmt.Errorf("partyline: the server refused this session: %s", cmp.Or(l.reason, unusableSession))
	case stateDeleted:
		return errDeleted
	case stateAgentLimit:
		return errAgentLimit
	default:
		return nil
	}
}

var errInvalidToken = errors.New("partyline: not connected (invalid token)")

var errAgentLimit = errors.New("partyline: agent limit reached for this account; delete unused agents in the web app")

// takenOverText is what the harness is told, once, when another session takes the AI over, and
// what every tool answers from then on (SES-05).
const takenOverText = "This intelligence was opened in another session; Partyline is disconnected here."

// reconnectText is what a channel that found the AI open elsewhere tells its harness, once, when it
// connects by itself; the messages that queued meanwhile come with the backlog (SES-04).
const reconnectText = "Partyline is connected again; messages reach this session."

// endedByReason is the close reason of a session that someone who manages the AI ended (POS-04): the
// server closes it with the code of a takeover, which v0.5 channels already obey, and says who in the
// reason, "ended by @lead". Only a reason of exactly this form is repeated to the harness: anything else
// is a takeover by another session.
var endedByReason = regexp.MustCompile(`^ended by @` + strings.TrimSuffix(strings.TrimPrefix(dto.HandlePattern, "^"), "$") + `$`)

// rememberEnded keeps who ended the session when a 4002 close says so, and forgets it when it does not.
func (l *link) rememberEnded(err error) {
	var ce websocket.CloseError
	reason := ""
	if errors.As(err, &ce) && endedByReason.MatchString(ce.Reason) {
		reason = ce.Reason
	}
	l.mu.Lock()
	l.endedBy = reason
	l.mu.Unlock()
}

// takenOverTextLocked is what the harness is told when the session is over without a fault of its own: that
// someone ended it, or that another session took the AI over. The caller holds l.mu.
func (l *link) takenOverTextLocked() string {
	if l.endedBy != "" {
		return "This session was " + l.endedBy + "; Partyline is disconnected here."
	}
	return takenOverText
}

func (l *link) takenOverText() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.takenOverTextLocked()
}

// sentence is an error whose text is a full sentence told to the person as it is, so it ends with
// a full stop, which Go error strings do not.
type sentence string

func (s sentence) Error() string { return string(s) }

// busyText is what a channel that cannot connect because another session holds the AI tells the
// person (SES-04): who is open where and since when (local time), and how to take over.
func (l *link) busyText() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.busyTextLocked()
}

// busyTextLocked is busyText for a caller that holds l.mu.
func (l *link) busyTextLocked() string {
	b := l.busy
	where := ""
	switch {
	case b.appLabel != "" && !b.since.IsZero():
		where = " (" + b.appLabel + ", since " + b.since.In(cmp.Or(l.zone, time.Local)).Format("15:04") + ")"
	case b.appLabel != "":
		where = " (" + b.appLabel + ")"
	}
	command := "partyline " + cmp.Or(l.identity.Harness, "<harness>") + " --take-over"
	return cmp.Or(b.name, "This intelligence") + " is open in another session" + where +
		`. Partyline is not connected here. To take over, restart with "` + command + `" or set PARTYLINE_TAKEOVER=1.`
}

// connectionNotice is the notification that tells the harness why Partyline is not (or no
// longer) connected here; adapters tell it from a room message by its type.
func connectionNotice(reason lostReason, text string) Notification {
	return systemNotice(string(reason), text)
}

// reasonIdentity is the reason of the notice that says who the AI is and who its owner is.
const reasonIdentity = "identity"

// reasonConnected is the reason of the notice that says Partyline is connected again after a busy
// session (SES-04).
const reasonConnected = "connected"

// systemNotice is a notification from Partyline itself, outside any room: its meta says type
// "notice" and why. The adapters of pi, OpenCode and omp show such a notice to the person and
// never hand it to the model; Claude Code gives every channel event to the model.
func systemNotice(reason, text string) Notification {
	return Notification{Content: text, Meta: map[string]string{
		"type": "notice", "reason": reason, "author": "Partyline", "author_kind": string(dto.AuthorKindSystem),
		"sender_name": "", "trusted": "false", "kind": string(dto.MessageKindSystem),
	}}
}

// identityText is what the first welcome tells the session, in the words of the design: "You are Worker 1
// (@w1), an AI of Acme (acme), position Worktrees. Messages marked trusted="true" come from the identities that
// manage you: Lead (@lead), Alice (@alice). You operate: <Platform> <account> (connection <id>), ...". The
// position clause is left out of an AI that holds none, and an AI nobody manages is told "No one manages you
// in Acme, so no message is marked trusted." (POS-10). A welcome that names no organization - an older
// server - gets the sentences it always had: "You are <name> (@handle). Your owner is <name> (@handle)."
// The text is empty when the server named no AI, and has no operate sentence when the AI operates no
// connection (CID-06): the ids are what platform events name their connection by.
func identityText(w dto.AgentWelcomeFrame) string {
	named := func(r dto.IntelligenceRef) string { return r.DisplayName + " (@" + r.Handle + ")" }
	if w.Intelligence.Handle == "" {
		return ""
	}
	var text string
	if org := w.Organization; org != nil {
		text = "You are " + named(w.Intelligence) + ", an AI of " + orgLabel(*org)
		if w.Position != "" {
			text += ", position " + w.Position
		}
		text += "."
		if len(w.Managers) == 0 {
			text += " No one manages you in " + cmp.Or(org.Name, org.Slug) + ", so no message is marked trusted."
		} else {
			managers := make([]string, len(w.Managers))
			for i, m := range w.Managers {
				managers[i] = named(m)
			}
			text += ` Messages marked trusted="true" come from the identities that manage you: ` + strings.Join(managers, ", ") + "."
		}
	} else {
		text = "You are " + named(w.Intelligence) + "."
		if w.Owner.Handle != "" {
			text += " Your owner is " + named(w.Owner) + "."
		}
	}
	if len(w.Connections) > 0 {
		operated := make([]string, len(w.Connections))
		for i, c := range w.Connections {
			operated[i] = strings.TrimSpace(platformLabel(c.Platform)+" "+c.AccountLabel) + " (connection " + c.ID + ")"
		}
		text += " You operate: " + strings.Join(operated, ", ") + "."
	}
	if w.Session.Label != "" {
		text += " This session is :" + w.Session.Label + "."
	}
	return text
}

// orgLabel is an organization as the notice names it: "Acme (acme)", its name and its address.
func orgLabel(o dto.ContactOrg) string {
	if o.Name == "" || o.Slug == "" {
		return cmp.Or(o.Name, o.Slug)
	}
	return o.Name + " (" + o.Slug + ")"
}

// expectReconnectAfterBusy records that onLost was told about a busy session and that the link keeps
// trying: the welcome of the connection that follows says that Partyline is back (SES-04).
func (l *link) expectReconnectAfterBusy() {
	l.mu.Lock()
	l.expectReconnect = true
	l.mu.Unlock()
}

// announceReconnect sends the notice that says Partyline is connected again, once, when a channel that
// told its harness the AI was open elsewhere connects by itself: the messages that queued meanwhile
// arrive with the backlog (SES-04).
func (l *link) announceReconnect() error {
	l.mu.Lock()
	waiting := l.expectReconnect
	l.mu.Unlock()
	if !waiting {
		return nil
	}
	if err := l.notify.Notify(NotificationMethod, systemNotice(reasonConnected, reconnectText)); err != nil {
		return err
	}
	l.mu.Lock()
	l.expectReconnect = false
	l.mu.Unlock()
	return nil
}

// announceSession tells the person which parallel session this connection became, once per process
// (SESS-15): the line the SES-01 takeover question used to ask about. The principal says nothing.
func (l *link) announceSession(w dto.AgentWelcomeFrame) {
	label := w.Session.Label
	if label == "" {
		return
	}
	l.mu.Lock()
	said := l.announcedSession
	l.announcedSession = true
	l.mu.Unlock()
	if said {
		return
	}
	fmt.Fprintf(l.stderr, "partyline: opened as parallel session %s\n", label)
}

// announceIdentity sends the notice that says who the AI is, once per process: the welcome of a
// reconnect, which follows a dropped connection of the same session, says nothing.
func (l *link) announceIdentity(w dto.AgentWelcomeFrame) error {
	l.announceSession(w)
	text := identityText(w)
	l.mu.Lock()
	skip := !l.announce || l.announced || text == ""
	l.mu.Unlock()
	if skip {
		return nil
	}
	if err := l.notify.Notify(NotificationMethod, systemNotice(reasonIdentity, text)); err != nil {
		return err
	}
	l.mu.Lock()
	l.announced = true
	l.mu.Unlock()
	return nil
}

var errDeleted = errors.New("partyline: this agent was deleted; restart the session to register it again")

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
	outBusy      // the AI is live in another session and this connection did not take over
	outTakenOver // another session took the AI over from this one
	outInvalid   // the server cannot use the machine, folder or placement the session asked for
	outDeleted
	outAgentLimit
)

// run connects and reconnects until ctx ends (nil) or writing a notification fails.
func (l *link) run(ctx context.Context) error {
	backoff := l.t.minBackoff
	busyTries := 0    // consecutive refusals because the AI is live elsewhere
	busyTold := false // onLost was told about this busy episode
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
			wait, backoff, busyTries, busyTold = l.t.minBackoff, nextBackoff(l.t.minBackoff, l.t.maxBackoff), 0, false
		case outLagged:
			l.setState(stateConnecting)
			wait, backoff, busyTries, busyTold = 0, l.t.minBackoff, 0, false
		case outBusy:
			l.setState(stateBusy)
			switch {
			case busyTries < len(l.t.busyRetries):
				// The session that holds the AI may be ending (SES-06): retry soon.
				wait, backoff = l.t.busyRetries[busyTries], l.t.minBackoff
				busyTries++
			case !l.keepTryingWhenBusy:
				// The session that holds the AI did not end: say so and stop trying.
				l.lost(lostBusy, l.busyText())
				<-ctx.Done()
				return nil
			default:
				// A channel never gives up, because the old session may have died without closing its
				// socket and so lingers until the server's ping drops it (SES-04, SES-06): tell the
				// harness once, then keep dialing until it connects or ctx ends.
				if !busyTold {
					l.lost(lostBusy, l.busyText())
					l.expectReconnectAfterBusy()
					busyTold = true
				}
				wait, backoff = l.t.busyPoll, l.t.minBackoff
			}
		case outTakenOver:
			l.setState(stateTakenOver)
			l.lost(lostTakenOver, l.takenOverText())
			<-ctx.Done()
			return nil
		case outInvalid:
			l.setState(stateInvalid)
			fmt.Fprintf(l.stderr, "partyline: the server refused this session: %s\n", l.refusal())
			<-ctx.Done()
			return nil
		case outAgentLimit:
			if l.setState(stateAgentLimit) {
				fmt.Fprintf(l.stderr, "partyline: the server refused this agent: agent limit reached; retrying every %s\n", l.t.limitPause)
			}
			wait, backoff = l.t.limitPause, l.t.minBackoff
		case outDeleted:
			l.setState(stateDeleted)
			fmt.Fprintln(l.stderr, "partyline: this agent was deleted; not reconnecting until restart")
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

// lost reports to onLost that the link gave up.
func (l *link) lost(reason lostReason, text string) {
	if l.onLost != nil {
		l.onLost(reason, text)
	}
}

// refused tells how the server refused a connection from its response; a nil response is a
// dial that did not reach the server.
func (l *link) refused(resp *http.Response) outcome {
	if resp == nil {
		return outDialFailed
	}
	if resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return outInvalidToken // a bad or revoked token
	case http.StatusForbidden:
		// A session that asked for an organization or a position and is told 403 was refused for that: the creator is
		// not in the organization or may not give the position (ORG-05, POS-07). Without such a request a 403 is a
		// token the server will not take (a user's, not an agent's).
		if e := refusalBody(resp); l.identity.asksPlacement() && e.Message != "" {
			return l.refusedFor(e.Message)
		}
		return outInvalidToken
	case http.StatusConflict:
		e := refusalBody(resp)
		switch e.Code {
		case dto.ErrorCodeIdentityBusy:
			l.rememberBusy(e.Details)
			return outBusy
		case dto.ErrorCodeOrganizationMismatch: // --org names an organization other than the AI's home (ORG-04)
			return l.refusedFor(e.Message)
		}
		return outAgentLimit // the owner already has the maximum number of agents
	case http.StatusUnprocessableEntity:
		return l.refusedFor(refusalBody(resp).Message)
	}
	return outDialFailed
}

// refusedFor remembers why the server cannot use this session, in its own words, and reports the refusal.
func (l *link) refusedFor(reason string) outcome {
	l.mu.Lock()
	l.reason = reason
	l.mu.Unlock()
	return outInvalid
}

// rememberBusy keeps what a refusal says about the session that holds the AI.
func (l *link) rememberBusy(details map[string]string) {
	b := busyInfo{name: details[dto.BusyDetailDisplayName], appLabel: details[dto.BusyDetailAppLabel]}
	if at, err := time.Parse(time.RFC3339, details[dto.BusyDetailConnectedAt]); err == nil {
		b.since = at
	}
	l.mu.Lock()
	l.busy = b
	l.mu.Unlock()
}

// refusal is what the server said about the machine or folder it refused.
func (l *link) refusal() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return cmp.Or(l.reason, unusableSession)
}

// unusableSession stands for a refusal that came without a reason.
const unusableSession = "it cannot use the machine or folder"

// refusalBody reads the error of a refused connection; the zero value when the body is not one.
func refusalBody(resp *http.Response) dto.Error {
	var body dto.ErrorBody
	if resp.Body != nil {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body)
	}
	return body.Error
}

// session runs one connection. A non-nil error is fatal (the notification output failed).
func (l *link) session(ctx context.Context) (outcome, error) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+l.token)
	l.identity.setHeaders(h)
	h.Set(dto.HeaderLive, l.liveID)
	if l.takeover {
		h.Set(dto.HeaderTakeover, "1")
	}
	h.Set(dto.HeaderFeatures, dto.FeaturePlatform) // platform events (CID-06)
	ws, resp, err := websocket.Dial(ctx, l.wsURL(), &websocket.DialOptions{HTTPClient: l.http, HTTPHeader: h})
	if err != nil {
		return l.refused(resp), nil
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	l.takeover = false // only the first connection that succeeds takes over (SES-02)
	ws.SetReadLimit(l.readLimit)
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

	// The reader only queues frames; a delivery goroutine turns them into notifications once
	// Claude Code is initialized. So the socket is read, pings are answered and closes are
	// seen even before notifications/initialized.
	q := newFrameQueue(l.queueCap)
	var deliverErr error
	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		if err := l.deliver(readCtx, ws, q); err != nil {
			deliverErr = err
			_ = ws.CloseNow() // ends the read below
		}
	}()

	for {
		_, data, err := ws.Read(readCtx)
		if err != nil {
			_ = ws.CloseNow()
			stopRead()
			<-delivered
			if deliverErr != nil && !errors.Is(deliverErr, mcp.ErrClosed) && !errors.Is(deliverErr, errAck) {
				return outDropped, deliverErr // the notification output failed
			}
			switch websocket.CloseStatus(err) {
			case closeTakenOver:
				l.rememberEnded(err)
				return outTakenOver, nil
			case closeBusy:
				return outBusy, nil // lost the race to another session between the check and the attach
			case closeDeleted:
				return outDeleted, nil
			case closeLagged:
				return outLagged, nil
			}
			return outDropped, nil // dropped, shutting down (ctx ends next) or a failed ack
		}
		if !q.push(data) {
			// Claude Code stopped reading: drop the connection instead of growing without
			// bound; unacknowledged messages come back with the backlog after reconnecting.
			_ = ws.Close(websocket.StatusPolicyViolation, "channel queue full")
			stopRead()
			<-delivered
			return outDropped, nil
		}
	}
}

// deliver waits until Claude Code is initialized, then handles the queued frames in order
// until ctx ends. Frames still queued then are not acknowledged, so the backlog sends them
// again after a reconnect.
func (l *link) deliver(ctx context.Context, ws *websocket.Conn, q *frameQueue) error {
	select {
	case <-l.notify.Initialized():
	case <-ctx.Done():
		return nil
	}
	for {
		data, ok := q.pop(ctx)
		if !ok {
			return nil
		}
		if err := l.handle(ctx, ws, data); err != nil {
			return err
		}
	}
}

// maxFrameBytes bounds one server frame: a message carries at most 16,000 characters plus
// metadata and attachment references (files are downloaded separately).
const maxFrameBytes = 1 << 20

// maxQueuedFrames bounds the frames waiting for delivery (the backlog is at most 50 per room).
const maxQueuedFrames = 2000

// frameQueue holds the frames read from the socket until they are delivered.
type frameQueue struct {
	mu    sync.Mutex
	items [][]byte
	max   int
	ready chan struct{}
}

func newFrameQueue(limit int) *frameQueue {
	return &frameQueue{max: limit, ready: make(chan struct{}, 1)}
}

// push queues data; false when the queue is full.
func (q *frameQueue) push(data []byte) bool {
	q.mu.Lock()
	if len(q.items) >= q.max {
		q.mu.Unlock()
		return false
	}
	q.items = append(q.items, data)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
	return true
}

// pop returns the oldest frame, waiting for one; false when ctx ends first.
func (q *frameQueue) pop(ctx context.Context) ([]byte, bool) {
	for {
		if ctx.Err() != nil {
			return nil, false
		}
		q.mu.Lock()
		if len(q.items) > 0 {
			data := q.items[0]
			q.items[0] = nil
			q.items = q.items[1:]
			q.mu.Unlock()
			return data, true
		}
		q.mu.Unlock()
		select {
		case <-q.ready:
		case <-ctx.Done():
			return nil, false
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
		ack, _ := json.Marshal(dto.AgentAckFrame{Type: dto.AgentFrameTypeAck, MessageID: f.Message.ID})
		return l.notifyThenAck(ctx, ws, f.Message.ID, func() Notification { return Format(f.AgentMessage) }, ack)
	case dto.AgentFrameTypeMessageEdited, dto.AgentFrameTypeMessageDeleted:
		// A message that was delivered before changed (EDT-07, DLT-07). It is told as it happened, in order with
		// the messages around it, and neither acknowledged nor remembered: its id is the message's own, which the
		// dedupe of notifyThenAck would take for a repeat.
		var f dto.AgentMessage
		if json.Unmarshal(data, &f) != nil || f.Message.ID == "" {
			return nil
		}
		return l.notify.Notify(NotificationMethod, FormatUpdate(head.Type, f))
	case dto.AgentFrameTypePlatform:
		var f dto.AgentPlatformFrame
		if json.Unmarshal(data, &f) != nil || f.Event.ID == "" {
			return nil
		}
		ack, _ := json.Marshal(dto.AgentPlatformAckFrame{Type: dto.AgentFrameTypePlatformAck, ItemID: f.Event.ID})
		return l.notifyThenAck(ctx, ws, f.Event.ID, func() Notification { return FormatPlatform(f.Event) }, ack)
	case dto.AgentFrameTypeNotice:
		var f dto.AgentNoticeFrame
		if json.Unmarshal(data, &f) != nil {
			return nil
		}
		return l.notify.Notify(NotificationMethod, FormatNotice(f))
	case dto.AgentFrameTypeWelcome:
		var f dto.AgentWelcomeFrame
		if json.Unmarshal(data, &f) != nil {
			return nil
		}
		if err := l.announceIdentity(f); err != nil {
			return err
		}
		return l.announceReconnect()
	}
	return nil // unknown frames need nothing: a client ignores the types a newer server adds
}

// notifyThenAck hands the frame with this id to the harness and then acknowledges it. Delivery is
// at least once: an id already notified is acknowledged without a second notification (AGT-10,
// CID-25), and the ack follows the write to the harness (AGT-07). A failed ack ends the connection.
func (l *link) notifyThenAck(ctx context.Context, ws *websocket.Conn, id string, notification func() Notification, ack []byte) error {
	if !l.seenBefore(id) {
		if err := l.notify.Notify(NotificationMethod, notification()); err != nil {
			return err
		}
		l.remember(id)
	}
	actx, cancel := context.WithTimeout(ctx, ackTimeout)
	defer cancel()
	if err := ws.Write(actx, websocket.MessageText, ack); err != nil {
		return errAck
	}
	return nil
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
