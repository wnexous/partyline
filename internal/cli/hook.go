package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	goio "io"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// hookTimeout bounds the pending-messages request of a turn-end hook.
const hookTimeout = 10 * time.Second

// hookDecisions is the "keep going" decision of each harness's turn-end hook (TURN-02): Codex
// CLI's Stop hook blocks the stop, Gemini CLI's AfterAgent hook denies the response, Antigravity's
// Stop hook continues the loop; all take the reason as the next prompt.
var hookDecisions = map[string]string{
	"codex-stop":         "block",
	"gemini-after-agent": "deny",
	"antigravity-stop":   "continue",
}

// hookCmd runs `partyline hook codex-stop|gemini-after-agent|antigravity-stop`: it reads the hook input (JSON
// with the session's cwd) from stdin, takes the agent's undelivered room messages and platform
// events (the messages that arrived on the connections it operates), and the count of the platform
// messages the server dropped, and prints the harness's decision to continue with them as the
// reason, or {} (stop as usual) when there is nothing. Problems are reported on stderr and never
// block the harness.
func hookCmd(ctx context.Context, args []string, io stdio) int {
	if len(args) < 1 || hookDecisions[args[0]] == "" {
		fmt.Fprintln(io.stderr, "usage: partyline hook codex-stop|gemini-after-agent|antigravity-stop")
		return 2
	}
	name := args[0]
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	always := fs.Bool("always", false, "run even without PARTYLINE_HARNESS, which only `partyline <harness>` sets (partyline setup --always)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	decision := hookDecisions[name]
	allow := func() int {
		fmt.Fprintln(io.stdout, "{}")
		return 0
	}
	// Only a session the launcher started joins Partyline (PARTYLINE_HARNESS, which a harness
	// that strips the hook's environment still has on a launched ancestor) or one installed
	// with `partyline setup --always`. Anything else allows the stop without touching the
	// server, so a plain Codex or Gemini session creates no AI (launch-only).
	io = withLauncherEnv(io)
	if !*always && strings.TrimSpace(io.getenv("PARTYLINE_HARNESS")) == "" {
		return allow()
	}
	var in struct {
		Cwd string `json:"cwd"`
		// Antigravity's hook input has no cwd (the hook runs in the directory of hooks.json): it
		// names the workspaces, and why the loop stopped.
		WorkspacePaths    []string `json:"workspacePaths"`
		TerminationReason string   `json:"terminationReason"`
	}
	data, _ := readAllLimited(io.stdin, 1<<20)
	_ = json.Unmarshal(data, &in)
	// Only a turn that ended normally continues with messages: a cancelled, failed or exhausted
	// Antigravity loop stops (an empty reason is a harness that does not send one).
	if name == "antigravity-stop" && in.TerminationReason != "" && in.TerminationReason != "NO_TOOL_CALL" {
		return allow()
	}
	if in.Cwd == "" && len(in.WorkspacePaths) > 0 {
		in.Cwd = in.WorkspacePaths[0]
	}

	creds, err := cliconfig.Resolve(cliconfig.Config{}, io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline hook:", err)
		return allow()
	}
	if creds.URL == "" || creds.Token == "" {
		fmt.Fprintln(io.stderr, "partyline hook: not configured; run partyline setup")
		return allow()
	}
	// The AI of the folder the session works in, like every other process started there:
	// PARTYLINE_FOLDER (set by the launcher; a harness that strips the hook's environment loses
	// it, and it comes from the nearest launched ancestor, withLauncherEnv), else the cwd the
	// harness reports on stdin, else this process's working directory (AID-07).
	hio := io
	if in.Cwd != "" {
		hio.getwd = func() (string, error) { return in.Cwd, nil }
	}
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	pending, err := channel.Pending(ctx, channel.PendingOptions{URL: creds.URL, Token: creds.Token, Identity: sessionIdentity("", hio)})
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline hook:", err)
		return allow()
	}
	reason := channel.FramePending(pending)
	if reason == "" {
		return allow()
	}
	enc := json.NewEncoder(io.stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]string{"decision": decision, "reason": reason})
	return 0
}

func readAllLimited(r goio.Reader, n int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return goio.ReadAll(goio.LimitReader(r, n))
}
