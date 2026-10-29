package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/harness"
)

// devicePollInterval is how often `setup --browser` asks whether the machine was approved;
// the server refuses faster polls (DEV-04).
const devicePollInterval = 2 * time.Second

// maxMachineName is the longest machine name the server accepts (DEV-01).
const maxMachineName = 64

// setupHTTP is the client setup talks to the server with.
var setupHTTP = &http.Client{Timeout: 30 * time.Second}

// prompter asks questions on stdout and reads the answers from stdin, one line each.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// ask prints question (with the default in brackets) and returns the answer, or def for an
// empty answer or the end of input.
func (p *prompter) ask(question, def string) string {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(p.out, "%s: ", question)
	}
	line, err := p.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && line == "" {
		fmt.Fprintln(p.out)
	}
	if line == "" {
		return def
	}
	return line
}

// setupFlags are the options of `partyline setup`.
type setupFlags struct {
	url, token  string
	tokenStdin  bool
	browser     bool
	yes         bool
	harness     string
	allDetected bool
	remove      string
	uninstall   bool
	keepBackup  bool
	always      bool
}

func newPrompter(io stdio) *prompter { return &prompter{in: bufio.NewReader(io.stdin), out: io.stdout} }

// setupCmd runs `partyline setup` (CLI-03..07): with credentials flags it runs the login step
// first, then registers and removes harnesses, from the checklist menu on a terminal or from
// --harness/--all-detected/--remove without one. Without credentials it requires a saved
// login (CLI-06) and verifies it.
func setupCmd(ctx context.Context, args []string, io stdio) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.stderr)
	var f setupFlags
	flags.StringVar(&f.url, "url", "", "Partyline instance URL (default $PARTYLINE_URL, else the config file)")
	flags.StringVar(&f.token, "token", "", "agent token (else the saved one, else asked); visible to other users in ps, prefer --token-stdin")
	flags.BoolVar(&f.tokenStdin, "token-stdin", false, "read the agent token from the first line of standard input")
	flags.BoolVar(&f.browser, "browser", false, "approve this machine in the web app instead of using a saved token")
	flags.BoolVar(&f.yes, "yes", false, "do not ask; apply the selection as given")
	flags.StringVar(&f.harness, "harness", "", "harnesses to register, comma-separated: claude, opencode, pi, omp, or none")
	flags.BoolVar(&f.always, "always", false, "also start Partyline in sessions not started by `partyline <harness>` (records the choice in the harness config; --always=false undoes it)")
	flags.BoolVar(&f.allDetected, "all-detected", false, "register every harness installed on this machine")
	flags.StringVar(&f.remove, "remove", "", "harnesses to remove Partyline from, comma-separated")
	flags.BoolVar(&f.uninstall, "uninstall", false, "remove what setup added: harness entries, extension files and the config file")
	flags.BoolVar(&f.keepBackup, "keep-backups", false, "keep the <file>.partyline.bak copy of each harness config setup changes (default: deleted after a successful run)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if n := btoi(f.browser) + btoi(f.token != "") + btoi(f.tokenStdin); n > 1 {
		fmt.Fprintln(io.stderr, "partyline setup: use only one of --token, --token-stdin and --browser")
		return 2
	}
	if f.token != "" {
		// ONB-08: command-line arguments are readable by every user of the machine.
		fmt.Fprintln(io.stderr, "partyline setup: warning: --token is visible to other users of this machine (ps, /proc); prefer --token-stdin or the prompt")
	}
	if f.uninstall {
		return uninstall(ctx, f, io)
	}
	env, err := harnessEnv(io)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return 1
	}
	env.Always = f.always
	regs := harness.All(env)
	// A mistyped harness name must not write credentials, so validate the flags first.
	register, err := namedHarnesses(env, f.harness)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return 2
	}
	remove, err := namedHarnesses(env, f.remove)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return 2
	}
	cleanLegacy(ctx, env, io) // AD-035: the old setup of the harnesses that are no longer supported
	if f.allDetected {
		if f.harness != "" {
			fmt.Fprintln(io.stderr, "partyline setup: use only one of --harness and --all-detected")
			return 2
		}
		for _, r := range regs {
			if r.Detected() {
				register = append(register, r)
			}
		}
	}

	saved, _, err := loadSavedConfig(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline setup: ignoring the current config file: %v\n", err)
		saved = cliconfig.Config{}
	}
	loggedIn := saved.URL != "" && saved.Token != ""
	credentials := f.url != "" || f.token != "" || f.tokenStdin || f.browser
	switch {
	case credentials:
		// CLI-07: --url/--token/--token-stdin/--browser run the login step first.
		if _, code := loginFlow(ctx, loginFlags{url: f.url, token: f.token, tokenStdin: f.tokenStdin, yes: f.yes, name: "setup"}, io); code != 0 {
			return code
		}
	case !loggedIn:
		// CLI-06: setup never asks for credentials itself.
		fmt.Fprintln(io.stderr, "partyline setup: Run partyline login first")
		return 1
	default:
		if err := verifyAgentToken(ctx, saved.URL, saved.Token); err != nil {
			fmt.Fprintln(io.stderr, "partyline setup:", err)
			return 1
		}
		fmt.Fprintln(io.stdout, "Using the saved agent token.")
	}

	if f.harness == "" && !f.allDetected && f.remove == "" {
		if !isTerminal(io, 0) || !isTerminal(io, 1) {
			fmt.Fprintln(io.stderr, "partyline setup: no terminal: pass --harness a,b, --all-detected or --remove a,b")
			return 2
		}
		return setupMenu(ctx, harnessStates(ctx, regs), f.keepBackup, io)
	}
	return applyHarnessFlags(ctx, regs, register, remove, f.keepBackup, io)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// normalizeURL accepts an http(s) URL and drops a trailing slash.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("no instance URL: pass --url https://<your instance>")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid instance URL %q: want http(s)://host", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// checkReachable requires GET /healthz to answer 200.
