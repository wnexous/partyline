package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wnexous/partyline/integrations"
)

// extensionMarker is in the first line of every extension or plugin file setup writes;
// uninstall deletes only files that carry it.
const extensionMarker = "written by `partyline setup`"

// extensionHeader is the first line of an installed extension file.
func extensionHeader(version string) string {
	return fmt.Sprintf("// partyline %s: %s; `partyline setup --uninstall` removes it.\n", version, extensionMarker)
}

// fileHarness registers Partyline by editing a JSON config file (one member, every other
// byte kept) and installing an extension or plugin file.
type fileHarness struct {
	env   Env
	id    string
	title string
	cli   string
	// dir is the harness's user config directory; config and extension live under it.
	dir    func() string
	config string   // config file, relative to dir
	keys   []string // where the server entry goes
	value  func(binary string) any
	// command reads the binary of an existing entry (raw JSON of the member).
	command func(d *docEdit, n *node) string
	ext     string // extension file, relative to dir
	source  string // embedded source in package integrations
}

func (h *fileHarness) ID() string    { return h.id }
func (h *fileHarness) Title() string { return h.title }

func (h *fileHarness) configPath() string { return filepath.Join(h.dir(), h.config) }
func (h *fileHarness) extPath() string    { return filepath.Join(h.dir(), h.ext) }

// Detected reports whether the harness CLI is on PATH or its config directory exists.
func (h *fileHarness) Detected() bool {
	if _, err := h.env.lookPath(h.cli); err == nil {
		return true
	}
	fi, err := os.Stat(h.dir())
	return err == nil && fi.IsDir()
}

func (h *fileHarness) Plan() []string {
	return []string{
		fmt.Sprintf("edit %s: set %s = %s", h.env.tildePath(h.configPath()), strings.Join(h.keys, "."), marshal(h.value(h.env.Binary), false, "")),
		fmt.Sprintf("write %s (Partyline %s %s)", h.env.tildePath(h.extPath()), h.env.Version, extensionKind(h.ext)),
	}
}

func extensionKind(rel string) string {
	if strings.Contains(rel, "plugin") {
		return "plugin"
	}
	return "extension"
}

// binaryLine is the line of the pi and OpenCode adapters that names the partyline binary they
// run (`partyline listen`); setup writes the absolute path into it.
const binaryLine = `const PARTYLINE_BINARY = "partyline";`

// extensionContent is the file setup installs: the version header and the embedded source,
// with the absolute binary path in binaryLine.
func (h *fileHarness) extensionContent() ([]byte, error) {
	src, err := integrations.Files.ReadFile(h.source)
	if err != nil {
		return nil, err
	}
	quoted, err := json.Marshal(h.env.Binary) // a JSON string is a valid TypeScript string
	if err != nil {
		return nil, err
	}
	src = bytes.Replace(src, []byte(binaryLine), []byte("const PARTYLINE_BINARY = "+string(quoted)+";"), 1)
	return append([]byte(extensionHeader(h.env.Version)), src...), nil
}

// writtenBySetup reports whether the file at path was written by setup.
func writtenBySetup(path string) (exists, written bool, err error) {
	data, err := os.ReadFile(path) // #nosec G304 -- extension path under the user's home
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return true, false, err
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))
	return true, bytes.Contains(first, []byte(extensionMarker)), nil
}

