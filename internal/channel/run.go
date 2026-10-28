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

	"github.com/wnexous/partyline/internal/channel/mcp"
	"github.com/wnexous/partyline/internal/handles"
)

// ServerName is the MCP server name; Claude Code shows events as <channel source="partyline">.
const ServerName = "partyline"

// busyKeepsTrying is what the busy notice adds for a channel: it never gives up on the AI (SES-04).
const busyKeepsTrying = "Partyline keeps trying and connects by itself when that session ends."

// Options configure Run.
type Options struct {
	// URL is the Partyline instance base URL (http or https).
	URL string
	// Token is an agent token (pl_agent_...).
	Token string
	// Machine, Folder, MainFolder, Home and Adopt identify the session's AI (Identity): the hostname,
	// the canonical folder (CanonicalFolder), the main worktree folder of a linked worktree (SESS-01,
	// empty outside one), the canonical home folder and the handle or v0.3 name of an unlinked AI to
	// adopt. They are sent on every connection and call.
	Machine    string
	Folder     string
	MainFolder string
	Home       string
	Adopt      string
	// Org, Position and Parent are what the session asked for when it started (Identity): sent on every
	// connection and call, only when set.
	Org, Position, Parent string
	// TempDir is where download_attachment saves files (<TempDir>/partyline/<folder name>/);
	// empty means os.TempDir().
	TempDir string
	// Version is reported in the initialize answer.
	Version string
	// ToolsOnly serves the tools without the agent WebSocket, for harnesses that get the
	// messages from `partyline listen` (PUSH-02). The tools act as the AI of the folder like any
	// process started there.
	ToolsOnly bool
	// LiveID is, for a tools-only process, the id of the `partyline listen` process of the same pi or
	// OpenCode session (SESS-45): sent as X-Partyline-Live with every call, so the server resolves the
	// session the tools act as. Empty for Codex and Gemini, which hold no such process; the full channel
	// ignores it and uses its own.
	LiveID string
	// Harness is the harness id sent on connect; HarnessOmp also switches the MCP
	// instructions to OmpInstructions. Empty means Claude Code for the full channel and an
	// unknown harness ("agent") for a tools-only one.
	Harness string
	// Takeover makes the first connection take over the AI's live session when another
	// session has it (SES-02); later connections, after a drop, never do.
	Takeover bool

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// HTTPClient is used for the WebSocket and REST calls; nil means a default client.
	HTTPClient *http.Client

	timing timing
	// zone is where the busy text shows times; tests set it, nil is the local zone.
	zone *time.Location
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
	id := Identity{
		Machine: o.Machine, Folder: o.Folder, MainFolder: o.MainFolder, Home: o.Home, Harness: o.Harness, Adopt: o.Adopt,
		Org: o.Org, Position: o.Position, Parent: o.Parent,
	}
	if id.Harness == "" && !o.ToolsOnly {
		id.Harness = HarnessClaude
	}
	if o.ToolsOnly {
		id.LiveID = o.LiveID
	}

	instructions := Instructions
	switch {
	case o.ToolsOnly:
		instructions = ToolsOnlyInstructions
		if isLinkedWorktree(id) && id.LiveID == "" {
			// Only a linked-worktree tools-only process without a listen process has no live channel; it is
			// told so (SESS-06). The tools half of pi and OpenCode has one: its listen process holds it.
			instructions += "\n" + WorktreeToolsOnlyNote
		}
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
	t := &tools{dir: filepath.Join(tmp, "partyline", downloadDirName(o.Folder))}
	t.api = &client{base: base, token: o.Token, identity: id, http: o.HTTPClient}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.ToolsOnly {
		t.register(srv)
		t.registerWait(srv)
		err := srv.Serve(ctx)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	l := newLink(base, o.Token, id, o.HTTPClient, srv, o.Stderr, o.timing)
	// The tools post as the process whose socket is connected, so the server records the session
	// each message came from (SESS-29). A tools-only process has no link and sends no live id.
	id.LiveID = l.liveID
	t.api.identity = id
	l.takeover, l.zone, l.announce = o.Takeover, o.zone, true
	l.keepTryingWhenBusy = true // a channel reconnects by itself, it never gives up (SES-04)
	l.onLost = func(reason lostReason, text string) {
		fmt.Fprintf(o.Stderr, "partyline: %s\n", text)
		if reason == lostBusy {
			// The other session may end at any time and Partyline will connect by itself: say so.
			text += " " + busyKeepsTrying
		}
		// Tell the harness once; the tools answer the same sentence from now on (SES-04, SES-05).
		_ = srv.Notify(NotificationMethod, connectionNotice(reason, text))
	}
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

// ServeIdle answers MCP on in/out with no tools, no instructions and no channel capability. A
// harness that starts the server `partyline setup` registered without `partyline <harness>`
// gets this: the session shows no Partyline tools, and nothing registers an AI or touches the
// server (launch-only: only the launcher connects a session).
func ServeIdle(ctx context.Context, in io.Reader, out io.Writer, version string) error {
	srv := mcp.NewServer(in, out, mcp.ServerInfo{Name: ServerName, Version: version, NoChannel: true})
	if err := srv.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// downloadDirName is the directory name under <TempDir>/partyline where an AI's downloads go
// (TOOL-06): the slug of the folder's name, "agent" when there is none.
func downloadDirName(folder string) string {
	if name := handles.Slug(filepath.Base(folder)); name != "" {
		return name
	}
	return "agent"
}
