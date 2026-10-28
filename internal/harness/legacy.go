package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Codex CLI, Gemini CLI and Antigravity are no longer supported (AD-034): they have no live push
// channel and no parallel sessions. Older versions of Partyline registered an MCP server and a
// turn-end hook in each; CleanLegacy takes those back, so `partyline setup` leaves no dangling entry.

// Hook names older versions wrote as `partyline hook <name>`.
const (
	codexStopHook        = "codex-stop"
	geminiAfterAgentHook = "gemini-after-agent"
	antigravityStopHook  = "antigravity-stop"
)

// LegacyHooks are the hook names of the removed harnesses: `partyline hook <name>` still accepts them
// and does nothing, so a hook that cleanup has not removed yet does not fail every turn.
var LegacyHooks = []string{codexStopHook, geminiAfterAgentHook, antigravityStopHook}

// LegacyHarness is a harness Partyline no longer supports.
type LegacyHarness struct {
	// ID is the launcher and --harness id, Title the display name.
	ID, Title string
	// Aliases are other spellings of the launcher command.
	Aliases []string
}

// LegacyHarnesses lists the harnesses Partyline no longer supports.
var LegacyHarnesses = []LegacyHarness{
	{ID: "codex", Title: "Codex"},
	{ID: "gemini", Title: "Gemini"},
	{ID: "antigravity", Title: "Antigravity", Aliases: []string{"agy"}},
}

// FindLegacy returns the removed harness a launcher command or a --harness value names.
func FindLegacy(name string) (LegacyHarness, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, l := range LegacyHarnesses {
		if l.ID == name || slices.Contains(l.Aliases, name) {
			return l, true
		}
	}
	return LegacyHarness{}, false
}

// LegacyResult is the outcome of the cleanup for one removed harness.
type LegacyResult struct {
	LegacyHarness
	// Removed is whether the cleanup found and deleted something; it is false when nothing was left
	// (the second run of an idempotent cleanup), and Err is why it could not finish.
	Removed bool
	Err     error
}

// CleanLegacy removes what older versions registered in the harnesses that are no longer supported and
// reports the ones it found something in (or failed on). Only Partyline's own entries go: the
// partyline MCP server that runs `channel --tools-only`, the hooks whose command is exactly the one
// setup wrote for this binary, and the tool rules setup added; everything else in those files stays.
// It is idempotent: with nothing left it changes nothing and reports nothing.
func (e Env) CleanLegacy(ctx context.Context) []LegacyResult {
	steps := map[string]func(context.Context) (bool, error){
		"codex": e.cleanCodex, "gemini": e.cleanGemini, "antigravity": e.cleanAntigravity,
	}
	var out []LegacyResult
	for _, l := range LegacyHarnesses {
		removed, err := steps[l.ID](ctx)
		if removed || err != nil {
			out = append(out, LegacyResult{LegacyHarness: l, Removed: removed, Err: err})
		}
	}
	return out
}

// ownsToolsOnlyServer is whether an MCP server entry is the one older versions wrote: it runs
// `<binary> channel --tools-only …`. A server of that name that runs something else is not Partyline's.
func ownsToolsOnlyServer(args []string) bool {
	return len(args) > 0 && args[0] == "channel" && slices.Contains(args, "--tools-only")
}

// serverEntry reads the MCP server at keys of the JSON file d: its arguments, and whether it is there.
func serverEntry(d *docEdit, keys ...string) (args []string, found bool, err error) {
	n, err := d.lookup(keys...)
	if err != nil || n == nil {
		return nil, false, err
	}
	var s struct {
		Args []string `json:"args"`
	}
	if json.Unmarshal(d.raw(n), &s) != nil {
		return nil, true, nil
	}
	return s.Args, true, nil
}

// --- Codex CLI ------------------------------------------------------------------------

// cleanCodex deletes the partyline MCP server of Codex (`codex mcp remove`, when codex is installed)
// and the Stop hook in $CODEX_HOME/hooks.json (~/.codex).
func (e Env) cleanCodex(ctx context.Context) (bool, error) {
	dir := e.getenv("CODEX_HOME")
	if dir == "" {
		dir = filepath.Join(e.Home, ".codex")
	}
	hooks := filepath.Join(dir, "hooks.json")
	// A broken hooks file stops the cleanup before anything changes.
	if _, _, err := readDoc(hooks); err != nil {
		return false, err
	}
	removed := false
	if _, err := e.lookPath("codex"); err == nil {
		out, err := e.run(ctx, "codex", "mcp", "get", ServerName, "--json")
		if err == nil {
			var got struct {
				Transport struct {
					Args []string `json:"args"`
				} `json:"transport"`
			}
			if json.Unmarshal(jsonPart(out), &got) == nil && ownsToolsOnlyServer(got.Transport.Args) {
				if err := e.runCLI(ctx, "codex", "mcp", "remove", ServerName); err != nil {
					return false, err
				}
				removed = true
			}
		} else if !strings.Contains(string(out), "No MCP server named") {
			return false, fmt.Errorf("codex mcp get %s --json: %w: %s", ServerName, err, strings.TrimSpace(string(out)))
		}
	}
	n, err := e.removeHookGroups(hooks, codexStopHook, "hooks", "Stop")
	return removed || n > 0, err
}

