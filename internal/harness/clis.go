package harness

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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