func checkReachable(ctx context.Context, base string) error {
	resp, err := request(ctx, http.MethodGet, base+"/healthz", nil, nil)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", base, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s is not a Partyline server: /healthz answered %d", base, resp.StatusCode)
	}
	return nil
}

// request sends a JSON request (body may be nil) with optional headers.
func request(ctx context.Context, method, target string, body any, header http.Header) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return setupHTTP.Do(req)
}

// decodeError reads the API error envelope of resp (best effort).
func decodeError(resp *http.Response) dto.Error {
	var e dto.ErrorBody
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
	return e.Error
}

// verifyAgentToken asks the server whether token is an active agent token with GET
// /agent/whoami, which needs no folder and no AI.
func verifyAgentToken(ctx context.Context, base, token string) error {
	_, err := agentWhoAmI(ctx, base, token)
	return err
}

// approveInBrowser runs the device flow (DEV-01..03, DEV-06) for an agent token: it creates a
// request named after the machine, prints the approval page (without the code) and the code to
// type there, and polls until the request is approved, denied or expired. It returns the
// delivered agent token and the approver's username.
func approveInBrowser(ctx context.Context, base string, io stdio) (token, approver string, err error) {
	return approveInBrowserKind(ctx, base, io, dto.TokenKindAgent)
}

// approveInBrowserKind is approveInBrowser for the token kind the approval creates: an agent
// token for setup, a runner token for `partyline runner` (CA-01).
func approveInBrowserKind(ctx context.Context, base string, io stdio, kind dto.TokenKind) (token, approver string, err error) {
	machine := machineName(io)
	resp, err := request(ctx, http.MethodPost, base+"/api/v1/devices", dto.CreateDeviceRequest{Machine: machine, Kind: kind}, nil)
	if err != nil {
		return "", "", fmt.Errorf("cannot reach %s: %w", base, err)
	}
	if resp.StatusCode != http.StatusCreated {
		e := decodeError(resp)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return "", "", errors.New("too many approval requests from this address; try again later or use --token")
		}
		return "", "", fmt.Errorf("could not start the approval (%d %s)", resp.StatusCode, e.Message)
	}
	var created dto.DeviceRequestCreated
	err = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if err != nil {
		return "", "", fmt.Errorf("could not start the approval: %w", err)
	}
	fmt.Fprintf(io.stdout, "Open %s in a browser where you are logged in to Partyline and type this code to approve %q:\n\n  %s\n\nWaiting for approval…\n",
		created.ApproveURL, machine, displayCode(created.Code))

	sleep := io.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for {
		if !sleep(ctx, devicePollInterval) {
			return "", "", ctx.Err()
		}
		poll, status, err := pollDevice(ctx, base, created.PollSecret)
		if err != nil {
			return "", "", err
		}
		switch {
		case status == http.StatusTooManyRequests:
			continue
		case status == http.StatusNotFound:
			return "", "", errors.New("approval expired")
		case status != http.StatusOK:
			return "", "", fmt.Errorf("unexpected answer %d while waiting for the approval", status)
		}
		switch poll.Status {
		case dto.DeviceStatusPending:
			continue
		case dto.DeviceStatusDenied:
			return "", "", errors.New("approval denied")
		case dto.DeviceStatusExpired:
			return "", "", errors.New("approval expired")
		case dto.DeviceStatusApproved:
			if poll.Token == "" {
				return "", "", errors.New("the token was already delivered to another poll; run partyline setup again")
			}
			return poll.Token, poll.ApprovedBy, nil
		default:
			return "", "", fmt.Errorf("unexpected approval status %q", poll.Status)
		}
	}
}

func pollDevice(ctx context.Context, base, secret string) (dto.DevicePoll, int, error) {
	resp, err := request(ctx, http.MethodPost, base+"/api/v1/devices/poll", dto.DevicePollRequest{PollSecret: secret}, nil)
	if err != nil {
		return dto.DevicePoll{}, 0, fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var p dto.DevicePoll
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			return p, 0, fmt.Errorf("unreadable approval status: %w", err)
		}
	}
	return p, resp.StatusCode, nil
}

// revokeAgentToken revokes token on the server with DELETE /api/v1/agent/token (ONB-09).
func revokeAgentToken(ctx context.Context, base, token string) error {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	resp, err := request(ctx, http.MethodDelete, base+"/api/v1/agent/token", nil, h)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusUnauthorized:
		return errors.New("the server does not accept this token any more (already revoked or expired)")
	default:
		return fmt.Errorf("the server answered %d %s", resp.StatusCode, decodeError(resp).Message)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// displayCode splits an 8-character code in two halves (ABCD-EFGH) so it is easy to read
// and type; the server ignores the hyphen.
func displayCode(code string) string {
	if len(code) != 8 {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// machineName is the hostname cut to what the server accepts.
func machineName(io stdio) string {
	hostname := io.hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	name, err := hostname()
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if err != nil || name == "" {
		name = "partyline"
	}
	if r := []rune(name); len(r) > maxMachineName {
		name = string(r[:maxMachineName])
	}
	return name
}

// sleepCtx waits d and reports false when ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