// jsonPart drops anything a CLI prints before the JSON value (Codex prints warnings).
func jsonPart(out []byte) []byte {
	if i := strings.IndexByte(string(out), '{'); i > 0 {
		return out[i:]
	}
	return out
}

// --- Gemini CLI -----------------------------------------------------------------------

// cleanGemini deletes the partyline MCP server and the AfterAgent hook from ~/.gemini/settings.json.
func (e Env) cleanGemini(context.Context) (bool, error) {
	settings := filepath.Join(e.Home, ".gemini", "settings.json")
	d, existed, err := readDoc(settings)
	if err != nil || !existed {
		return false, err
	}
	removed := false
	args, found, err := serverEntry(d, "mcpServers", ServerName)
	if err != nil {
		return false, err
	}
	if found && ownsToolsOnlyServer(args) {
		if _, err := d.del("mcpServers", ServerName); err != nil {
			return false, err
		}
		if err := e.saveRemoval(d); err != nil {
			return false, err
		}
		removed = true
	}
	n, err := e.removeHookGroups(settings, geminiAfterAgentHook, "hooks", "AfterAgent")
	return removed || n > 0, err
}

// --- Antigravity CLI ------------------------------------------------------------------

// antigravityToolRules are the allow rules older versions added to permissions.allow of agy's settings.
var antigravityToolRules = []string{
	"mcp(" + ServerName + "/reply)",
	"mcp(" + ServerName + "/send_message)",
	"mcp(" + ServerName + "/react)",
	"mcp(" + ServerName + "/edit_message)",
	"mcp(" + ServerName + "/ask)",
	"mcp(" + ServerName + "/history)",
	"mcp(" + ServerName + "/room_info)",
	"mcp(" + ServerName + "/list_rooms)",
	"mcp(" + ServerName + "/wait_for_messages)",
}

// cleanAntigravity runs `agy mcp remove partyline` when agy is installed and the server is in
// ~/.gemini/config/mcp_config.json, and deletes the Stop hook of ~/.gemini/config/hooks.json and the tool
// rules of ~/.gemini/antigravity-cli/settings.json.
func (e Env) cleanAntigravity(ctx context.Context) (bool, error) {
	dir := filepath.Join(e.Home, ".gemini", "config")
	hooks := filepath.Join(dir, "hooks.json")
	settings := filepath.Join(e.Home, ".gemini", "antigravity-cli", "settings.json")
	for _, p := range []string{hooks, settings} {
		if _, _, err := readDoc(p); err != nil {
			return false, err
		}
	}
	removed := false
	d, _, err := readDoc(filepath.Join(dir, "mcp_config.json"))
	if err != nil {
		return false, err
	}
	args, found, err := serverEntry(d, "mcpServers", ServerName)
	if err != nil {
		return false, err
	}
	if _, lookErr := e.lookPath("agy"); found && ownsToolsOnlyServer(args) && lookErr == nil {
		if err := e.runCLI(ctx, "agy", "mcp", "remove", ServerName); err != nil {
			return false, err
		}
		removed = true
	}
	n, err := e.removeHookGroups(hooks, antigravityStopHook, ServerName, "Stop")
	if err != nil {
		return removed, err
	}
	m, err := e.removeAllowRules(settings, antigravityToolRules)
	return removed || n > 0 || m > 0, err
}

// removeAllowRules deletes the rules of want from permissions.allow of the settings file at path and
// returns how many it removed; the list and the objects around it go when they end up empty.
func (e Env) removeAllowRules(path string, want []string) (int, error) {
	d, existed, err := readDoc(path)
	if err != nil || !existed {
		return 0, err
	}
	removed := 0
	for _, rule := range want {
		n, err := d.removeElems(func(raw []byte) bool {
			var s string
			return json.Unmarshal(raw, &s) == nil && s == rule
		}, "permissions", "allow")
		if err != nil {
			return 0, err
		}
		removed += n
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, e.saveRemoval(d)
}

// --- shared hook-file helpers ---------------------------------------------------------

// ownsGroup reports whether a hook entry holds a hook with the exact command setup writes for
// this binary: a group (a {"hooks": [{"command": …}]} object) or a flat handler (a
// {"command": …} object, Antigravity's Stop event).
func (e Env) ownsGroup(raw []byte, hook string) bool {
	var group struct {
		Command string `json:"command"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &group) != nil {
		return false
	}
	if group.Command != "" && e.ownsHook(group.Command, hook) {
		return true
	}
	for _, h := range group.Hooks {
		if e.ownsHook(h.Command, hook) {
			return true
		}
	}
	return false
}

// removeHookGroups deletes the Partyline hook groups from the array at keys of path and returns how many.
// A file with nothing else left is deleted, and so is the backup older versions kept.
func (e Env) removeHookGroups(path, hook string, keys ...string) (int, error) {
	d, existed, err := readDoc(path)
	if err != nil || !existed {
		return 0, err
	}
	n, err := d.removeElems(func(raw []byte) bool { return e.ownsGroup(raw, hook) }, keys...)
	if err != nil || n == 0 {
		return 0, err
	}
	return n, e.saveRemoval(d)
}
