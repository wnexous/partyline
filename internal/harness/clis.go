package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Hook names of `partyline hook <name>` (TURN-02).
const (
	CodexStopHook        = "codex-stop"
	GeminiAfterAgentHook = "gemini-after-agent"
	AntigravityStopHook  = "antigravity-stop"
)

// Hook timeouts: Codex and Antigravity count seconds, Gemini milliseconds.
const (
	codexHookTimeoutSec       = 30
	geminiHookTimeoutMs       = 30000
	antigravityHookTimeoutSec = 30
)

// --- Claude Code ---------------------------------------------------------------------

// claude registers the channel with `claude mcp add --scope user` (stored in ~/.claude.json,
// or $CLAUDE_CONFIG_DIR/.claude.json). `claude mcp add` refuses an existing name, so an
// update removes the old entry first.
type claude struct{ env Env }

func (c *claude) ID() string     { return "claude" }
func (c *claude) Title() string  { return "Claude Code" }
func (c *claude) Detected() bool { _, err := c.env.lookPath("claude"); return err == nil }

func (c *claude) configPath() string {
	if dir := c.env.getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(c.env.Home, ".claude.json")
}

func (c *claude) addArgs() []string {
	return append([]string{"mcp", "add", "--scope", "user", ServerName, "--", c.env.Binary, "channel"}, c.env.alwaysArgs()...)
}

func (c *claude) removeArgs() []string {
	return []string{"mcp", "remove", "--scope", "user", ServerName}
}

// registered returns the command of the user-scope partyline server, or "".
func (c *claude) registered() (string, error) {
	d, _, err := readDoc(c.configPath())
	if err != nil {
		return "", err
	}
	return stringAt(d, "mcpServers", ServerName, "command")
}

func (c *claude) Plan() []string {
	var plan []string
	if cmd, _ := c.registered(); cmd != "" {
		plan = append(plan, "run: claude "+strings.Join(c.removeArgs(), " "))
	}
	return append(plan, "run: claude "+strings.Join(c.addArgs(), " "))
}

func (c *claude) Apply(ctx context.Context) error {
	cmd, err := c.registered()
	if err != nil {
		return err
	}
	if cmd != "" {
		if err := c.env.runCLI(ctx, "claude", c.removeArgs()...); err != nil {
			return err
		}
	}
	return c.env.runCLI(ctx, "claude", c.addArgs()...)
}

func (c *claude) RemovePlan(context.Context) []string {
	if cmd, _ := c.registered(); cmd == "" {
		return nil
	}
	return []string{"run: claude " + strings.Join(c.removeArgs(), " ")}
}

func (c *claude) Remove(ctx context.Context) error {
	cmd, err := c.registered()
	if err != nil || cmd == "" {
		return err
	}
	return c.env.runCLI(ctx, "claude", c.removeArgs()...)
}

func (c *claude) Backups() []string { return nil }

func (c *claude) Entries(context.Context) ([]Entry, error) {
	cmd, err := c.registered()
	if err != nil || cmd == "" {
		return nil, err
	}
	return []Entry{{What: "MCP server in " + c.env.tildePath(c.configPath()), Binary: cmd}}, nil
}

// --- Codex CLI -----------------------------------------------------------------------

// codex registers the tools with `codex mcp add` (~/.codex/config.toml; it replaces an
// existing entry) and a Stop hook in ~/.codex/hooks.json ($CODEX_HOME respected). The server
// runs `channel --tools-only --harness codex`: Codex starts MCP servers without the launcher's
// PARTYLINE_HARNESS, so the harness is named on the command line (ID-01). Entries are
// recognised by their command, so ones written without --harness stay Partyline's.
type codex struct{ env Env }

func (c *codex) ID() string     { return "codex" }
func (c *codex) Title() string  { return "Codex CLI" }
func (c *codex) Detected() bool { _, err := c.env.lookPath("codex"); return err == nil }

func (c *codex) home() string {
	if dir := c.env.getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(c.env.Home, ".codex")
}

func (c *codex) hooksPath() string { return filepath.Join(c.home(), "hooks.json") }

func (c *codex) addArgs() []string {
	return append([]string{"mcp", "add", ServerName, "--", c.env.Binary, "channel", "--tools-only", "--harness", c.ID()}, c.env.alwaysArgs()...)
}

// hookGroup is the Stop hook group Partyline adds.
func (c *codex) hookGroup() any {
	return orderedObject{{"hooks", []any{orderedObject{
		{"type", "command"},
		{"command", c.env.hookCommand(CodexStopHook)},
		{"timeout", codexHookTimeoutSec},
	}}}}
}

