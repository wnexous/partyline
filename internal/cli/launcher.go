package cli

import (
	"context"
	"encoding/json"
	"fmt"
	goio "io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/harness"
)

// execLookPath finds programs on PATH; tests use stdio.lookPath instead.
var execLookPath = exec.LookPath

// worktreeMainFolder finds the main worktree of a linked worktree (channel.MainWorktreeFolder); tests
// replace it so a launch needs no git.
var worktreeMainFolder = channel.MainWorktreeFolder

// claudeChannelArgs are the arguments `partyline claude` puts before the user's (HAR-03).
var claudeChannelArgs = []string{"--dangerously-load-development-channels", "server:partyline"}

// launcherSessionTimeout bounds the lookup of the AI's live session before a launch (SES-01):
// a server that does not answer by then is ignored, so it never holds a person up.
const launcherSessionTimeout = 2 * time.Second

// launcher is one `partyline <harness>` command (HAR-03): the harness binary (named like the
// registrar id) and the arguments put before the user's. live is whether the harness opens a
// live session; those can take the AI over (claude, opencode, pi, omp). Codex and Gemini use the tools
// only (design Spec issue 6).
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
// project, where the AI asks to be put (org, position, parent) and whether the person already
// agreed to take a live session over.
type launcherOptions struct {
	as, project           string
	org, position, parent string
	takeOver              bool
	// args is what goes to the harness unchanged.
	args []string
}

// launcherValueFlags are the leading flags that take a value, as `--x v` or `--x=v`.
var launcherValueFlags = []string{"--as", "--name", "--project", "--org", "--position", "--parent"}

// launcherFlags takes the leading --take-over, --as (also --name, its older spelling), --project,
// --org, --position and --parent flags (also --as=<v>) off args (CLI-01, ORG-04); the rest goes to the
// harness unchanged. as is the identity to adopt; project is accepted and ignored, since AIs no longer
// belong to projects; org, position and parent are where the AI asks to be put.
func launcherFlags(args []string) (launcherOptions, error) {
	var opts launcherOptions
	for len(args) > 0 {
		if args[0] == "--take-over" {
			opts.takeOver, args = true, args[1:]
			continue
		}
		flag, value, inline := strings.Cut(args[0], "=")
		if !slices.Contains(launcherValueFlags, flag) {
			break
		}
		if !inline {
			if len(args) < 2 {
				return launcherOptions{}, fmt.Errorf("%s needs a value", flag)
			}
			value, args = args[1], args[1:]
		}
		args = args[1:]
		switch flag {
		case "--project":
			opts.project = value
		case "--org":
			opts.org = value
		case "--position":
			opts.position = value
		case "--parent":
			opts.parent = value
		default:
			opts.as = value
		}
	}
	opts.args = args
	return opts, nil
}

// launchPlacement is where the AI of a launch asks to be put (ORG-04, POS-07, POS-08): the organization
// (a slug or an id), the name of the position and the @handle or id of the AI that starts it. An empty
// field asks for nothing, and the server applies its defaults (AD-020).
type launchPlacement struct{ org, position, parent string }

// any is whether the launch asked for something.
func (p launchPlacement) any() bool { return p.org != "" || p.position != "" || p.parent != "" }

// placementOf is what a launch asks for: each flag, else its PARTYLINE_ORG, PARTYLINE_POSITION or
// PARTYLINE_PARENT variable; a flag wins, and a blank one is no flag. Only these are read as input: the
// PARTYLINE_LAUNCH_* ones of a session the launcher itself runs in belong to that session.
func placementOf(opts launcherOptions, io stdio) launchPlacement {
	pick := func(flag, variable string) string {
		if v := strings.TrimSpace(flag); v != "" {
			return v
		}
		return strings.TrimSpace(io.getenv(variable))
	}
	return launchPlacement{
		org: pick(opts.org, orgEnvVar), position: pick(opts.position, positionEnvVar), parent: pick(opts.parent, parentEnvVar),
	}
}

