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

// namedHarnesses returns the registrars named in list ("claude,pi"); an empty list and
// "none" select nothing. setup uses this for --harness and --remove, where an unset flag must
// not fall back to the detected harnesses.
func namedHarnesses(env harness.Env, list string) ([]harness.Registrar, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	return selectHarnesses(env, list)
}

// selectHarnesses returns the registrars named in list ("claude,pi"), or every detected
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
		if message, removed := removedHarnessMessage(id); removed && !ok {
			return nil, errors.New(message)
		}
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

// harnessState is one row of the setup menu: the registrar and what it is now.
type harnessState struct {
	reg                   harness.Registrar
	installed, registered bool
}

// harnessStates reads the state of every harness on this machine. A harness whose entries
// cannot be read counts as not registered; applying updates in place and removing is
// idempotent, so a stale answer costs nothing.
func harnessStates(ctx context.Context, regs []harness.Registrar) []harnessState {
	states := make([]harnessState, len(regs))
	for i, r := range regs {
		entries, err := r.Entries(ctx)
		states[i] = harnessState{reg: r, installed: r.Detected(), registered: err == nil && len(entries) > 0}
	}
	return states
}

// cleanLegacy removes what older versions of Partyline registered in Codex, Gemini CLI and Antigravity,
// which are no longer supported (AD-034), and says so in one line per harness. It is idempotent: with
// nothing left it prints nothing. A failure is a warning on stderr; it never stops setup. It reports
// whether it removed anything.
func cleanLegacy(ctx context.Context, env harness.Env, io stdio) (removed bool) {
	for _, r := range env.CleanLegacy(ctx) {
		if r.Removed {
			removed = true
			fmt.Fprintf(io.stdout, "Removed the old Partyline setup for %s (no longer supported)\n", r.Title)
		}
		if r.Err != nil {
			fmt.Fprintf(io.stderr, "partyline setup: could not remove the old Partyline setup for %s (no longer supported): %v\n", r.Title, r.Err)
		}
	}
	return removed
}

// maybeSetupMenu opens the checklist right after login when this is a terminal and no
// harness is registered yet (CLI-02).
func maybeSetupMenu(ctx context.Context, io stdio) int {
	if !isTerminal(io, 0) || !isTerminal(io, 1) {
		return 0
	}
	env, err := harnessEnv(io)
	if err != nil {
		return 0
	}
	cleanLegacy(ctx, env, io)
	states := harnessStates(ctx, harness.All(env))
	for _, s := range states {
		if s.registered {
			return 0
		}
	}
	return setupMenu(ctx, states, false, io)
}

// setupMenu shows the checklist (CLI-03) and applies the confirmed selection (CLI-04).
func setupMenu(ctx context.Context, states []harnessState, keepBackups bool, io stdio) int {
	items := make([]MenuItem, len(states))
	for i, s := range states {
		items[i] = MenuItem{
			Title:      s.reg.Title(),
			Installed:  s.installed,
			Registered: s.registered,
			// Registered and installed harnesses start checked, so a plain Enter changes
			// nothing; deselecting one is what removes Partyline from it.
			Selected: s.installed || s.registered,
		}
	}
	menu := Menu{Items: items, In: io.stdin, Out: io.stdout, Width: func() int { return terminalWidth(io) }, raw: io.rawTerm}
	selected, ok, err := menu.Run()
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return 1
	}
	if !ok {
		return 0
	}
	earlier := backupsBefore(states)
	applied, final := applyHarnessSelection(ctx, states, selected, io)
	if applied {
		settleBackups(registrarsOf(states), earlier, keepBackups, io)
	}
	nameFirstLauncher(states, final, io)
	if !applied {
		return 1
	}
	return 0
}

// applyHarnessFlags applies `--harness`/`--all-detected` (register) and `--remove` without a
// menu (CLI-05). Registered harnesses that are not named stay as they are.
func applyHarnessFlags(ctx context.Context, regs []harness.Registrar, register, remove []harness.Registrar, keepBackups bool, io stdio) int {
	if len(register) == 0 && len(remove) == 0 {
		fmt.Fprintln(io.stdout, "No supported harness found (Claude Code, OpenCode, pi, omp).")
		fmt.Fprintln(io.stdout, "Register one later with: partyline setup --harness <name>, then start it with partyline <name>")
		return 0
	}
	earlier := map[string]bool{}
	for _, r := range regs {
		for _, b := range r.Backups() {
			earlier[b] = exists(b)
		}
	}
	ok := true
	first := ""
	for _, r := range regs {
		inRegister, inRemove := containsRegistrar(register, r), containsRegistrar(remove, r)
		if !inRegister && !inRemove {
			continue
		}
		entries, err := r.Entries(ctx)
		registered := err == nil && len(entries) > 0
		if inRegister {
			// Apply updates a registration in place, so naming a harness again refreshes
			// the extension files and records the current --always choice.
			if err := r.Apply(ctx); err != nil {
				fmt.Fprintf(io.stderr, "✗ %s: %v\n", r.Title(), err)
				ok = false
				continue
			}
			if first == "" {
				first = r.ID()
			}
			if registered {
				fmt.Fprintf(io.stdout, "✓ %s: updated\n", r.Title())
			} else {
				fmt.Fprintf(io.stdout, "✓ %s: registered\n", r.Title())
			}
			continue
		}
		if err == nil && !registered {
			continue
		}
		if err := r.Remove(ctx); err != nil {
			fmt.Fprintf(io.stderr, "✗ %s: %v\n", r.Title(), err)
			ok = false
			continue
		}
		fmt.Fprintf(io.stdout, "✓ %s: removed\n", r.Title())
	}
	if ok {
		settleBackups(regs, earlier, keepBackups, io)
	}
	if first != "" {
		fmt.Fprintf(io.stdout, "\nStart a session with partyline %s.\n", first)
	}
	if !ok {
		return 1
	}
	return 0
}