var codexHookKeys = []string{"hooks", "Stop"}

func (c *codex) Plan() []string {
	return []string{
		"run: codex " + strings.Join(c.addArgs(), " "),
		fmt.Sprintf("edit %s: add to hooks.Stop %s", c.env.tildePath(c.hooksPath()), marshal(c.hookGroup(), false, "")),
	}
}

func (c *codex) Apply(ctx context.Context) error {
	// Check the hooks file first so a broken file stops the registration before any change.
	if _, _, err := readDoc(c.hooksPath()); err != nil {
		return err
	}
	if _, err := c.env.writeTarget(c.hooksPath()); err != nil {
		return err
	}
	if err := c.env.runCLI(ctx, "codex", c.addArgs()...); err != nil {
		return err
	}
	return c.env.upsertHookGroup(c.hooksPath(), c.hookGroup(), CodexStopHook, codexHookKeys...)
}

// mcpCommand returns the command of the partyline MCP server Codex knows, or "".
func (c *codex) mcpCommand(ctx context.Context) (string, error) {
	out, err := c.env.run(ctx, "codex", "mcp", "get", ServerName, "--json")
	if err != nil {
		if strings.Contains(string(out), "No MCP server named") {
			return "", nil
		}
		return "", fmt.Errorf("codex mcp get %s --json: %w: %s", ServerName, err, strings.TrimSpace(string(out)))
	}
	var got struct {
		Transport struct {
			Command string `json:"command"`
		} `json:"transport"`
	}
	if err := json.Unmarshal(jsonPart(out), &got); err != nil {
		return "", fmt.Errorf("codex mcp get %s --json: %w", ServerName, err)
	}
	return got.Transport.Command, nil
}

func (c *codex) RemovePlan(ctx context.Context) []string {
	var plan []string
	if cmd, _ := c.mcpCommand(ctx); cmd != "" {
		plan = append(plan, "run: codex mcp remove "+ServerName)
	}
	if bins, _ := c.env.hookBinaries(c.hooksPath(), CodexStopHook, codexHookKeys...); len(bins) > 0 {
		plan = append(plan, fmt.Sprintf("edit %s: remove the Partyline Stop hook", c.env.tildePath(c.hooksPath())))
	}
	return plan
}

func (c *codex) Remove(ctx context.Context) error {
	if _, _, err := readDoc(c.hooksPath()); err != nil {
		return err
	}
	cmd, err := c.mcpCommand(ctx)
	if err != nil {
		return err
	}
	if cmd != "" {
		if err := c.env.runCLI(ctx, "codex", "mcp", "remove", ServerName); err != nil {
			return err
		}
	}
	return c.env.removeHookGroups(c.hooksPath(), CodexStopHook, codexHookKeys...)
}

func (c *codex) Backups() []string { return []string{c.hooksPath() + backupSuffix} }

func (c *codex) Entries(ctx context.Context) ([]Entry, error) {
	var out []Entry
	cmd, err := c.mcpCommand(ctx)
	if err != nil {
		return nil, err
	}
	if cmd != "" {
		out = append(out, Entry{What: "MCP server in " + c.env.tildePath(filepath.Join(c.home(), "config.toml")), Binary: cmd})
	}
	bins, err := c.env.hookBinaries(c.hooksPath(), CodexStopHook, codexHookKeys...)
	if err != nil {
		return nil, err
	}
	for _, b := range bins {
		out = append(out, Entry{What: "Stop hook in " + c.env.tildePath(c.hooksPath()), Binary: b})
	}
	return out, nil
}

// jsonPart drops anything a CLI prints before the JSON value (Codex prints warnings).
func jsonPart(out []byte) []byte {
	if i := strings.IndexByte(string(out), '{'); i > 0 {
		return out[i:]
	}
	return out
}

// --- Gemini CLI ----------------------------------------------------------------------

// gemini registers the tools with `gemini mcp add -s user` (~/.gemini/settings.json; it
// replaces an existing entry), as `channel --tools-only --harness gemini` like Codex, and an
// AfterAgent hook in the same file.
type gemini struct{ env Env }

func (g *gemini) ID() string     { return "gemini" }
func (g *gemini) Title() string  { return "Gemini CLI" }
func (g *gemini) Detected() bool { _, err := g.env.lookPath("gemini"); return err == nil }

func (g *gemini) settingsPath() string { return filepath.Join(g.env.Home, ".gemini", "settings.json") }

