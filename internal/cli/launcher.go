package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/harness"
)

// execLookPath finds programs on PATH; tests use stdio.lookPath instead.
var execLookPath = exec.LookPath

// claudeChannelArgs are the arguments `partyline claude` puts before the user's (HAR-03).
var claudeChannelArgs = []string{"--dangerously-load-development-channels", "server:partyline"}

// launcher is one `partyline <harness>` command (HAR-03): the harness binary (named like the
// registrar id) and the arguments put before the user's.
type launcher struct {
	id    string
	extra []string
}

// launchers are the harnesses `partyline <id>` starts, in the order --help lists them.
var launchers = []launcher{
	{id: "claude", extra: claudeChannelArgs},
	{id: "opencode"},
	{id: "pi"},
	{id: "omp"},
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

// sessionEnvVar is the session key listen and the tools-only channel share (see
// channel.ListenOptions.SessionKey).
const sessionEnvVar = "PARTYLINE_SESSION"

// newSessionID returns a fresh session key: "pl-" and 16 random hex digits, a safe file name.
func newSessionID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "pl-" + hex.EncodeToString(b[:]), nil
}

// launchSessionID is the session key a launch passes on. Where the launcher execs the
// harness (execKeepsPID) it is this process id, which the harness keeps: a tools-only server
// that the harness starts with a filtered environment (Codex passes it no PARTYLINE_* variable)
// falls back to its parent process id, the same key, so the session's hook and tools share
// one agent. Elsewhere it is a fresh random id.
func launchSessionID(io stdio) (string, error) {
	if !execKeepsPID {
		return newSessionID()
	}
	getpid := io.getpid
	if getpid == nil {
		getpid = os.Getpid
	}
	return strconv.Itoa(getpid()), nil
}

// launcherFlags takes the leading --name and --project flags (also --name=<v>) off args
// (CLI-01); the rest goes to the harness unchanged.
func launcherFlags(args []string) (name, project string, rest []string, err error) {
	for len(args) > 0 {
		flag, value, inline := strings.Cut(args[0], "=")
		if flag != "--name" && flag != "--project" {
			break
		}
		if !inline {
			if len(args) < 2 {
				return "", "", nil, fmt.Errorf("%s needs a value", flag)
			}
			value, args = args[1], args[1:]
		}
		args = args[1:]
		if flag == "--name" {
			name = value
		} else {
			project = value
		}
	}
	return name, project, args, nil
}

// launch runs `partyline <harness> [--name <agent>] [--project <project>] [args…]`: it
// replaces this process with the harness binary found on PATH, the launcher's own arguments
// and then args unchanged, in this environment with PARTYLINE_SESSION set to a fresh id, so
// the listen and tools-only processes of this session share their agent name and no other
// session does (HAR-03), PARTYLINE_HARNESS set to the harness id and PARTYLINE_AGENT and
// PARTYLINE_PROJECT set from --name and --project, which the session's partyline processes
// read (CLI-01). Started in the home folder without a project or name it prints the home
// warning (ID-03). Without the binary it exits 127; when Partyline is not registered in the
// harness it warns once and starts anyway.
func launch(ctx context.Context, l launcher, args []string, io stdio) int {
	name, project, args, err := launcherFlags(args)
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

	session, err := launchSessionID(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: session id: %v\n", l.id, err)
		return 1
	}
	environ := io.environ
	if environ == nil {
		environ = os.Environ
	}
	set := map[string]string{sessionEnvVar: session, "PARTYLINE_HARNESS": l.id}
	if name != "" {
		set["PARTYLINE_AGENT"] = name
	}
	if project != "" {
		set["PARTYLINE_PROJECT"] = project
	}
	var env []string
	for _, kv := range environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[k]; !replaced {
			env = append(env, kv)
		}
	}
	for _, k := range []string{sessionEnvVar, "PARTYLINE_HARNESS", "PARTYLINE_AGENT", "PARTYLINE_PROJECT"} {
		if v, ok := set[k]; ok {
			env = append(env, k+"="+v)
		}
	}
	envOf := func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return ""
	}
	if envOf("PARTYLINE_AGENT") == "" && envOf("PARTYLINE_PROJECT") == "" {
		getwd := io.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		if wd, err := getwd(); err == nil && channel.IsHomeDir(wd, envOf("HOME")) {
			fmt.Fprintln(io.stderr, homeWarning(l.id))
		}
	}

	argv := append(append([]string{l.id}, l.extra...), args...)
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
