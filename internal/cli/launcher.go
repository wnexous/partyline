package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/harness"
)

// execLookPath finds programs on PATH; tests use stdio.lookPath instead.
var execLookPath = exec.LookPath

// claudeChannelArgs are the arguments `partyline claude` puts before the user's (HAR-03).
var claudeChannelArgs = []string{"--dangerously-load-development-channels", "server:partyline"}

// launcherSessionTimeout bounds the lookup of the AI's live session before a launch (SES-01):
// a server that does not answer by then is ignored, so it never holds a person up.
const launcherSessionTimeout = 2 * time.Second

// launcher is one `partyline <harness>` command (HAR-03): the harness binary (named like the
// registrar id) and the arguments put before the user's. live is whether the harness opens a
// live session; the takeover prompt applies to those (claude, opencode, pi, omp). Codex and
// Gemini use the tools only (design Spec issue 6).
type launcher struct {
	id    string
	extra []string
	live  bool
}

// launchers are the harnesses `partyline <id>` starts, in the order --help lists them.
var launchers = []launcher{
	{id: "claude", extra: claudeChannelArgs, live: true},
	{id: "opencode", live: true},
	{id: "pi", live: true},
	{id: "omp", live: true},
	{id: "codex"},
	{id: "gemini"},
}

// launcherAliases are other spellings of a launcher id.
var launcherAliases = map[string]string{"claudecode": "claude", "claude-code": "claude"}

// findLauncher returns the launcher a command name selects.
func findLauncher(name string) (launcher, bool) {
	if id, ok := launcherAliases[name]; ok {
		name = id
	}
	for _, l := range launchers {
		if l.id == name {
			return l, true
		}
	}
	return launcher{}, false
}

// launcherOptions are the leading launcher flags: the identity to adopt (as), the ignored
// project and whether the person already agreed to take a live session over.
type launcherOptions struct {
	as, project string
	takeOver    bool
	// args is what goes to the harness unchanged.
	args []string
}

// launcherFlags takes the leading --take-over, --as (also --name, its older spelling) and
// --project flags (also --as=<v>) off args (CLI-01); the rest goes to the harness unchanged. as
// is the identity to adopt; project is accepted and ignored, since AIs no longer belong to
// projects.
func launcherFlags(args []string) (launcherOptions, error) {
	var opts launcherOptions
	for len(args) > 0 {
		if args[0] == "--take-over" {
			opts.takeOver, args = true, args[1:]
			continue
		}
		flag, value, inline := strings.Cut(args[0], "=")
		if flag != "--as" && flag != "--name" && flag != "--project" {
			break
		}
		if !inline {
			if len(args) < 2 {
				return launcherOptions{}, fmt.Errorf("%s needs a value", flag)
			}
			value, args = args[1], args[1:]
		}
		args = args[1:]
		if flag == "--project" {
			opts.project = value
		} else {
			opts.as = value
		}
	}
	opts.args = args
	return opts, nil
}

// takeoverChoice is what a launch does about an AI whose live session is in another process.
type takeoverChoice int

const (
	// keepTheirs starts the harness without takeover.
	keepTheirs takeoverChoice = iota
	// takeOver starts the harness with PARTYLINE_TAKEOVER=1 (SES-02).
	takeOver
	// cancelLaunch starts nothing (SES-03).
	cancelLaunch
)