func (g *gemini) addArgs() []string {
	return append([]string{"mcp", "add", "-s", "user", ServerName, g.env.Binary, "channel", "--tools-only", "--harness", g.ID()}, g.env.alwaysArgs()...)
}

func (g *gemini) hookGroup() any {
	return orderedObject{
		{"matcher", "*"},
		{"hooks", []any{orderedObject{
			{"name", ServerName},
			{"type", "command"},
			{"command", g.env.hookCommand(GeminiAfterAgentHook)},
			{"timeout", geminiHookTimeoutMs},
		}}},
	}
}

var geminiHookKeys = []string{"hooks", "AfterAgent"}

func (g *gemini) Plan() []string {
	return []string{
		"run: gemini " + strings.Join(g.addArgs(), " "),
		fmt.Sprintf("edit %s: add to hooks.AfterAgent %s", g.env.tildePath(g.settingsPath()), marshal(g.hookGroup(), false, "")),
	}
}

func (g *gemini) Apply(ctx context.Context) error {
	if _, _, err := readDoc(g.settingsPath()); err != nil {
		return err
	}
	if _, err := g.env.writeTarget(g.settingsPath()); err != nil {
		return err
	}
	if err := g.env.runCLI(ctx, "gemini", g.addArgs()...); err != nil {
		return err
	}
	return g.env.upsertHookGroup(g.settingsPath(), g.hookGroup(), GeminiAfterAgentHook, geminiHookKeys...)
}

func (g *gemini) mcpCommand() (string, error) {
	d, _, err := readDoc(g.settingsPath())
	if err != nil {
		return "", err
	}
	return stringAt(d, "mcpServers", ServerName, "command")
}

func (g *gemini) RemovePlan(context.Context) []string {
	var plan []string
	if cmd, _ := g.mcpCommand(); cmd != "" {
		plan = append(plan, "run: gemini mcp remove -s user "+ServerName)
	}
	if bins, _ := g.env.hookBinaries(g.settingsPath(), GeminiAfterAgentHook, geminiHookKeys...); len(bins) > 0 {
		plan = append(plan, fmt.Sprintf("edit %s: remove the Partyline AfterAgent hook", g.env.tildePath(g.settingsPath())))
	}
	return plan
}

func (g *gemini) Remove(ctx context.Context) error {
	cmd, err := g.mcpCommand()
	if err != nil {
		return err
	}
	if cmd != "" {
		if err := g.env.runCLI(ctx, "gemini", "mcp", "remove", "-s", "user", ServerName); err != nil {
			return err
		}
	}
	return g.env.removeHookGroups(g.settingsPath(), GeminiAfterAgentHook, geminiHookKeys...)
}

func (g *gemini) Backups() []string { return []string{g.settingsPath() + backupSuffix} }

func (g *gemini) Entries(context.Context) ([]Entry, error) {
	var out []Entry
	cmd, err := g.mcpCommand()
	if err != nil {
		return nil, err
	}
	where := g.env.tildePath(g.settingsPath())
	if cmd != "" {
		out = append(out, Entry{What: "MCP server in " + where, Binary: cmd})
	}
	bins, err := g.env.hookBinaries(g.settingsPath(), GeminiAfterAgentHook, geminiHookKeys...)
	if err != nil {
		return nil, err
	}
	for _, b := range bins {
		out = append(out, Entry{What: "AfterAgent hook in " + where, Binary: b})
	}
	return out, nil
}

// --- Antigravity CLI ---------------------------------------------------------------

// antigravity registers the tools with `agy mcp add` (~/.gemini/config/mcp_config.json, the
// global configuration; adding a name that exists updates it), as `channel --tools-only
// --harness antigravity` like Codex and Gemini, and a Stop hook in the global
// ~/.gemini/config/hooks.json under the hook name "partyline". The Stop event is flat: its
// array holds the handlers directly.
type antigravity struct{ env Env }

func (a *antigravity) ID() string     { return "antigravity" }
func (a *antigravity) Title() string  { return "Antigravity" }
func (a *antigravity) Detected() bool { _, err := a.env.lookPath("agy"); return err == nil }

func (a *antigravity) configDir() string { return filepath.Join(a.env.Home, ".gemini", "config") }

func (a *antigravity) mcpPath() string   { return filepath.Join(a.configDir(), "mcp_config.json") }
func (a *antigravity) hooksPath() string { return filepath.Join(a.configDir(), "hooks.json") }

