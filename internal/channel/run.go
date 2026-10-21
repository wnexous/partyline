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
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
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
	// Agent is the explicit agent name (--name), slugified like the server does: a fixed
	// agent, newest session wins (ID-02). Empty makes the session temporary: the server names
	// it after Project and Harness and the channel keeps that name (ID-01).
	Agent string
	// Project is the session's project (ID-01); Machine its hostname. Both are sent on connect.
	Project string
	Machine string
	// TempDir is where download_attachment saves files (<TempDir>/partyline/<agent>/);
	// empty means os.TempDir().
	TempDir string
	// Version is reported in the initialize answer.
	Version string
	// ToolsOnly serves the tools without the agent WebSocket, for harnesses that get the
	// messages from `partyline listen` (PUSH-02). The agent is registered over REST and the
	// tools act as the agent named in the session file of SessionKey when listen recorded
	// one, else as Agent, else as the temporary agent the registration returned.
	ToolsOnly bool
	// SessionKey names the session file listen writes (see ListenOptions.SessionKey).
	SessionKey string
	// Harness is the harness id sent on connect; HarnessOmp also switches the MCP
	// instructions to OmpInstructions. Empty means Claude Code for the full channel and an
	// unknown harness ("agent") for a tools-only one.
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
	agent := ""
	if o.Agent != "" {
		agent = agents.Slug(o.Agent)
	}
	id := Identity{Name: agent, Project: o.Project, Harness: o.Harness, Machine: o.Machine}
	if id.Harness == "" && !o.ToolsOnly {
		id.Harness = HarnessClaude
	}

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
	dirName := agent
	if dirName == "" {
		dirName = agents.Slug(o.Project) // a temporary session: the project's folder
	}
	t := &tools{dir: filepath.Join(tmp, "partyline", dirName)}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.ToolsOnly {
		path := sessionFile(tmp, o.SessionKey)
		reg := &registration{done: make(chan struct{})}
		name := func() string {
			if n := readSessionName(path); n != "" {
				return n
			}
			if agent != "" {
				return agent
			}
			return reg.wait(toolTimeout)
		}
		t.api = &client{base: base, token: o.Token, agent: name, identity: id, http: o.HTTPClient}
		t.register(srv)
		t.registerWait(srv)
		go reg.run(ctx, t.api, agent, path, o.Stderr)
		err := srv.Serve(ctx)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	l := newLink(base, o.Token, agent, o.HTTPClient, srv, o.Stderr, o.timing)
	l.identity, l.temporary = id, agent == ""
	t.api = &client{base: base, token: o.Token, agent: l.name, identity: id, http: o.HTTPClient}
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

// registration registers a tools-only session's agent once (PUSH-02) and keeps the name the
// server returned for a temporary session.
type registration struct {
	done chan struct{}
	mu   sync.Mutex
	name string
}

// run registers the agent the session's tools act as: the one listen recorded in the session
// file (its temporary name, mode temporary), else the explicit agent, else a new temporary
// agent of the session's project and harness. A failure is reported on stderr; the tools then
// answer with the server's error.
func (r *registration) run(ctx context.Context, c *client, agent, sessionPath string, stderr io.Writer) {
	defer close(r.done)
	name, mode := agent, ""
	if n := readSessionName(sessionPath); n != "" {
		name, mode = n, dto.AgentModeTemporary
	}
	info, err := c.register(ctx, name, mode)
	if err != nil {
		if ctx.Err() == nil {
			label := name
			if label == "" {
				label = "for project " + c.identity.Project
			}
			fmt.Fprintf(stderr, "partyline: cannot register agent %s: %v\n", label, err)
		}
		return
	}
	r.mu.Lock()
	r.name = info.Name
	r.mu.Unlock()
}

// wait returns the registered name once the registration ended, waiting at most d.
func (r *registration) wait(d time.Duration) string {
	select {
	case <-r.done:
	case <-time.After(d):
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.name
}
