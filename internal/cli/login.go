package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// loginFlags are the credentials `partyline login` takes. setup reuses the flow with the
// flags it also accepts (token, a skipped confirmation).
type loginFlags struct {
	url        string
	token      string
	tokenStdin bool
	// yes skips the question that names the approver; setup --yes sets it.
	yes bool
	// name prefixes messages: login or setup.
	name string
}

// loginCmd runs `partyline login` (CLI-01): it saves the instance URL and an agent token,
// either read from stdin or delivered by the browser approval flow, and names the account
// the token belongs to.
func loginCmd(ctx context.Context, args []string, io stdio) int {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(io.stderr)
	var f loginFlags
	f.name = "login"
	flags.StringVar(&f.url, "url", "", "Partyline instance URL (default $PARTYLINE_URL, else the config file)")
	flags.BoolVar(&f.tokenStdin, "token-stdin", false, "read the agent token from the first line of standard input instead of approving this machine in the browser")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	_, code := loginFlow(ctx, f, io)
	return code
}

// login resolves the instance URL, gets an agent token (stdin or device approval), verifies
// it with GET /agent/whoami, saves URL and token and prints the CLI-01 line. It returns the
// saved credentials and an exit code.
func loginFlow(ctx context.Context, f loginFlags, io stdio) (cliconfig.Config, int) {
	name := f.name
	if name == "" {
		name = "login"
	}
	saved, path, err := loadSavedConfig(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: ignoring the current config file: %v\n", name, err)
		saved = cliconfig.Config{}
	}
	p := newPrompter(io)

	// CLI-01: URL from the flag, PARTYLINE_URL, the saved config, else the prompt.
	base := firstNonEmpty(f.url, io.getenv("PARTYLINE_URL"), saved.URL)
	if base == "" {
		base = p.ask("Partyline URL", "")
	}
	base, err = normalizeURL(base)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %v\n", name, err)
		return cliconfig.Config{}, 2
	}
	if err := checkReachable(ctx, base); err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %v\n", name, err)
		return cliconfig.Config{}, 1
	}

	token := strings.TrimSpace(f.token)
	if token == "" && f.tokenStdin {
		line, _ := p.in.ReadString('\n')
		if token = strings.TrimSpace(line); token == "" {
			fmt.Fprintf(io.stderr, "partyline %s: --token-stdin: no token on standard input\n", name)
			return cliconfig.Config{}, 2
		}
	}
	if token == "" {
		got, approver, err := approveInBrowser(ctx, base, io)
		if err != nil {
			fmt.Fprintf(io.stderr, "partyline %s: %v\n", name, err)
			return cliconfig.Config{}, 1
		}
		fmt.Fprintf(io.stdout, "Approved by @%s on %s\n", approver, base)
		if f.yes {
			fmt.Fprintf(io.stderr, "partyline %s: warning: --yes accepted the agent token of @%s without asking; if @%s is not you, run partyline setup --uninstall to revoke it\n", name, approver, approver)
		}
		if !f.yes && !p.confirm(fmt.Sprintf("Save this agent token of @%s on this machine?", approver)) {
			msg := "revoked it"
			if err := revokeAgentToken(ctx, base, got); err != nil {
				msg = fmt.Sprintf("could not revoke it (%v); revoke %q in the web app (Tokens)", err, machineName(io)+" (via setup)")
			}
			fmt.Fprintf(io.stderr, "partyline %s: not saved: the token belongs to @%s; %s.\n", name, approver, msg)
			return cliconfig.Config{}, 1
		}
		token = got
	}

	me, err := agentWhoAmI(ctx, base, token)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: %v\n", name, err)
		return cliconfig.Config{}, 1
	}
	cfg := cliconfig.Config{URL: base, Token: token}
	if err := cliconfig.Save(path, cfg); err != nil {
		fmt.Fprintf(io.stderr, "partyline %s: save the config file: %v\n", name, err)
		return cliconfig.Config{}, 1
	}
	fmt.Fprintf(io.stdout, "Logged in as @%s on %s (machine %s)\n", me.Handle, base, machineName(io))
	return cfg, 0
}

// loadSavedConfig reads the config file; a missing file is empty credentials, not an error.
func loadSavedConfig(io stdio) (cliconfig.Config, string, error) {
	path, err := cliconfig.Path(io.getenv)
	if err != nil {
		return cliconfig.Config{}, "", err
	}
	cfg, err := cliconfig.Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cliconfig.Config{}, path, nil
	}
	return cfg, path, err
}

// agentWhoAmI asks GET /api/v1/agent/whoami who the token belongs to. The route needs no
// folder and no AI: 200 for a valid agent token, 401 for an unknown or revoked one and 403
// "agent token required" for a user token.
func agentWhoAmI(ctx context.Context, base, token string) (dto.IntelligenceRef, error) {
	var me dto.IntelligenceRef
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	resp, err := request(ctx, http.MethodGet, base+"/api/v1/agent/whoami", nil, h)
	if err != nil {
		return me, fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
			return me, fmt.Errorf("unreadable whoami answer: %w", err)
		}
		return me, nil
	case http.StatusUnauthorized:
		return me, errors.New("the server rejected the token (mistyped, revoked or expired); nothing was saved")
	case http.StatusForbidden:
		return me, errors.New("this is not an agent token: create an agent token in the web app (Tokens) or use --browser; nothing was saved")
	default:
		return me, fmt.Errorf("unexpected answer %d while checking the token; nothing was saved", resp.StatusCode)
	}
}
