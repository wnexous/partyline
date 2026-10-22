package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ListenOptions configure Listen.
type ListenOptions struct {
	// URL, Token, Machine, Folder, Home, Harness and Adopt mean what they mean for Run; an empty
	// Harness is sent as unknown ("agent").
	URL     string
	Token   string
	Machine string
	Folder  string
	Home    string
	Harness string
	Adopt   string
	// Takeover means what it means for Run.
	Takeover bool

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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.Stdin != nil {
		go func() {
			_, _ = io.Copy(io.Discard, o.Stdin)
			cancel()
		}()
	}

	id := Identity{Machine: o.Machine, Folder: o.Folder, Home: o.Home, Harness: o.Harness, Adopt: o.Adopt}
	l := newLink(base, o.Token, id, o.HTTPClient, &lineWriter{w: o.Stdout}, o.Stderr, o.timing)
	l.takeover = o.Takeover
	err = l.run(ctx)
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