func (h *fileHarness) Apply(context.Context) error {
	d, existed, err := readDoc(h.configPath())
	if err != nil {
		return err
	}
	exists, written, err := writtenBySetup(h.extPath())
	if err != nil {
		return err
	}
	if exists && !written {
		return fmt.Errorf("%s exists and was not written by partyline setup; move it away and run setup again", h.extPath())
	}
	content, err := h.extensionContent()
	if err != nil {
		return err
	}
	if err := d.set(h.value(h.env.Binary), h.keys...); err != nil {
		return err
	}
	// HAR-05: the extension first, so the config never names a missing extension; when the
	// config cannot be written, the extension goes back to what it was.
	var previous []byte
	if exists {
		if previous, err = os.ReadFile(h.extPath()); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(h.extPath(), content, 0o644); err != nil {
		return err
	}
	if err := writeDoc(d, existed, true); err != nil {
		if exists {
			_ = writeFileAtomic(h.extPath(), previous, 0o644)
		} else {
			_ = os.Remove(h.extPath())
		}
		return err
	}
	return nil
}

func (h *fileHarness) RemovePlan(context.Context) []string {
	var plan []string
	if d, _, err := readDoc(h.configPath()); err == nil {
		if n, _ := d.lookup(h.keys...); n != nil {
			plan = append(plan, fmt.Sprintf("edit %s: remove %s", h.env.tildePath(h.configPath()), strings.Join(h.keys, ".")))
		}
	}
	if _, written, _ := writtenBySetup(h.extPath()); written {
		plan = append(plan, "delete "+h.env.tildePath(h.extPath()))
	}
	return plan
}

func (h *fileHarness) Remove(context.Context) error {
	d, existed, err := readDoc(h.configPath())
	if err != nil {
		return err
	}
	if existed {
		found, err := d.del(h.keys...)
		if err != nil {
			return err
		}
		if found {
			if err := saveRemoval(d); err != nil {
				return err
			}
		}
	}
	if _, written, err := writtenBySetup(h.extPath()); err != nil || !written {
		return err
	}
	return os.Remove(h.extPath())
}

func (h *fileHarness) Entries(context.Context) ([]Entry, error) {
	d, _, err := readDoc(h.configPath())
	if err != nil {
		return nil, err
	}
	n, err := d.lookup(h.keys...)
	if err != nil || n == nil {
		return nil, err
	}
	return []Entry{{What: "MCP server in " + h.env.tildePath(h.configPath()), Binary: h.command(d, n)}}, nil
}

// mcpServer is the entry format of pi's and omp's mcp.json.
type mcpServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// opencodeServer is OpenCode's local MCP server entry.
type opencodeServer struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
	Enabled bool     `json:"enabled"`
}

func commandField(d *docEdit, n *node) string {
	s, _ := stringAt(&docEdit{path: d.path, data: d.raw(n)}, "command")
	return s
}

func opencodeCommand(d *docEdit, n *node) string {
	var s opencodeServer
	if err := json.Unmarshal(d.raw(n), &s); err != nil || len(s.Command) == 0 {
		return ""
	}
	return s.Command[0]
}

// newOpenCode: global opencode.json (key mcp.partyline, the tools only; pushes come from the
// plugin, which runs `partyline listen`) and plugins/partyline.ts.
func newOpenCode(env Env) Registrar {
	return &fileHarness{
		env: env, id: "opencode", title: "OpenCode", cli: "opencode",
		dir: func() string {
			if x := env.getenv("XDG_CONFIG_HOME"); x != "" {
				return filepath.Join(x, "opencode")
			}
			return filepath.Join(env.Home, ".config", "opencode")
		},
		config: "opencode.json", keys: []string{"mcp", ServerName},
		value: func(bin string) any {
			return opencodeServer{Type: "local", Command: []string{bin, "channel", "--tools-only"}, Enabled: true}
		},
		command: opencodeCommand,
		ext:     filepath.Join("plugins", "partyline.ts"), source: "opencode/partyline.ts",
	}
}

// newPi: ~/.pi/agent/mcp.json (the tools only; pushes come from the extension, which runs
// `partyline listen`) and extensions/partyline.ts.
func newPi(env Env) Registrar {
	return &fileHarness{
		env: env, id: "pi", title: "pi", cli: "pi",
		dir:    func() string { return filepath.Join(env.Home, ".pi", "agent") },
		config: "mcp.json", keys: []string{"mcpServers", ServerName},
		value:   func(bin string) any { return mcpServer{Command: bin, Args: []string{"channel", "--tools-only"}} },
		command: commandField,
		ext:     filepath.Join("extensions", "partyline.ts"), source: "pi/partyline.ts",
	}
}

// newOmp: ~/.omp/agent/mcp.json (the full channel: omp hands its notifications to the
// extension, which frames them as <partyline …>; --harness omp makes the MCP instructions
// describe that framing) and extensions/partyline.ts.
func newOmp(env Env) Registrar {
	return &fileHarness{
		env: env, id: "omp", title: "oh-my-pi (omp)", cli: "omp",
		dir:    func() string { return filepath.Join(env.Home, ".omp", "agent") },
		config: "mcp.json", keys: []string{"mcpServers", ServerName},
		value:   func(bin string) any { return mcpServer{Command: bin, Args: []string{"channel", "--harness", "omp"}} },
		command: commandField,
		ext:     filepath.Join("extensions", "partyline.ts"), source: "omp/partyline.ts",
	}
}
