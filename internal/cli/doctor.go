package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/harness"
)

// check is one line of the doctor report.
type check struct {
	name, detail, fix string
	ok                bool
}

// doctor runs `partyline doctor` (ONB-06): it checks the binary on PATH, the config file,
// the server, the token, every registered harness entry and the versions, prints pass/fail
// with a fix for each failure, and exits 1 when any check failed.
func doctor(ctx context.Context, io stdio) int {
	var checks []check
	add := func(c check) { checks = append(checks, c) }

	env, err := harnessEnv(io)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline doctor:", err)
		return 1
	}
	add(checkOnPath(env, io))

	creds, fileCheck := checkConfigFile(io)
	add(fileCheck)

	serverVersion := ""
	if creds.URL == "" {
		add(check{name: "server", detail: "no instance URL", fix: "run partyline setup"})
	} else if v, err := serverInfo(ctx, creds.URL); err != nil {
		add(check{name: "server", detail: err.Error(), fix: "check the URL and your network, or run partyline setup --url <instance>"})
	} else {
		serverVersion = v
		add(check{name: "server", detail: creds.URL + " is reachable", ok: true})
		add(checkToken(ctx, creds))
	}

	for _, c := range checkHarnesses(ctx, env) {
		add(c)
	}
	add(checkVersions(buildVersion(), serverVersion, creds.URL))

	failed := 0
	for _, c := range checks {
		mark := "ok  "
		if !c.ok {
			mark = "FAIL"
			failed++
		}
		fmt.Fprintf(io.stdout, "%s %s: %s\n", mark, c.name, c.detail)
		if !c.ok && c.fix != "" {
			fmt.Fprintf(io.stdout, "     fix: %s\n", c.fix)
		}
	}
	if failed > 0 {
		fmt.Fprintf(io.stdout, "\n%d check(s) failed.\n", failed)
		return 1
	}
	fmt.Fprintln(io.stdout, "\nAll checks passed.")
	return 0
}

// checkToken requires the server to accept the agent token.
func checkToken(ctx context.Context, creds cliconfig.Config) check {
	if creds.Token == "" {
		return check{name: "token", detail: "no agent token", fix: "run partyline setup"}
	}
	if err := verifyAgentToken(ctx, creds.URL, creds.Token); err != nil {
		return check{name: "token", detail: strings.TrimSuffix(err.Error(), "; nothing was saved"), fix: "run partyline setup --browser (or --token <agent token>)"}
	}
	return check{name: "token", detail: "agent token accepted", ok: true}
}

// checkOnPath requires `partyline` on PATH.
func checkOnPath(env harness.Env, io stdio) check {
	lookPath := io.lookPath
	if lookPath == nil {
		lookPath = execLookPath
	}
	if p, err := lookPath("partyline"); err == nil {
		return check{name: "binary", detail: "partyline on PATH at " + p, ok: true}
	}
	dir := filepath.Dir(env.Binary)
	return check{name: "binary", detail: "partyline is not on PATH (this one is " + env.Binary + ")",
		fix: fmt.Sprintf("add it to your shell profile: export PATH=\"%s:$PATH\"", dir)}
}

// checkConfigFile requires a private config file with a URL and a token, and returns the
// credentials the commands use (flags none, environment, then the file).
func checkConfigFile(io stdio) (cliconfig.Config, check) {
	c := check{name: "config"}
	path, err := cliconfig.Path(io.getenv)
	if err != nil {
		c.detail, c.fix = err.Error(), "set HOME"
		return cliconfig.Config{}, c
	}
	file, err := cliconfig.Load(path)
	var np *cliconfig.NotPrivateError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.detail, c.fix = path+" does not exist", "run partyline setup"
	case errors.As(err, &np):
		c.detail = err.Error()
		if np.Dir {
			c.fix = "chmod 700 " + np.Path
		} else {
			c.fix = "chmod 600 " + np.Path
		}
	case err != nil:
		c.detail, c.fix = err.Error(), "run partyline setup"
	case file.URL == "" || file.Token == "":
		c.detail, c.fix = path+" has no instance URL or no agent token", "run partyline setup"
	default:
		c.detail, c.ok = path+" is private (0600)", true
	}
	creds, rerr := cliconfig.Resolve(cliconfig.Config{}, io.getenv)
	if rerr != nil {
		// The file is unusable; the environment may still name the server.
		creds = cliconfig.Config{URL: strings.TrimSpace(io.getenv("PARTYLINE_URL")), Token: strings.TrimSpace(io.getenv("PARTYLINE_TOKEN"))}
	}
	return creds, c
}

// serverInfo requests /healthz and returns the server version it reports.
func serverInfo(ctx context.Context, base string) (string, error) {
	resp, err := request(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/healthz", nil, nil)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s/healthz answered %d", base, resp.StatusCode)
	}
	var h struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	return h.Version, nil
}

// checkHarnesses requires every registered entry of every installed harness to run an
// existing binary.
func checkHarnesses(ctx context.Context, env harness.Env) []check {
	var out []check
	found := false
	for _, r := range harness.All(env) {
		if !r.Detected() {
			continue
		}
		entries, err := r.Entries(ctx)
		if err != nil {
			out = append(out, check{name: r.Title(), detail: err.Error(), fix: "fix the file, then run partyline setup --harness " + r.ID()})
			continue
		}
		if len(entries) == 0 {
			continue
		}
		found = true
		for _, e := range entries {
			if executableFile(e.Binary) {
				out = append(out, check{name: r.Title(), detail: e.What + " runs " + e.Binary, ok: true})
			} else {
				out = append(out, check{name: r.Title(), detail: e.What + " runs " + e.Binary + ", which does not exist",
					fix: "run partyline setup --harness " + r.ID()})
			}
		}
	}
	if !found {
		out = append(out, check{name: "harnesses", detail: "Partyline is not registered in any installed harness", fix: "run partyline setup"})
	}
	return out
}

func executableFile(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// checkVersions reports both versions; two release versions that differ fail.
func checkVersions(local, server, base string) check {
	c := check{name: "versions", detail: fmt.Sprintf("partyline %s, server %s", local, orUnknown(server))}
	switch {
	case server == "":
		c.fix = "the server did not report its version; update the server"
	case local != "dev" && server != "dev" && local != server:
		c.fix = fmt.Sprintf("update this machine: curl -fsSL %s/install.sh | sh", strings.TrimRight(base, "/"))
	default:
		c.ok = true
	}
	return c
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