// settingsPath is the CLI's own settings file; its permissions.allow list holds the tool rules.
func (a *antigravity) settingsPath() string {
	return filepath.Join(a.env.Home, ".gemini", "antigravity-cli", "settings.json")
}

// antigravityToolRules are the allow rules setup adds to permissions.allow of settings.json, one per tool,
// so agy does not ask before every call of a Partyline tool: mcp(<server>/<tool>) is agy's own syntax. Tools
// that read or write local files (send_file, download_attachment, update_profile's avatar) and the ones that
// create things (create_group) and delete messages are left out, and nothing outside the partyline server
// is ever granted.
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

var antigravityAllowKeys = []string{"permissions", "allow"}

func (a *antigravity) addArgs() []string {
	return append([]string{"mcp", "add", ServerName, a.env.Binary, "channel", "--tools-only", "--harness", a.ID()}, a.env.alwaysArgs()...)
}

func (a *antigravity) hookHandler() any {
	return orderedObject{
		{"type", "command"},
		{"command", a.env.hookCommand(AntigravityStopHook)},
		{"timeout", antigravityHookTimeoutSec},
	}
}

// antigravityHookKeys is the array the hook handler lives in: the Stop event of the hook
// named after the server.
var antigravityHookKeys = []string{ServerName, "Stop"}

func (a *antigravity) Plan() []string {
	return []string{
		"run: agy " + strings.Join(a.addArgs(), " "),
		fmt.Sprintf("edit %s: add to partyline.Stop %s", a.env.tildePath(a.hooksPath()), marshal(a.hookHandler(), false, "")),
		fmt.Sprintf("edit %s: add to permissions.allow %s", a.env.tildePath(a.settingsPath()), strings.Join(antigravityToolRules, ", ")),
	}
}

func (a *antigravity) Apply(ctx context.Context) error {
	for _, p := range []string{a.hooksPath(), a.settingsPath()} {
		if _, _, err := readDoc(p); err != nil {
			return err
		}
		if _, err := a.env.writeTarget(p); err != nil {
			return err
		}
	}
	if err := a.env.runCLI(ctx, "agy", a.addArgs()...); err != nil {
		return err
	}
	if err := a.env.upsertHookGroup(a.hooksPath(), a.hookHandler(), AntigravityStopHook, antigravityHookKeys...); err != nil {
		return err
	}
	return a.env.addAllowRules(a.settingsPath(), antigravityToolRules)
}

func (a *antigravity) mcpCommand() (string, error) {
	d, _, err := readDoc(a.mcpPath())
	if err != nil {
		return "", err
	}
	return stringAt(d, "mcpServers", ServerName, "command")
}

func (a *antigravity) RemovePlan(context.Context) []string {
	var plan []string
	if cmd, _ := a.mcpCommand(); cmd != "" {
		plan = append(plan, "run: agy mcp remove "+ServerName)
	}
	if bins, _ := a.env.hookBinaries(a.hooksPath(), AntigravityStopHook, antigravityHookKeys...); len(bins) > 0 {
		plan = append(plan, fmt.Sprintf("edit %s: remove the Partyline Stop hook", a.env.tildePath(a.hooksPath())))
	}
	if present, _ := a.env.allowRulesIn(a.settingsPath(), antigravityToolRules); len(present) > 0 {
		plan = append(plan, fmt.Sprintf("edit %s: remove the Partyline tool rules from permissions.allow", a.env.tildePath(a.settingsPath())))
	}
	return plan
}

func (a *antigravity) Remove(ctx context.Context) error {
	cmd, err := a.mcpCommand()
	if err != nil {
		return err
	}
	if cmd != "" {
		if err := a.env.runCLI(ctx, "agy", "mcp", "remove", ServerName); err != nil {
			return err
		}
	}
	if err := a.env.removeHookGroups(a.hooksPath(), AntigravityStopHook, antigravityHookKeys...); err != nil {
		return err
	}
	return a.env.removeAllowRules(a.settingsPath(), antigravityToolRules)
}

// Backups lists the backups of hooks.json and settings.json, the files setup edits itself; agy
// writes mcp_config.json.
func (a *antigravity) Backups() []string {
	return []string{a.hooksPath() + backupSuffix, a.settingsPath() + backupSuffix}
}