// launch runs `partyline <harness> [--take-over] [--as <identity>] [args…]`: it replaces this
// process with the harness binary found on PATH, the launcher's own arguments and then args
// unchanged, in this environment with PARTYLINE_FOLDER set to the canonical folder it runs in
// and PARTYLINE_HARNESS to the harness id, which the session's partyline processes read so that
// the channel, listen, the tools-only server and the hooks of one session act as the same AI
// (HAR-03, AID-07), and PARTYLINE_AGENT set from --as to adopt that identity (MIG-02). A harness
// that opens a live session (claude, opencode, pi, omp) asks first when the AI already has one
// elsewhere (SES-01): yes takes it over with PARTYLINE_TAKEOVER=1 (SES-02), anything else exits
// 1 without starting (SES-03); without a terminal it starts without asking (SES-07). Codex and
// Gemini use the tools only and only announce the live session. Without the binary it exits
// 127; when Partyline is not registered in the harness it warns once and starts anyway.
func launch(ctx context.Context, l launcher, args []string, io stdio) int {
	opts, err := launcherFlags(args)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %v\n", l.id, err)
		return 2
	}
	title := harnessTitle(l.id)
	lookPath := io.lookPath
	if lookPath == nil {
		lookPath = execLookPath
	}
	path, err := lookPath(l.id)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %s is not installed (install it, then run partyline setup)\n", l.id, title)
		return 127
	}
	warnUnregistered(ctx, l.id, title, io)
	if opts.project != "" {
		fmt.Fprintln(io.stderr, projectIgnored(l.id))
	}

	takingOver := false
	if l.live {
		takingOver = opts.takeOver || strings.TrimSpace(io.getenv("PARTYLINE_TAKEOVER")) == "1"
		if !takingOver {
			switch askTakeover(ctx, l, opts.as, io) {
			case cancelLaunch:
				return 1
			case takeOver:
				takingOver = true
			}
		}
	} else {
		announceToolsOnly(ctx, l, opts.as, io)
	}

	environ := io.environ
	if environ == nil {
		environ = os.Environ
	}
	set := map[string]string{"PARTYLINE_HARNESS": l.id}
	if folder := launchFolder(io); folder != "" {
		set["PARTYLINE_FOLDER"] = folder
	}
	if opts.as != "" {
		set["PARTYLINE_AGENT"] = opts.as
	}
	if takingOver {
		set["PARTYLINE_TAKEOVER"] = "1"
	}
	var env []string
	for _, kv := range environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[k]; replaced || k == "PARTYLINE_TAKEOVER" {
			continue
		}
		env = append(env, kv)
	}
	for _, k := range []string{"PARTYLINE_FOLDER", "PARTYLINE_HARNESS", "PARTYLINE_TAKEOVER", "PARTYLINE_AGENT"} {
		if v, ok := set[k]; ok {
			env = append(env, k+"="+v)
		}
	}

	argv := append(append([]string{l.id}, l.extra...), opts.args...)
	run := io.exec
	if run == nil {
		run = execProgram
	}
	code, err := run(path, argv, env)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %v\n", l.id, err)
		return 1
	}
	return code
}

// launchFolder is the canonical folder of the session being started: the launcher's working
// directory, never an inherited PARTYLINE_FOLDER (the new session runs where it was started).
func launchFolder(io stdio) string {
	return sessionFolder(stdio{getenv: func(string) string { return "" }, getwd: io.getwd})
}

// askTakeover asks a person on a terminal whether to take the live session of the folder's AI
// over (SES-01..03, SES-07): without a terminal, with no live session or when the server cannot
// answer it starts without asking.
func askTakeover(ctx context.Context, l launcher, as string, io stdio) takeoverChoice {
	if !onTerminal(0, io) || !onTerminal(2, io) {
		return keepTheirs
	}
	state, ok := lookupSession(ctx, l, as, io)
	if !ok || !state.Online {
		return keepTheirs
	}
	fmt.Fprint(io.stderr, takeoverPrompt(state, io))
	line, _ := bufio.NewReader(io.stdin).ReadString('\n')
	switch answer := strings.TrimSpace(line); {
	case strings.EqualFold(answer, "y"), strings.EqualFold(answer, "yes"):
		return takeOver
	default:
		return cancelLaunch
	}
}

// announceToolsOnly prints the one informational line of Codex and Gemini when the folder's AI
// has a live session elsewhere (design Spec issue 6): those harnesses use the tools only, so
// there is nothing to take over.
func announceToolsOnly(ctx context.Context, l launcher, as string, io stdio) {
	state, ok := lookupSession(ctx, l, as, io)
	if !ok || !state.Online {
		return
	}
	fmt.Fprintf(io.stderr, "%s is also open in another session (%s); this session uses tools only\n",
		sessionName(state), openWhere(state, io))
}

// takeoverPrompt is the SES-01 question: who is open where and since when, then the answer.
func takeoverPrompt(state dto.SessionState, io stdio) string {
	return fmt.Sprintf("%s is open in another session (%s). Take over? [y/N] ", sessionName(state), openWhere(state, io))
}

