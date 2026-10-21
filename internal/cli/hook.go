package cli

import (
	"context"
	"encoding/json"
	"fmt"
	goio "io"
	"time"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// hookTimeout bounds the pending-messages request of a turn-end hook.
const hookTimeout = 10 * time.Second

// hookDecisions is the "keep going" decision of each harness's turn-end hook (TURN-02): Codex
// CLI's Stop hook blocks the stop, Gemini CLI's AfterAgent hook denies the response; both take
// the reason as the next prompt.
var hookDecisions = map[string]string{
	"codex-stop":         "block",
	"gemini-after-agent": "deny",
}

// hookCmd runs `partyline hook codex-stop|gemini-after-agent`: it reads the hook input (JSON
// with the session's cwd) from stdin, takes the agent's undelivered room messages and prints
// the harness's decision to continue with them as the reason, or {} (stop as usual) when there
// are none. Problems are reported on stderr and never block the harness.
func hookCmd(ctx context.Context, args []string, io stdio) int {
	if len(args) != 1 || hookDecisions[args[0]] == "" {
		fmt.Fprintln(io.stderr, "usage: partyline hook codex-stop|gemini-after-agent")
		return 2
	}
	decision := hookDecisions[args[0]]
	allow := func() int {
		fmt.Fprintln(io.stdout, "{}")
		return 0
	}
	var in struct {
		Cwd string `json:"cwd"`
	}
	data, _ := readAllLimited(io.stdin, 1<<20)
	_ = json.Unmarshal(data, &in)

	creds, err := cliconfig.Resolve(cliconfig.Config{}, io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline hook:", err)
		return allow()
	}
	if creds.URL == "" || creds.Token == "" {
		fmt.Fprintln(io.stderr, "partyline hook: not configured; run partyline setup")
		return allow()
	}
	// The same agent the tools-only channel of this session uses: PARTYLINE_AGENT, else the
	// temporary agent of the session's project and harness (ID-01), which the server returns
	// to both since neither is ever online.
	hio := io
	hio.stderr = goio.Discard // no terminal to warn
	if in.Cwd != "" {
		hio.getwd = func() (string, error) { return in.Cwd, nil }
	}
	id, _ := sessionIdentity("hook", "", "", "", hio)
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	msgs, err := channel.Pending(ctx, channel.PendingOptions{URL: creds.URL, Token: creds.Token, Agent: id.Name, Identity: id})
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline hook:", err)
		return allow()
	}
	if len(msgs) == 0 {
		return allow()
	}
	enc := json.NewEncoder(io.stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]string{"decision": decision, "reason": channel.FrameAll(msgs)})
	return 0
}

func readAllLimited(r goio.Reader, n int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return goio.ReadAll(goio.LimitReader(r, n))
}
