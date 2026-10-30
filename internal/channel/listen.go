package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ListenOptions configure Listen.
type ListenOptions struct {
	// URL, Token, Machine, Folder, MainFolder, Home, Harness and Adopt mean what they mean for Run; an
	// empty Harness is sent as unknown ("agent").
	URL        string
	Token      string
	Machine    string
	Folder     string
	MainFolder string
	Home       string
	Harness    string
	Adopt      string
	// Org, Position and Parent mean what they mean for Run.
	Org, Position, Parent string
	// LiveID is the id this process connects with, shared with the tools-only process of the same pi or
	// OpenCode session so its tool calls are this session's (SESS-45). Empty means a random one; a listen
	// process restarted by the adapter with the same id keeps its label (SESS-14).
	LiveID string
	// Takeover means what it means for Run.
	Takeover bool
	// HarnessSession and Principal mean what they mean for Run.
	HarnessSession string
	Principal      bool
	// Version is the partyline client version, sent as X-Partyline-Client-Version on every connection so
	// the server can tell an outdated session (SESS-63); empty sends none.
	Version string

	// Stdin ending (the harness that spawned listen went away) stops Listen; nil never does.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// HTTPClient is used for the WebSocket; nil means a default client.
	HTTPClient *http.Client

	timing timing
	// zone is where the busy text shows times; tests set it, nil is the local zone.
	zone *time.Location
}

// ErrSessionLost is what Listen returns when another session holds the AI (and did not let go
// during the retries) or took it over from this one. The notice has been written; the command
// exits with code 3 and the adapters do not start it again.
var ErrSessionLost = errors.New("the AI's live session is in another process")

// Listen holds the agent WebSocket like the channel and writes one JSON line per pushed
// message or notice to Stdout: the fields of the channel notification's meta plus "content"
// (PUSH-01). A message is acknowledged after its line is written. Listen returns nil when ctx
// ends or Stdin closes, ErrSessionLost after writing the notice line (type "notice") when another
// session holds the AI or took it over (SES-04, SES-05), and an error when writing to Stdout
// fails.
func Listen(ctx context.Context, o ListenOptions) error {
	base, err := parseBase(o.URL)
	if err != nil {
		return err
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.Stdin != nil {
		go func() {
			_, _ = io.Copy(io.Discard, o.Stdin)
			cancel()
		}()
	}

	id := Identity{
		Machine: o.Machine, Folder: o.Folder, MainFolder: o.MainFolder, Home: o.Home, Harness: o.Harness, Adopt: o.Adopt,
		Org: o.Org, Position: o.Position, Parent: o.Parent, HarnessSession: o.HarnessSession,
		ClientVersion: o.Version,
	}
	out := &lineWriter{w: o.Stdout}
	l := newLink(base, o.Token, id, o.HTTPClient, out, o.Stderr, o.timing)
	l.takeover, l.principal, l.zone = o.Takeover, o.Principal, o.zone
	if o.LiveID != "" {
		l.liveID = o.LiveID
	}
	var lost bool
	var noticeErr error
	l.onLost = func(reason lostReason, text string) {
		lost, noticeErr = true, out.Notify(NotificationMethod, connectionNotice(reason, text))
		cancel() // nothing more to do: end the command
	}
	err = l.run(ctx)
	switch {
	case lost && noticeErr != nil:
		return noticeErr
	case lost:
		return ErrSessionLost
	case errors.Is(err, context.Canceled):
		return nil
	}
	return err
}

// lineWriter is the notifier of listen: each notification becomes one JSON line.
type lineWriter struct {
	w io.Writer
}

var initializedNow = func() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

func (lw *lineWriter) Initialized() <-chan struct{} { return initializedNow }

func (lw *lineWriter) Notify(_ string, params any) error {
	n, ok := params.(Notification)
	if !ok {
		return nil
	}
	line := make(map[string]string, len(n.Meta)+1)
	for k, v := range n.Meta {
		line[k] = v
	}
	line["content"] = n.Content
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if _, err := lw.w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write message line: %w", err)
	}
	return nil
}
