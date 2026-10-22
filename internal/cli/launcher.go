package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// launcherFlags takes the leading --as (also --name, its older spelling) and --project flags
// (also --as=<v>) off args (CLI-01); the rest goes to the harness unchanged. as is the identity
// to adopt; project is accepted and ignored, since AIs no longer belong to projects.
func launcherFlags(args []string) (as, project string, rest []string, err error) {
	for len(args) > 0 {
		flag, value, inline := strings.Cut(args[0], "=")
		if flag != "--as" && flag != "--name" && flag != "--project" {
			break
		}
		if !inline {
			if len(args) < 2 {
				return "", "", nil, fmt.Errorf("%s needs a value", flag)
			}
			value, args = args[1], args[1:]
		}
		args = args[1:]
		if flag == "--project" {
			project = value
		} else {
			as = value
		}
	}
	return as, project, args, nil
}

// launch runs `partyline <harness> [--as <identity>] [args…]`: it replaces this process with
// the harness binary found on PATH, the launcher's own arguments and then args unchanged, in
// this environment with PARTYLINE_FOLDER set to the canonical folder it runs in and
// PARTYLINE_HARNESS to the harness id, which the session's partyline processes read so that
// the channel, listen, the tools-only server and the hooks of one session act as the same AI
// (HAR-03, AID-07), and PARTYLINE_AGENT set from --as to adopt that identity (MIG-02). Without
// the binary it exits 127; when Partyline is not registered in the harness it warns once and
// starts anyway.
func launch(ctx context.Context, l launcher, args []string, io stdio) int {
	as, project, args, err := launcherFlags(args)
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
	if project != "" {
		fmt.Fprintln(io.stderr, projectIgnored(l.id))
	}

	environ := io.environ
	if environ == nil {
		environ = os.Environ
	}
	set := map[string]string{"PARTYLINE_HARNESS": l.id}
	if folder := sessionFolder(stdio{getenv: func(string) string { return "" }, getwd: io.getwd}); folder != "" {
		set["PARTYLINE_FOLDER"] = folder
	}
	if as != "" {
		set["PARTYLINE_AGENT"] = as
	}
	var env []string
	for _, kv := range environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[k]; !replaced {
			env = append(env, kv)
		}
	}
	for _, k := range []string{"PARTYLINE_FOLDER", "PARTYLINE_HARNESS", "PARTYLINE_AGENT"} {
		if v, ok := set[k]; ok {
			env = append(env, k+"="+v)
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