// openWhere describes the live session in the prompt's parentheses: its readable app and the
// start in the person's local time.
func openWhere(state dto.SessionState, io stdio) string {
	since := time.Now()
	if state.ConnectedAt != nil {
		since = *state.ConnectedAt
	}
	loc := time.Local
	if io.now != nil {
		loc = io.now().Location()
	}
	return fmt.Sprintf("%s, since %s", agents.HarnessLabel(state.App), since.In(loc).Format("15:04"))
}

// sessionName is the AI's display name for the takeover lines.
func sessionName(state dto.SessionState) string {
	if state.Intelligence != nil && strings.TrimSpace(state.Intelligence.DisplayName) != "" {
		return state.Intelligence.DisplayName
	}
	return "This intelligence"
}

// onTerminal reports whether fd is a terminal (stdio.isTerminal; golang.org/x/term for real
// runs).
func onTerminal(fd int, io stdio) bool {
	isTerminal := io.isTerminal
	if isTerminal == nil {
		isTerminal = term.IsTerminal
	}
	return isTerminal(fd)
}

// lookupSession asks GET /agent/session who holds the AI of the folder being started (SES-01),
// giving up after launcherSessionTimeout. ok is false when the launcher has no credentials or
// the server cannot answer, which starts the harness without asking.
func lookupSession(ctx context.Context, l launcher, as string, io stdio) (dto.SessionState, bool) {
	creds, err := cliconfig.Resolve(cliconfig.Config{}, io.getenv)
	if err != nil || creds.URL == "" || creds.Token == "" {
		return dto.SessionState{}, false
	}
	client := io.sessionHTTP
	if client == nil {
		client = &http.Client{Timeout: launcherSessionTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, launcherSessionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(creds.URL, "/")+"/api/v1/agent/session", nil)
	if err != nil {
		return dto.SessionState{}, false
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	identity := sessionIdentity(as, io)
	identity.Folder, identity.Harness = launchFolder(io), l.id
	setIdentityHeaders(req.Header, identity)
	resp, err := client.Do(req)
	if err != nil {
		return dto.SessionState{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return dto.SessionState{}, false
	}
	var state dto.SessionState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return dto.SessionState{}, false
	}
	return state, true
}

// setIdentityHeaders sets the folder headers of a session request like channel.Identity does
// for the channel: the machine and the percent-encoded folder always, the rest when known.
func setIdentityHeaders(h http.Header, id channel.Identity) {
	machine := id.Machine
	if machine == "" {
		machine = channel.UnknownMachine
	}
	h.Set(dto.HeaderMachine, machine)
	h.Set(dto.HeaderFolder, url.PathEscape(id.Folder))
	if id.Home != "" {
		h.Set(dto.HeaderHome, url.PathEscape(id.Home))
	}
	if id.Harness != "" {
		h.Set(dto.HeaderHarness, id.Harness)
	}
	if id.Adopt != "" {
		h.Set(dto.HeaderAdopt, id.Adopt)
	}
}

// harnessTitle is the registrar's name for id ("Claude Code").
func harnessTitle(id string) string {
	for _, r := range harness.All(harness.Env{}) {
		if r.ID() == id {
			return r.Title()
		}
	}
	return id
}

// warnUnregistered prints one line when Partyline is not registered in the harness id (or its
// registration cannot be read); it never stops the launch.
func warnUnregistered(ctx context.Context, id, title string, io stdio) {
	env, err := harnessEnv(io)
	if err != nil {
		return
	}
	for _, r := range harness.All(env) {
		if r.ID() != id {
			continue
		}
		entries, err := r.Entries(ctx)
		switch {
		case err != nil:
			fmt.Fprintf(io.stderr, "partyline %s: cannot read the Partyline registration in %s (%v); run partyline setup --harness %s. Starting anyway.\n", id, title, err, id)
		case len(entries) == 0:
			fmt.Fprintf(io.stderr, "partyline %s: Partyline is not registered in %s; run partyline setup --harness %s. Starting anyway.\n", id, title, id)
		}
		return
	}
}
