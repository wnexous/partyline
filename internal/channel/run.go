package channel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/channel/mcp"
)

// ServerName is the MCP server name; Claude Code shows events as <channel source="partyline">.
const ServerName = "partyline"

// Options configure Run.
type Options struct {
	// URL is the Partyline instance base URL (http or https).
	URL string
	// Token is an agent token (pl_agent_...).
	Token string
	// Agent is the agent name; it is slugified like the server does (empty → "agent").
	Agent string
	// UniqueName marks a derived name (not set by the user): when that agent is online in
	// another session, the server assigns the next free "<name>-N" and the channel uses it
	// (NAME-02). An explicit name replaces the older session instead (NAME-03).
	UniqueName bool
	// TempDir is where download_attachment saves files (<TempDir>/partyline/<agent>/);
	// empty means os.TempDir().
	TempDir string
	// Version is reported in the initialize answer.
	Version string
	// ToolsOnly serves the tools without the agent WebSocket, for harnesses that get the
	// messages from `partyline listen` (PUSH-02). The agent is registered over REST and the
	// tools act as the agent named in the session file of SessionKey when listen recorded
	// one, else as Agent (never in unique mode: without a socket there is no welcome).
	ToolsOnly bool
	// SessionKey names the session file listen writes (see ListenOptions.SessionKey).
	SessionKey string
	// Harness names the harness that runs the full channel when it is not Claude Code;
	// HarnessOmp switches the MCP instructions to OmpInstructions. Empty means Claude Code.
	Harness string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// HTTPClient is used for the WebSocket and REST calls; nil means a default client.
	HTTPClient *http.Client

	timing timing
}

// parseBase checks the instance URL.
func parseBase(raw string) (*url.URL, error) {
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("invalid Partyline URL %q: want http(s)://host", raw)
	}
	return base, nil
}

// Run serves MCP on Stdin/Stdout and keeps the agent connected until Stdin ends or ctx is
// done. It returns nil on a normal end, and an error when the notification output fails.
func Run(ctx context.Context, o Options) error {
	base, err := parseBase(o.URL)
	if err != nil {
		return err
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	agent := agents.Slug(o.Agent)

	instructions := Instructions
	switch {
	case o.ToolsOnly:
		instructions = ToolsOnlyInstructions
	case o.Harness == HarnessOmp:
		instructions = OmpInstructions
	}
	srv := mcp.NewServer(o.Stdin, o.Stdout, mcp.ServerInfo{Name: ServerName, Version: o.Version, Instructions: instructions})
	if o.TempDir == "" {
		o.TempDir = os.TempDir()
	}
	tmp, err := filepath.Abs(o.TempDir)
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	t := &tools{dir: filepath.Join(tmp, "partyline", agent)}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.ToolsOnly {
		path := sessionFile(tmp, o.SessionKey)
		name := func() string {
			if n := readSessionName(path); n != "" {
				return n
			}
			return agent
		}
		t.api = &client{base: base, token: o.Token, agent: name, http: o.HTTPClient}
		t.register(srv)
		t.registerWait(srv)
		go t.api.register(ctx, o.Stderr)
		err := srv.Serve(ctx)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	l := newLink(base, o.Token, agent, o.HTTPClient, srv, o.Stderr, o.timing)
	l.unique = o.UniqueName
	t.api = &client{base: base, token: o.Token, agent: l.name, http: o.HTTPClient}
	t.link = l
	t.register(srv)

	linkErr := make(chan error, 1)
	go func() {
		err := l.run(ctx)
		if err != nil {
			cancel() // the output is gone: stop serving
		}
		linkErr <- err
	}()

	serveErr := srv.Serve(ctx)
	cancel()
	// Give the link a moment to close the WebSocket cleanly; exit within 2 s regardless (AGT-03).
	select {
	case err := <-linkErr:
		if serveErr == nil && err != nil && !errors.Is(err, context.Canceled) {
			serveErr = err
		}
	case <-time.After(closeTimeout):
	}
	return serveErr
}
