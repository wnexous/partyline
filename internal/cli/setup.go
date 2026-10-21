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
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
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
	url, token string
	tokenStdin bool
	browser    bool
	yes        bool
	harness    string
	uninstall  bool
	keepBackup bool
}

func newPrompter(io stdio) *prompter { return &prompter{in: bufio.NewReader(io.stdin), out: io.stdout} }

// setupCmd runs `partyline setup` (ONB-04): it asks for the instance URL (default: the
// flag, PARTYLINE_URL, else the config file, which the served installer pre-fills) and an
// agent token or a browser approval (DEV-01..03), verifies both against the server and
// writes the private config file. Nothing is written when verification fails.
func setupCmd(ctx context.Context, args []string, io stdio) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.stderr)
	var f setupFlags
	flags.StringVar(&f.url, "url", "", "Partyline instance URL (default $PARTYLINE_URL, else the config file)")
	flags.StringVar(&f.token, "token", "", "agent token (else the saved one, else asked); visible to other users in ps, prefer --token-stdin")
	flags.BoolVar(&f.tokenStdin, "token-stdin", false, "read the agent token from the first line of standard input")
	flags.BoolVar(&f.browser, "browser", false, "approve this machine in the web app instead of pasting a token")
	flags.BoolVar(&f.yes, "yes", false, "do not ask; use the defaults and apply every change")
	flags.StringVar(&f.harness, "harness", "", "harnesses to register, comma-separated: claude, codex, gemini, opencode, pi, omp, or none (default: the installed ones)")
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
	regs, err := selectHarnesses(env, f.harness)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return 2
	}
	path, err := cliconfig.Path(io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return 1
	}
	saved, err := cliconfig.Load(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(io.stderr, "partyline setup: ignoring the current config file: %v\n", err)
	}
	if err != nil {
		saved = cliconfig.Config{}
	}
	p := newPrompter(io)

	cfg, code := setupCredentials(ctx, f, saved, p, io)
	if code != 0 {
		return code
	}
	if err := cliconfig.Save(path, cfg); err != nil {
		fmt.Fprintln(io.stderr, "partyline setup: save the config file:", err)
		return 1
	}
	fmt.Fprintf(io.stdout, "Saved the instance URL and agent token to %s\n", path)
	if !registerHarnesses(ctx, regs, f, p, io) {
		return 1
	}
	return 0
}

// setupCredentials picks the URL and token, verifies them and returns them, or an exit code.
func setupCredentials(ctx context.Context, f setupFlags, saved cliconfig.Config, p *prompter, io stdio) (cliconfig.Config, int) {
	def := firstNonEmpty(f.url, io.getenv("PARTYLINE_URL"), saved.URL)
	base := strings.TrimSpace(f.url)
	if base == "" && !f.yes {
		base = p.ask("Partyline URL", def)
	}
	if base == "" {
		base = def
	}
	base, err := normalizeURL(base)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return cliconfig.Config{}, 2
	}
	if err := checkReachable(ctx, base); err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return cliconfig.Config{}, 1
	}

	token := strings.TrimSpace(f.token)
	if f.tokenStdin {
		line, _ := p.in.ReadString('\n')
		if token = strings.TrimSpace(line); token == "" {
			fmt.Fprintln(io.stderr, "partyline setup: --token-stdin: no token on standard input")
			return cliconfig.Config{}, 2
		}
	}
	if token == "" && !f.browser {
		if saved.Token != "" && sameURL(saved.URL, base) {
			token = saved.Token
			fmt.Fprintln(io.stdout, "Using the saved agent token (pass --token or --browser to replace it).")
		}
		if token == "" && !f.yes {
			token = p.ask("Agent token (press Enter to approve this machine in the browser instead)", "")
		}
	}
	if token == "" {
		// ONB-08: the token is saved with the URL setup started with; the poll's URL is not
		// trusted, so an answer naming another server cannot redirect the token.
		got, approver, err := approveInBrowser(ctx, base, io)
		if err != nil {
			fmt.Fprintln(io.stderr, "partyline setup:", err)
			return cliconfig.Config{}, 1
		}
		// DEV-06: name the account the token belongs to, so an approval by someone else
		// (a phished or misdirected code) is noticed before anything is saved.
		fmt.Fprintf(io.stdout, "Approved by @%s on %s\n", approver, base)
		if f.yes {
			// --yes skips the question, so say on stderr (seen even when stdout is not)
			// whose token this is and how to undo it.
			fmt.Fprintf(io.stderr, "partyline setup: warning: --yes accepted the agent token of @%s without asking; if @%s is not you, run partyline setup --uninstall to revoke it\n", approver, approver)
		}
		if !f.yes && !p.confirm(fmt.Sprintf("Save this agent token of @%s on this machine?", approver)) {
			msg := "revoked it"
			if err := revokeAgentToken(ctx, base, got); err != nil {
				msg = fmt.Sprintf("could not revoke it (%v); revoke %q in the web app (Tokens)", err, machineName(io)+" (via setup)")
			}
			fmt.Fprintf(io.stderr, "partyline setup: not saved: the token belongs to @%s; %s.\n", approver, msg)
			return cliconfig.Config{}, 1
		}
		token = got
	}
	if err := verifyAgentToken(ctx, base, token); err != nil {
		fmt.Fprintln(io.stderr, "partyline setup:", err)
		return cliconfig.Config{}, 1
	}
	return cliconfig.Config{URL: base, Token: token}, 0
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

func sameURL(a, b string) bool {
	na, errA := normalizeURL(a)
	nb, errB := normalizeURL(b)
	return errA == nil && errB == nil && na == nb
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

// verifyAgentToken asks the server whether token is an active agent token. The agent API
// answers 401 to unknown or revoked tokens and 403 "agent token required" to user tokens; a
// valid agent token gets 200, or 403 "agent not registered" for a name that never connected.
func verifyAgentToken(ctx context.Context, base, token string) error {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set(dto.HeaderAgent, "partyline-setup")
	resp, err := request(ctx, http.MethodGet, base+"/api/v1/agent/rooms", nil, h)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return errors.New("the server rejected the token (mistyped, revoked or expired); nothing was saved")
	case http.StatusForbidden:
		e := decodeError(resp)
		if strings.Contains(e.Message, "agent not registered") {
			return nil
		}
		return errors.New("this is not an agent token: create an agent token in the web app (Tokens) or use --browser; nothing was saved")
	default:
		return fmt.Errorf("unexpected answer %d while checking the token; nothing was saved", resp.StatusCode)
	}
}

// approveInBrowser runs the device flow (DEV-01..03, DEV-06): it creates a request named after
// the machine, prints the approval page (without the code) and the code to type there, and
// polls until the request is approved, denied or expired. It returns the delivered agent
// token and the approver's username.
func approveInBrowser(ctx context.Context, base string, io stdio) (token, approver string, err error) {
	machine := machineName(io)
	resp, err := request(ctx, http.MethodPost, base+"/api/v1/devices", dto.CreateDeviceRequest{Machine: machine}, nil)
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
