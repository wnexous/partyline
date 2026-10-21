package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/wnexous/partyline/internal/agents"
)

// ListenOptions configure Listen.
type ListenOptions struct {
	// URL, Token, Agent, Project, Harness and Machine mean what they mean for Run; an empty
	// Harness is sent as unknown ("agent").
	URL     string
	Token   string
	Agent   string
	Project string
	Harness string
	Machine string
	// SessionKey names the session file where the agent name in use is recorded for a
	// tools-only channel of the same harness process; empty records nothing.
	SessionKey string
	// TempDir holds the session file (<TempDir>/partyline/.sessions); empty means os.TempDir().
	TempDir string

	// Stdin ending (the harness that spawned listen went away) stops Listen; nil never does.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// HTTPClient is used for the WebSocket; nil means a default client.
	HTTPClient *http.Client

	timing timing
}

// Listen holds the agent WebSocket like the channel and writes one JSON line per pushed
// message or notice to Stdout: the fields of the channel notification's meta plus "content"
// (PUSH-01). A message is acknowledged after its line is written. Listen returns nil when ctx
// ends or Stdin closes, and an error when writing to Stdout fails.
func Listen(ctx context.Context, o ListenOptions) error {
	base, err := parseBase(o.URL)
	if err != nil {
		return err
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	if o.TempDir == "" {
		o.TempDir = os.TempDir()
	}
	tmp, err := filepath.Abs(o.TempDir)
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.Stdin != nil {
		go func() {
			_, _ = io.Copy(io.Discard, o.Stdin)
			cancel()
		}()
	}

	agent := ""
	if o.Agent != "" {
		agent = agents.Slug(o.Agent)
	}
	l := newLink(base, o.Token, agent, o.HTTPClient, &lineWriter{w: o.Stdout}, o.Stderr, o.timing)
	l.identity = Identity{Name: agent, Project: o.Project, Harness: o.Harness, Machine: o.Machine, SessionKey: o.SessionKey}
	l.temporary = agent == ""
	var recorded string
	path := sessionFile(tmp, o.SessionKey)
	if path != "" {
		var once sync.Once
		l.onName = func(name string) {
			if err := writeSessionName(path, name); err != nil {
				once.Do(func() { fmt.Fprintln(o.Stderr, "partyline listen: cannot record the agent name for the tools:", err) })
				return
			}
			recorded = name
		}
	}
	err = l.run(ctx)
	if recorded != "" {
		removeSessionName(path, recorded)
	}
	if errors.Is(err, context.Canceled) {
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
