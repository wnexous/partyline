package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Hook names of `partyline hook <name>` (TURN-02).
const (
	CodexStopHook        = "codex-stop"
	GeminiAfterAgentHook = "gemini-after-agent"
)

// Hook timeouts: Codex counts seconds, Gemini milliseconds.
const (
	codexHookTimeoutSec = 30
	geminiHookTimeoutMs = 30000
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
	return []string{"mcp", "add", "--scope", "user", ServerName, "--", c.env.Binary, "channel"}
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

func (c *claude) Entries(context.Context) ([]Entry, error) {
	cmd, err := c.registered()
	if err != nil || cmd == "" {
		return nil, err
	}
	return []Entry{{What: "MCP server in " + c.env.tildePath(c.configPath()), Binary: cmd}}, nil
}

// --- Codex CLI -----------------------------------------------------------------------

// codex registers the tools with `codex mcp add` (~/.codex/config.toml; it replaces an
// existing entry) and a Stop hook in ~/.codex/hooks.json ($CODEX_HOME respected).
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
	return []string{"mcp", "add", ServerName, "--", c.env.Binary, "channel", "--tools-only"}
}

// hookGroup is the Stop hook group Partyline adds.
func (c *codex) hookGroup() any {
	return orderedObject{{"hooks", []any{orderedObject{
		{"type", "command"},
		{"command", hookCommand(c.env.Binary, CodexStopHook)},
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
	if bins, _ := hookBinaries(c.hooksPath(), CodexStopHook, codexHookKeys...); len(bins) > 0 {
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

func (c *codex) Entries(ctx context.Context) ([]Entry, error) {
	var out []Entry
	cmd, err := c.mcpCommand(ctx)
	if err != nil {
		return nil, err
	}
	if cmd != "" {
		out = append(out, Entry{What: "MCP server in " + c.env.tildePath(filepath.Join(c.home(), "config.toml")), Binary: cmd})
	}
	bins, err := hookBinaries(c.hooksPath(), CodexStopHook, codexHookKeys...)
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
// replaces an existing entry) and an AfterAgent hook in the same file.
type gemini struct{ env Env }

func (g *gemini) ID() string     { return "gemini" }
func (g *gemini) Title() string  { return "Gemini CLI" }
func (g *gemini) Detected() bool { _, err := g.env.lookPath("gemini"); return err == nil }

func (g *gemini) settingsPath() string { return filepath.Join(g.env.Home, ".gemini", "settings.json") }

func (g *gemini) addArgs() []string {
	return []string{"mcp", "add", "-s", "user", ServerName, g.env.Binary, "channel", "--tools-only"}
}

func (g *gemini) hookGroup() any {
	return orderedObject{
		{"matcher", "*"},
		{"hooks", []any{orderedObject{
			{"name", ServerName},
			{"type", "command"},
			{"command", hookCommand(g.env.Binary, GeminiAfterAgentHook)},
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
	if bins, _ := hookBinaries(g.settingsPath(), GeminiAfterAgentHook, geminiHookKeys...); len(bins) > 0 {
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
	bins, err := hookBinaries(g.settingsPath(), GeminiAfterAgentHook, geminiHookKeys...)
	if err != nil {
		return nil, err
	}
	for _, b := range bins {
		out = append(out, Entry{What: "AfterAgent hook in " + where, Binary: b})
	}
	return out, nil
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

// groupBinaries returns the partyline binaries of the hook commands in a hook group (a
// {"hooks": [{"command": …}]} object).
func groupBinaries(raw []byte, hook string) []string {
	var group struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &group) != nil {
		return nil
	}
	var out []string
	for _, h := range group.Hooks {
		if b := hookBinary(h.Command, hook); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// hookBinaries lists the binaries of the Partyline hooks in the array at keys of path.
func hookBinaries(path, hook string, keys ...string) ([]string, error) {
	d, _, err := readDoc(path)
	if err != nil {
		return nil, err
	}
	arr, err := d.lookup(keys...)
	if err != nil || arr == nil || arr.kind != '[' {
		return nil, err
	}
	var out []string
	for _, e := range arr.elems {
		out = append(out, groupBinaries(d.raw(e), hook)...)
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
		for i, e := range arr.elems {
			if len(groupBinaries(d.raw(e), hook)) > 0 {
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
	n, err := d.removeElems(func(raw []byte) bool { return len(groupBinaries(raw, hook)) > 0 }, keys...)
	if err != nil || n == 0 {
		return err
	}
	return e.saveRemoval(d)
}