func (a *antigravity) Entries(context.Context) ([]Entry, error) {
	var out []Entry
	cmd, err := a.mcpCommand()
	if err != nil {
		return nil, err
	}
	if cmd != "" {
		out = append(out, Entry{What: "MCP server in " + a.env.tildePath(a.mcpPath()), Binary: cmd})
	}
	bins, err := a.env.hookBinaries(a.hooksPath(), AntigravityStopHook, antigravityHookKeys...)
	if err != nil {
		return nil, err
	}
	for _, b := range bins {
		out = append(out, Entry{What: "Stop hook in " + a.env.tildePath(a.hooksPath()), Binary: b})
	}
	return out, nil
}

// allowRulesIn returns the rules of want that the permissions.allow list at keys of path holds.
func (e Env) allowRulesIn(path string, want []string) ([]string, error) {
	d, _, err := readDoc(path)
	if err != nil {
		return nil, err
	}
	arr, err := d.lookup(antigravityAllowKeys...)
	if err != nil || arr == nil || arr.kind != '[' {
		return nil, err
	}
	var present []string
	for _, el := range arr.elems {
		var s string
		if json.Unmarshal(d.raw(el), &s) == nil && slices.Contains(want, s) && !slices.Contains(present, s) {
			present = append(present, s)
		}
	}
	return present, nil
}

// addAllowRules appends the rules of want that permissions.allow of the settings file at path does not hold
// yet, keeping every other setting and rule as it was.
func (e Env) addAllowRules(path string, want []string) error {
	d, existed, err := readDoc(path)
	if err != nil {
		return err
	}
	have, err := e.allowRulesIn(path, want)
	if err != nil {
		return err
	}
	added := false
	for _, rule := range want {
		if slices.Contains(have, rule) {
			continue
		}
		if err := d.appendElem(rule, antigravityAllowKeys...); err != nil {
			return err
		}
		added = true
	}
	if !added {
		return nil
	}
	return e.writeDoc(d, existed, true)
}

// removeAllowRules deletes the rules of want from permissions.allow of the settings file at path; the list
// and the objects around it go when they end up empty.
func (e Env) removeAllowRules(path string, want []string) error {
	d, existed, err := readDoc(path)
	if err != nil || !existed {
		return err
	}
	removed := 0
	for _, rule := range want {
		n, err := d.removeElems(func(raw []byte) bool {
			var s string
			return json.Unmarshal(raw, &s) == nil && s == rule
		}, antigravityAllowKeys...)
		if err != nil {
			return err
		}
		removed += n
	}
	if removed == 0 {
		return nil
	}
	return e.saveRemoval(d)
}

// --- shared hook-file helpers ---------------------------------------------------------

// stringAt returns the string at keys of d, or "".
func stringAt(d *docEdit, keys ...string) (string, error) {
	n, err := d.lookup(keys...)
	if err != nil || n == nil {
		return "", err
	}
	var s string
	if json.Unmarshal(d.raw(n), &s) != nil {
		return "", nil
	}
	return s, nil
}

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

// hookBinaries lists the binary of each Partyline hook group in the array at keys of path.
func (e Env) hookBinaries(path, hook string, keys ...string) ([]string, error) {
	d, _, err := readDoc(path)
	if err != nil {
		return nil, err
	}
	arr, err := d.lookup(keys...)
	if err != nil || arr == nil || arr.kind != '[' {
		return nil, err
	}
	var out []string
	for _, el := range arr.elems {
		if e.ownsGroup(d.raw(el), hook) {
			out = append(out, e.Binary)
		}
	}
	return out, nil
}

// upsertHookGroup replaces the first Partyline hook group in the array at keys of path with
// group, or appends group when there is none.
func (e Env) upsertHookGroup(path string, group any, hook string, keys ...string) error {
	d, existed, err := readDoc(path)
	if err != nil {
		return err
	}
	arr, err := d.lookup(keys...)
	if err != nil {
		return err
	}
	idx := -1
	if arr != nil && arr.kind == '[' {
		for i, el := range arr.elems {
			if e.ownsGroup(d.raw(el), hook) {
				idx = i
				break
			}
		}
	}
	if idx >= 0 {
		err = d.replaceElem(group, idx, keys...)
	} else {
		err = d.appendElem(group, keys...)
	}
	if err != nil {
		return err
	}
	return e.writeDoc(d, existed, true)
}

// removeHookGroups deletes the Partyline hook groups from the array at keys of path.
func (e Env) removeHookGroups(path, hook string, keys ...string) error {
	d, existed, err := readDoc(path)
	if err != nil || !existed {
		return err
	}
	n, err := d.removeElems(func(raw []byte) bool { return e.ownsGroup(raw, hook) }, keys...)
	if err != nil || n == 0 {
		return err
	}
	return e.saveRemoval(d)
}