// launch runs `partyline <harness> [--take-over] [--as <identity>] [--org <slug>] [--position <name>]
// [--parent <@handle>] [args…]`: it replaces this process with the harness binary found on PATH, the
// launcher's own arguments and then args unchanged, in this environment with PARTYLINE_FOLDER set to
// the canonical folder it runs in, PARTYLINE_MAIN_FOLDER to the main worktree of a linked one (the
// session resolves the AI of the repository from it, SESS-01; a --parent launch sets none, SESS-09) and
// PARTYLINE_HARNESS to the harness id, which the session's partyline processes read so that the channel,
// listen, the tools-only server and the hooks of one session act as the same AI (HAR-03, AID-07), and
// PARTYLINE_AGENT set from --as to adopt that identity (MIG-02). What the AI asks for - --org,
// --position and --parent, else PARTYLINE_ORG,
// PARTYLINE_POSITION and PARTYLINE_PARENT - is consumed: those variables are removed from the harness's
// environment and the values are handed on as PARTYLINE_LAUNCH_ORG, _POSITION and _PARENT, which the
// session's processes send to the server as headers (ORG-04, POS-07, POS-08, AD-020). Placement
// variables the launcher itself inherited from a session it runs in are replaced, never read: a nested
// launch is another AI. When a placement is asked for the server is asked first whether the start would
// be refused, and a refusal is printed and exits 1 without starting anything. A harness that opens a
// live session (claude, opencode, pi, omp) never asks anything: it starts as another session of the AI,
// which the channel announces (SESS-15, SESS-16), or takes the AI over with PARTYLINE_TAKEOVER=1 when
// --take-over or PARTYLINE_TAKEOVER=1 gave the consent (SESS-13). Codex and Gemini use the tools only
// and only announce the live session. Without the binary it exits 127; when Partyline is not
// registered in the harness it warns once and starts anyway.
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
	place := placementOf(opts, io)
	lookup := newSessionLookup(ctx, l, opts.as, place, io)
	if place.any() {
		// A start the server would refuse is not started: the person reads why here, before the harness takes
		// the terminal. What the server cannot tell (unreachable, not configured, an answer that is no refusal)
		// never stops a launch; the session reports its own trouble.
		if res := lookup(); res.refusal != "" {
			fmt.Fprintf(io.stderr, "partyline %s: %s\n", l.id, res.refusal)
			return 1
		}
	}
	warnUnregistered(ctx, l.id, title, io)
	if opts.project != "" {
		fmt.Fprintln(io.stderr, projectIgnored(l.id))
	}

	takingOver := false
	if l.live {
		// Without --take-over the harness connects as another session: there is nothing to ask (SESS-16).
		takingOver = opts.takeOver || strings.TrimSpace(io.getenv("PARTYLINE_TAKEOVER")) == "1"
	} else {
		announceToolsOnly(lookup, io)
	}

	environ := io.environ
	if environ == nil {
		environ = os.Environ
	}
	set := map[string]string{"PARTYLINE_HARNESS": l.id}
	if folder := launchFolder(io); folder != "" {
		set[folderEnvVar] = folder
		// The main worktree travels beside the session's own folder, so the server resolves the AI of the
		// repository (SESS-01). A launch that asked for a parent keeps its own AI (SESS-09).
		if main := launchMainFolder(folder, place.parent); main != "" {
			set[mainFolderVar] = main
		}
	}
	if opts.as != "" {
		set["PARTYLINE_AGENT"] = opts.as
	}
	if takingOver {
		set["PARTYLINE_TAKEOVER"] = "1"
	}
	for name, v := range map[string]string{launchOrgVar: place.org, launchPositionVar: place.position, launchParentVar: place.parent} {
		if v != "" {
			set[name] = v
		}
	}
	var env []string
	for _, kv := range environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[k]; replaced || slices.Contains(consumedVars, k) {
			continue
		}
		env = append(env, kv)
	}
	for _, k := range []string{
		folderEnvVar, mainFolderVar, "PARTYLINE_HARNESS", "PARTYLINE_TAKEOVER", "PARTYLINE_AGENT",
		launchOrgVar, launchPositionVar, launchParentVar,
	} {
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

// consumedVars are the variables a launch takes out of the harness's environment: the consent to take a live
// session over, which only this launch may give; the main worktree folder of the session it may itself run in
// (this launch computes its own); the placement variables it consumed (PARTYLINE_ORG, PARTYLINE_POSITION,
// PARTYLINE_PARENT); and those of the session it may itself run in (PARTYLINE_LAUNCH_*), which this launch
// replaces with its own.
var consumedVars = []string{
	"PARTYLINE_TAKEOVER", mainFolderVar, orgEnvVar, positionEnvVar, parentEnvVar, launchOrgVar, launchPositionVar, launchParentVar,
}

// launchFolder is the canonical folder of the session being started: the launcher's working
// directory, never an inherited PARTYLINE_FOLDER (the new session runs where it was started).
func launchFolder(io stdio) string {
	return sessionFolder(stdio{getenv: func(string) string { return "" }, getwd: io.getwd})
}

// launchMainFolder is the main worktree of folder when it is a linked worktree, empty otherwise and for a
// launch that asked for a parent (SESS-01, SESS-09).
func launchMainFolder(folder, parent string) string {
	if parent != "" || folder == "" {
		return ""
	}
	if main := worktreeMainFolder(folder); main != folder {
		return main
	}
	return ""
}

// announceToolsOnly prints the one informational line of Codex and Gemini when the folder's AI
// has a live session elsewhere (design Spec issue 6): those harnesses use the tools only, so
// there is nothing to take over.
func announceToolsOnly(lookup func() sessionLookup, io stdio) {
	res := lookup()
	state := res.state
	if !res.ok || !state.Online {
		return
	}
	fmt.Fprintf(io.stderr, "%s is also open in another session (%s); this session uses tools only\n",
		sessionName(state), openWhere(state, io))
}

// openWhere describes the live session in the tools-only line's parentheses: its readable app and the
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

// sessionName is the AI's display name for the tools-only line.
func sessionName(state dto.SessionState) string {
	if state.Intelligence != nil && strings.TrimSpace(state.Intelligence.DisplayName) != "" {
		return state.Intelligence.DisplayName
	}
	return "This intelligence"
}

// sessionLookup is what GET /agent/session told the launcher. ok is false when the launcher has no
// credentials or the server could not answer, which starts the harness anyway. refusal is the
// server's message when it refused the placement the launch asked for (403, 409 or 422), empty for any other
// answer.
type sessionLookup struct {
	state   dto.SessionState
	ok      bool
	refusal string
}

// newSessionLookup returns the launch's one question to the server (ORG-05, SESS-06): the first call asks,
// the others repeat the answer, so a placement check and the tools-only line share one request.
func newSessionLookup(ctx context.Context, l launcher, as string, place launchPlacement, io stdio) func() sessionLookup {
	var res sessionLookup
	asked := false
	return func() sessionLookup {
		if !asked {
			res, asked = lookupSession(ctx, l, as, place, io), true
		}
		return res
	}
}

// lookupSession asks GET /agent/session who holds the AI of the folder being started (SES-01) and, when the
// launch asks for a placement, whether the server would refuse it, giving up after launcherSessionTimeout.
func lookupSession(ctx context.Context, l launcher, as string, place launchPlacement, io stdio) sessionLookup {
	creds, err := cliconfig.Resolve(cliconfig.Config{}, io.getenv)
	if err != nil || creds.URL == "" || creds.Token == "" {
		return sessionLookup{}
	}
	client := io.sessionHTTP
	if client == nil {
		client = &http.Client{Timeout: launcherSessionTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, launcherSessionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(creds.URL, "/")+"/api/v1/agent/session", nil)
	if err != nil {
		return sessionLookup{}
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	identity := sessionIdentity(as, io)
	identity.Folder, identity.Harness = launchFolder(io), l.id
	// This launch's own placement, never the one of a session it may run in (sessionIdentity reads that one).
	identity.Org, identity.Position, identity.Parent = place.org, place.position, place.parent
	// The main folder of the folder being launched in, never the one of the session this may run in; a child
	// launch is another AI, which its own folder identifies (SESS-09).
	identity.MainFolder = launchMainFolder(identity.Folder, place.parent)
	setIdentityHeaders(req.Header, identity)
	resp, err := client.Do(req)
	if err != nil {
		return sessionLookup{}
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		var state dto.SessionState
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			return sessionLookup{}
		}
		return sessionLookup{state: state, ok: true}
	case http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity:
		// Only a launch that asked for a placement looks at this (launch); the message is the server's own words.
		var body dto.ErrorBody
		if json.NewDecoder(goio.LimitReader(resp.Body, 4096)).Decode(&body) == nil {
			return sessionLookup{refusal: strings.TrimSpace(body.Error.Message)}
		}
	}
	return sessionLookup{}
}

// setIdentityHeaders sets the folder headers of a session request like channel.Identity does
// for the channel: the machine and the percent-encoded folder always, the rest when known - the home
// folder, the harness, the identity to adopt and the placement the launch asks for (the position
// percent-encoded like the folder).
func setIdentityHeaders(h http.Header, id channel.Identity) {
	machine := id.Machine
	if machine == "" {
		machine = channel.UnknownMachine
	}
	h.Set(dto.HeaderMachine, machine)
	h.Set(dto.HeaderFolder, url.PathEscape(id.Folder))
	if id.MainFolder != "" {
		h.Set(dto.HeaderMainFolder, url.PathEscape(id.MainFolder))
	}
	if id.Home != "" {
		h.Set(dto.HeaderHome, url.PathEscape(id.Home))
	}
	if id.Harness != "" {
		h.Set(dto.HeaderHarness, id.Harness)
	}
	if id.Adopt != "" {
		h.Set(dto.HeaderAdopt, id.Adopt)
	}
	if id.Org != "" {
		h.Set(dto.HeaderOrg, id.Org)
	}
	if id.Position != "" {
		h.Set(dto.HeaderPosition, url.PathEscape(id.Position))
	}
	if id.Parent != "" {
		h.Set(dto.HeaderParent, id.Parent)
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