// applyHarnessSelection registers what is selected and not registered yet and removes
// Partyline from what is registered and no longer selected, and reports every harness of the menu
// with one result line: registered or removed when it changed, already registered or not
// registered when it did not, an error on stderr when it failed (CLI-04). It returns whether every
// change succeeded and the resulting state.
func applyHarnessSelection(ctx context.Context, states []harnessState, selected []bool, io stdio) (bool, []bool) {
	ok := true
	final := make([]bool, len(states))
	for i, s := range states {
		final[i] = s.registered
		want := i < len(selected) && selected[i]
		switch {
		case want && !s.registered:
			if err := s.reg.Apply(ctx); err != nil {
				fmt.Fprintf(io.stderr, "✗ %s: %v\n", s.reg.Title(), err)
				ok = false
				continue
			}
			final[i] = true
			fmt.Fprintf(io.stdout, "✓ %s: registered\n", s.reg.Title())
		case !want && s.registered:
			if err := s.reg.Remove(ctx); err != nil {
				fmt.Fprintf(io.stderr, "✗ %s: %v\n", s.reg.Title(), err)
				ok = false
				continue
			}
			final[i] = false
			fmt.Fprintf(io.stdout, "✓ %s: removed\n", s.reg.Title())
		case want:
			fmt.Fprintf(io.stdout, "✓ %s: already registered\n", s.reg.Title())
		default:
			fmt.Fprintf(io.stdout, "- %s: not registered\n", s.reg.Title())
		}
	}
	return ok, final
}

// nameFirstLauncher names the launcher of the first harness that is registered now.
func nameFirstLauncher(states []harnessState, registered []bool, io stdio) {
	for i, s := range states {
		if i < len(registered) && registered[i] {
			fmt.Fprintf(io.stdout, "\nStart a session with partyline %s.\n", s.reg.ID())
			return
		}
	}
}

func backupsBefore(states []harnessState) map[string]bool {
	earlier := map[string]bool{}
	for _, s := range states {
		for _, b := range s.reg.Backups() {
			earlier[b] = exists(b)
		}
	}
	return earlier
}

func registrarsOf(states []harnessState) []harness.Registrar {
	regs := make([]harness.Registrar, len(states))
	for i, s := range states {
		regs[i] = s.reg
	}
	return regs
}

func containsRegistrar(list []harness.Registrar, want harness.Registrar) bool {
	for _, r := range list {
		if r.ID() == want.ID() {
			return true
		}
	}
	return false
}

// isTerminal reports whether fd is a terminal; tests inject stdio.isTerminal.
func isTerminal(io stdio, fd int) bool {
	return io.isTerminal != nil && io.isTerminal(fd)
}

// terminalWidth is the terminal's width for the menu, 80 when it cannot be read.
func terminalWidth(io stdio) int {
	if io.termSize != nil {
		if w, _, err := io.termSize(1); err == nil && w > 0 {
			return w
		}
	}
	return 80
}

// settleBackups deletes the backups this run created (earlier[b] false), or names them when
// keep is set. A backup that existed before the run is left alone; doctor reports it.
func settleBackups(regs []harness.Registrar, earlier map[string]bool, keep bool, io stdio) {
	for _, r := range regs {
		for _, b := range r.Backups() {
			if earlier[b] || !exists(b) {
				continue
			}
			if keep {
				fmt.Fprintf(io.stdout, "\nKept the backup %s\n", b)
				continue
			}
			if err := os.Remove(b); err != nil && !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(io.stderr, "partyline setup: could not delete the backup %s: %v\n", b, err)
			}
		}
	}
}

// exists reports whether something (a file or a link) is at path.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
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
	if f.harness == "" {
		// The old setup of the harnesses that are no longer supported goes too (AD-034).
		removed = cleanLegacy(ctx, env, io)
	}
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
