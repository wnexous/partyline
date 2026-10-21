package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/harness"
)

// harnessEnv describes this machine to the registrars: the user's home, the absolute path of
// the running binary and the hooks tests replace.
func harnessEnv(io stdio) (harness.Env, error) {
	executable := io.executable
	if executable == nil {
		executable = os.Executable
	}
	bin, err := executable()
	if err != nil {
		return harness.Env{}, fmt.Errorf("find the partyline binary: %w", err)
	}
	home := strings.TrimSpace(io.getenv("HOME"))
	if home == "" {
		if home, err = os.UserHomeDir(); err != nil {
			return harness.Env{}, err
		}
	}
	return harness.Env{
		Home: home, Getenv: io.getenv, Binary: bin, Version: buildVersion(),
		Run: io.runHarness, LookPath: io.lookPath,
	}, nil
}

// selectHarnesses returns the registrars named in list ("claude,codex"), or every detected
// one when list is empty; "none" selects nothing.
func selectHarnesses(env harness.Env, list string) ([]harness.Registrar, error) {
	all := harness.All(env)
	list = strings.TrimSpace(list)
	if list == "none" {
		return nil, nil
	}
	if list == "" {
		var out []harness.Registrar
		for _, r := range all {
			if r.Detected() {
				out = append(out, r)
			}
		}
		return out, nil
	}
	byID := map[string]harness.Registrar{}
	var ids []string
	for _, r := range all {
		byID[r.ID()] = r
		ids = append(ids, r.ID())
	}
	var out []harness.Registrar
	for _, id := range strings.Split(list, ",") {
		r, ok := byID[strings.TrimSpace(id)]
		if !ok {
			return nil, fmt.Errorf("unknown harness %q (known: %s)", strings.TrimSpace(id), strings.Join(ids, ", "))
		}
		out = append(out, r)
	}
	return out, nil
}

// confirm asks a yes/no question; Enter means yes, the end of input means no.
func (p *prompter) confirm(question string) bool {
	fmt.Fprintf(p.out, "%s [Y/n]: ", question)
	line, err := p.in.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		fmt.Fprintln(p.out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}

// registerHarnesses shows each selected harness's changes and applies them after
// confirmation (HAR-01), then names the launcher of each harness it registered (WIZ-01). It
// returns false when a registration failed.
func registerHarnesses(ctx context.Context, regs []harness.Registrar, yes bool, p *prompter, io stdio) bool {
	if len(regs) == 0 {
		fmt.Fprintln(io.stdout, "\nNo supported harness found (Claude Code, Codex CLI, Gemini CLI, OpenCode, pi, omp).\nRegister one later with: partyline setup --harness <name>, then start it with partyline <name>")
		return true
	}
	ok := true
	var started []string
	for _, r := range regs {
		fmt.Fprintf(io.stdout, "\n%s:\n", r.Title())
		for _, step := range r.Plan() {
			fmt.Fprintf(io.stdout, "  %s\n", step)
		}
		if !yes && !p.confirm(fmt.Sprintf("Register Partyline in %s?", r.Title())) {
			fmt.Fprintf(io.stdout, "Skipped %s.\n", r.Title())
			continue
		}
		if err := r.Apply(ctx); err != nil {
			fmt.Fprintf(io.stderr, "partyline setup: %s: %v\n", r.Title(), err)
			ok = false
			continue
		}
		fmt.Fprintf(io.stdout, "Registered Partyline in %s.\n", r.Title())
		started = append(started, "partyline "+r.ID())
	}
	if len(started) > 0 {
		fmt.Fprintf(io.stdout, "\nStart a session with %s.\n", orList(started))
	}
	return ok
}

// orList joins items as "a", "a or b", "a, b or c".
func orList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// uninstall removes what setup added (HAR-04): each harness's entries and files and, when
// no harness list narrows it, the config file.
func uninstall(ctx context.Context, f setupFlags, io stdio) int {
	env, err := harnessEnv(io)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return 1
	}
	regs := harness.All(env)
	if f.harness != "" {
		if regs, err = selectHarnesses(env, f.harness); err != nil {
			fmt.Fprintln(io.stderr, "partyline setup:", err)
			return 2
		}
	}
	p := newPrompter(io)
	code := 0
	removed := false
	for _, r := range regs {
		plan := r.RemovePlan(ctx)
		if len(plan) == 0 {
			continue
		}
		removed = true
		fmt.Fprintf(io.stdout, "\n%s:\n", r.Title())
		for _, step := range plan {
			fmt.Fprintf(io.stdout, "  %s\n", step)
		}
		if !f.yes && !p.confirm(fmt.Sprintf("Remove Partyline from %s?", r.Title())) {
			fmt.Fprintf(io.stdout, "Kept %s.\n", r.Title())
			continue
		}
		if err := r.Remove(ctx); err != nil {
			fmt.Fprintf(io.stderr, "partyline setup: %s: %v\n", r.Title(), err)
			code = 1
			continue
		}
		fmt.Fprintf(io.stdout, "Removed Partyline from %s.\n", r.Title())
	}
	if f.harness == "" {
		if path, err := cliconfig.Path(io.getenv); err == nil {
			if _, err := os.Stat(path); err == nil {
				removed = true
				fmt.Fprintf(io.stdout, "\nCredentials:\n  revoke the agent token on the server\n  delete %s (instance URL and agent token)\n", path)
				if f.yes || p.confirm("Delete the config file and revoke its agent token?") {
					revokeSaved(ctx, path, io)
					if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
						fmt.Fprintln(io.stderr, "partyline setup:", err)
						code = 1
					} else {
						fmt.Fprintln(io.stdout, "Deleted the config file.")
					}
				}
			}
		}
	}
	if !removed {
		fmt.Fprintln(io.stdout, "Nothing to remove: Partyline is not registered in any harness.")
	}
	return code
}

// revokeSaved revokes the agent token of the config file at path on its server (ONB-09). It is
// best effort: the result is reported and the uninstall goes on either way.
func revokeSaved(ctx context.Context, path string, io stdio) {
	cfg, err := cliconfig.Load(path)
	switch {
	case err != nil:
		fmt.Fprintf(io.stdout, "Could not read the agent token (%v); revoke it in the web app (Tokens).\n", err)
	case cfg.Token == "" || cfg.URL == "":
		fmt.Fprintln(io.stdout, "No agent token saved: nothing to revoke.")
	default:
		if err := revokeAgentToken(ctx, cfg.URL, cfg.Token); err != nil {
			fmt.Fprintf(io.stdout, "Could not revoke the agent token on %s: %v. Revoke it in the web app (Tokens).\n", cfg.URL, err)
			return
		}
		fmt.Fprintf(io.stdout, "Revoked the agent token on %s.\n", cfg.URL)
	}
}
