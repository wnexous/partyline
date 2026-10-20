# Contract Notes (from harness-onboarding Phase 1: T1-T5)

Binding for Phase 2 (setup, doctor, launcher: T6-T9), Phase 3 (listen, adapters, hooks:
T10-T14), Phase 4 (web: T15-T17) and the Verifier. Types live in `internal/api/dto` (AD-003);
`web/src/lib/api/types.gen.ts` is regenerated. Migration `0009_device_requests.sql`; the next
migration is `0010`.

## Version (ONB-07)
- `cli.buildVersion()` (`internal/cli/version.go`) is the version every command reports: the
  `-ldflags` `cli.Version` when it is not `dev`, else the build info's main module version
  (`go install …@v1.2.3`), else `dev` (`(devel)` or no build info). `version`, the `serve` log
  and the channel's MCP `initialize` answer use it; `doctor` (T9) should too. Tests replace
  `readBuildInfo`.

## Machine config file (ONB-04, ONB-05)
- `internal/cliconfig`: `Path(getenv)` = `$XDG_CONFIG_HOME/partyline/config.json`, else
  `$HOME/.config/partyline/config.json`; `ErrNoPath` when neither is set (tests pass no HOME, so
  they never read the real file). `Config{URL, Token}` as JSON `{"url","token"}`.
- `Load(path)`: refuses (`*NotPrivateError`, text names the fix) a file whose mode is not exactly
  0600 ("run chmod 600 <path>") and a directory with any group/other bit ("run chmod 700 <dir>"),
  before reading; a missing file matches `fs.ErrNotExist`. No permission checks on Windows.
- `Save(path, cfg)`: creates the directory 0700 (and chmods an existing one to 0700), writes a
  temp file 0600 in it and renames it over the file (atomic; an old 0644 file ends up 0600).
- `Resolve(flags, getenv)`: per value flag > `PARTYLINE_URL`/`PARTYLINE_TOKEN` > file. The file
  is read only when a value is still missing (a broken file does not matter when flags/env
  give both); a missing file leaves the values empty without error; a non-private or invalid
  file is an error. `channel` exits 1 with that error, and exits 2 with "… or run partyline
  setup" when URL or token is still missing. `listen` and `doctor` should use `Resolve` too.
- The served installer may write `{"url": "<instance>", "token": ""}` (see install.sh below):
  setup (T6) must treat a config with a URL and an empty token as "URL default known".

## Agent names (NAME-01..03)
- `channel.DeriveName(dir)`: basename of `git rev-parse --show-toplevel` run in `dir` (5 s
  timeout), else `filepath.Base(dir)`, slugified with `agents.Slug`.
- `channel.Options.UniqueName` (set by `partyline channel` when neither `--agent` nor
  `PARTYLINE_AGENT` names the agent). The link then dials with header
  `X-Partyline-Agent-Mode: unique` (`dto.HeaderAgentMode`, `dto.AgentModeUnique`) and records
  the welcome frame's name (`link.name()`); REST tool calls use that name from then on. A
  reconnect sends the assigned name without the mode header (newest wins over its own stale
  socket). After a `replaced` close the assigned name is forgotten and the next dial asks for
  a free name again (after the usual replaced pause). `listen` (T10) and `--tools-only` must
  agree on the name: tools-only has no welcome, so it must not use unique mode blindly.
- Gateway: in unique mode it takes the first of `<slug>`, `<slug>-2` … `<slug>-50` that is not
  an online agent of the owner (an offline existing agent is reused; a gap is filled first),
  shortening the base so `<base>-N` fits 48 characters. One gateway-wide lock covers name
  choice → upsert → upgrade → attach, so concurrent sessions get distinct names. A new name
  beyond `agents.MaxAgentsPerOwner` (50) answers 409 "agent limit reached" as before; all 50
  candidates online also answers 409. Without the header nothing changed (explicit names:
  newest wins, close 4000 `replaced`).
- testkit: `Client.DialAgentUnique(name)`.

## Device approval (DEV-01..04)
- Service `internal/devices` (`App.Devices`, `api.Deps.Devices`). Codes: 8 characters from
  `devices.CodeAlphabet` (A-Z without I/O, 2-9), valid `devices.RequestTTL` (10 min); stored as
  SHA-256 hashes, like the poll secret (a base62 session-style token). `NormalizeCode`
  upper-cases and drops spaces and hyphens.
- `POST /api/v1/devices` (anonymous; agent tokens 403) `CreateDeviceRequest{machine}` → 201
  `DeviceRequestCreated{code, poll_secret, approve_url = <public URL>/devices/<code>,
  expires_at}`. `machine`: trimmed, 1-64 characters, no control characters, else 422
  `machine`. At most `CreateLimit` 10 per `CreateWindow` (1 h, sliding) per address key (IPv4
  address or IPv6 /64, client IP after trusted proxies) → 429 `rate_limited`; counted inside
  the write transaction (exact under a burst). Rows older than 1 h are deleted on create.
- `POST /api/v1/devices/poll` (anonymous) `DevicePollRequest{poll_secret}` → 200
  `DevicePoll{status, token?, url?}`; unknown secret 404. One poll per request per
  `PollInterval` (2 s, `last_poll_at`), else 429 (a refused poll changes nothing). Status
  `pending|approved|denied|expired` (pending past `expires_at` reads expired). The first poll
  after approval returns `token` (a `pl_agent_…` secret) and `url` (public URL) and clears the
  sealed copy; later polls answer `{status:"approved"}` only. An approved token is delivered
  even when polled after `expires_at` (until the row is cleaned up, 1 h).
- `GET /api/v1/devices/{code}` (user; anonymous 401, agent token 403) → `DeviceRequest{code,
  machine, ip, status, created_at, expires_at}`; unknown 404.
- `POST /api/v1/devices/{code}/approve` and `/deny` (user, CSRF rules of cookie requests) → 200
  `DeviceRequest`. Any logged-in user may decide (the code is the secret); approve creates an
  agent token named `devices.TokenName(machine)` = machine cut to 52 characters + `devices.NameSuffix`
  (" (via setup)"), owned by the approver, in the same transaction (`users.CreateTokenTx`). Expired →
  410 `gone`; already approved/denied → 409 `conflict`; unknown → 404.
- Audit: `device.create` (actor empty, IP, `{machine}`), `device.approve`, `device.deny`
  (actor = user), `device.deliver` (actor = approver); target kind `device`, target id = request
  id. `token.create` is recorded for the new token as usual. Codes, poll secrets and tokens
  are never logged.
- CLI (T6): print `approve_url`, poll every 2 s (≥ `PollInterval`, or 429), stop on
  `denied` ("approval denied") or `expired` ("approval expired"), save `{url, token}` on
  approval.

## install.sh (ONB-01..03)
- `scripts/install.sh` (POSIX sh) is embedded by package `scripts` (`scripts.Install(url)`)
  and served at `GET /install.sh` (anonymous, `text/plain; charset=utf-8`, `no-cache`) with
  `DEFAULT_URL='<public URL>'` (single-quoted for the shell).
- Asset names follow GoReleaser's defaults: `partyline_<version without v>_<os>_<arch>.tar.gz`
  with the `partyline` binary at the archive root, and `checksums.txt` (`<sha256>  <asset>`).
  OS `linux|darwin` from `uname -s`, arch `amd64` (x86_64) or `arm64` (aarch64) from
  `uname -m`; anything else fails with "unsupported".
- Version: `PARTYLINE_VERSION` (with or without `v`), else `tag_name` of the GitHub releases
  API. Test overrides: `PARTYLINE_RELEASES_API` (latest-release JSON URL) and
  `PARTYLINE_RELEASE_BASE` (`<base>/<tag>/<asset>`).
- Verifies the checksum before touching the install dir (`PARTYLINE_INSTALL_DIR`, default
  `~/.local/bin`); mismatch or unlisted asset → exit 1, nothing written. Installs through a
  temp file + `mv -f` (0755), prints a PATH hint (`export PATH="<dir>:$PATH"`) when the dir is
  not on PATH.
- Served copy: when no config file exists it writes `{"url":"<instance>","token":""}` (umask
  077 → file 0600, dirs 0700) and prints `Next: partyline setup --browser`; an existing config
  is never touched. `PARTYLINE_URL` overrides the default. The raw script prints `Next:
  partyline setup --browser --url https://<your instance>`.

## Tests and gates
- Server tests: `internal/api/agent_names_test.go` (unique mode), `devices_handlers_test.go`
  (fake clock `deviceT0`, `PARTYLINE_TRUSTED_PROXIES=127.0.0.1` + `X-Forwarded-For` to pick the
  client address), `install_handler_test.go`. Channel: `internal/channel/name_test.go`
  (`headerGateway` records dial headers per connection). CLI: `channel_config_test.go`,
  `channel_names_test.go` (`initialize`, `writeConfig`, `onlineNames` helpers). Installer:
  `scripts/install_test.go` runs `sh` against a fake GitHub (`newRelease`) with a clean PATH
  and a fake `uname` for other platforms.
- Tests that create git repositories set `GIT_CEILING_DIRECTORIES` to the temp parent.
- `TestLinkPausesAfterReplaced` (agent-channel, 3 s pause, 1 s no-fight window) failed once
  under load during the T4 gate and passed on the rerun; it is timing-sensitive and not
  affected by this batch.

# Contract Notes (from harness-onboarding Phase 2: T6-T9)

Binding for Phase 3 (listen, adapters, hooks: T10-T14), Phase 4 (web, UAT: T15-T17) and the
Verifier.

## partyline setup (ONB-04, DEV-01..03)
- `setup [--url U] [--token T | --browser] [--harness a,b|none] [--yes] [--uninstall]`
  (`internal/cli/setup.go`, `setup_harness.go`). Plain line prompts on stdout, answers from
  stdin; `--yes` asks nothing.
- URL default: `--url` > `PARTYLINE_URL` > the config file's `url` (the served installer's
  `{"url": …, "token": ""}` makes the instance URL the default answer). A config file that
  cannot be read (not private, invalid) is ignored for defaults with a note on stderr.
  `normalizeURL` accepts http(s) only and drops a trailing `/`. No URL at all → exit 2
  "no instance URL".
- Token: `--token`, else the saved token when the saved URL equals the chosen URL ("Using the
  saved agent token"), else the prompt; an empty answer (or `--yes` with nothing) starts the
  browser approval. `PARTYLINE_TOKEN` is not read by setup.
- Checks: `GET /healthz` 200 ("cannot reach <url>" → exit 1), then the token with
  `GET /api/v1/agent/rooms` (`Authorization: Bearer`, `X-Partyline-Agent: partyline-setup`):
  200 or 403 "agent not registered…" = valid agent token; 401 → "the server rejected the
  token (mistyped, revoked or expired); nothing was saved"; other 403 → "this is not an
  agent token…". Nothing is written on any failure; success saves with `cliconfig.Save`.
- Browser flow: `POST /devices {machine: hostname}` (control characters dropped, cut to 64
  runes, empty → `partyline`), prints the `approve_url` and code, then waits
  `devicePollInterval` (2 s) before each poll; 429 → keep waiting; `denied` → "approval
  denied", `expired` or 404 → "approval expired" (exit 1); `approved` with a token → saves
  the poll's `url` (normalized) and the token after the token check. Tests replace the wait
  with `stdio.sleep` and run the server on a `testkit.Clock`.
- After saving, the harness step runs (below); a failed registration exits 1 but the
  credentials stay saved.
- `stdio` gained `hostname`, `sleep`, `executable`, `runHarness`, `lookPath`, `exec` (nil =
  real). CLI tests must set `lookPath`/`runHarness` (see `runSetup`) so no real harness CLI
  ever runs; `harness.Env.run` additionally sets `HOME=<Env.Home>` for real CLI calls.

## Harness registrars (HAR-01, HAR-02, HAR-04) — `internal/harness`
- `harness.All(env)`: claude, codex, gemini, opencode, pi, omp (`Registrar`: `ID`, `Title`,
  `Detected`, `Plan`, `Apply`, `RemovePlan`, `Remove`, `Entries`). `Env{Home, Getenv,
  Binary (os.Executable, absolute), Version, Run, LookPath}`. Setup picks `--harness` ids
  (unknown → exit 2 before anything else) or every `Detected()` one; for each it prints the
  `Plan()` lines and asks "Register Partyline in <Title>? [Y/n]" (Enter = yes, "n" = no, end
  of input = no).
- Verified CLI syntax (2026-10-20, temp HOME; versions claude 2.1.303, codex-cli 0.159.3,
  gemini 0.62.0, opencode 1.18.32, pi 1.0.4, omp 18.6.1):
  - Claude Code: `claude mcp add --scope user partyline -- <abs> channel` writes
    `~/.claude.json` `mcpServers.partyline = {"type":"stdio","command":<abs>,"args":["channel"],"env":{}}`;
    it exits 1 "already exists" for an existing name, so update = `claude mcp remove --scope
    user partyline` then add (remove of a missing name exits 1, so it runs only when the file
    has the entry). `$CLAUDE_CONFIG_DIR/.claude.json` is read when set. `claude mcp get`
    health-checks the server, so Entries reads the file instead.
  - Codex: `codex mcp add partyline -- <abs> channel --tools-only` upserts
    `[mcp_servers.partyline]` in `~/.codex/config.toml` (it refuses an invalid config.toml
    with the TOML error, which setup reports); `codex mcp remove partyline` (exit 0 even when
    missing); `codex mcp get partyline --json` → `transport.command` (exit 1 "No MCP server
    named …" when missing; Codex prints WARNING lines before the JSON). Stop hook in
    `$CODEX_HOME/hooks.json` (default `~/.codex/hooks.json`), format confirmed from a real
    hooks.json and the binary: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"<abs> hook codex-stop","timeout":30}]}]}}`
    (timeout in seconds; Codex's hooks feature is stable/on). Its output contract is
    Claude-compatible: `{"decision":"block","reason":…}` and block requires a non-empty
    reason (string in the binary).
  - Gemini: `gemini mcp add -s user partyline <abs> channel --tools-only` upserts
    `~/.gemini/settings.json` `mcpServers.partyline = {"command":<abs>,"args":["channel","--tools-only"]}`
    (the trailing `--tools-only` reaches args without `--`); `gemini mcp remove -s user
    partyline` exits 0 even when missing and leaves `"mcpServers": {}` behind. AfterAgent hook
    (docs bundled with 0.62.0, `docs/hooks/`): `hooks.AfterAgent += {"matcher":"*","hooks":[{"name":"partyline","type":"command","command":"<abs> hook gemini-after-agent","timeout":30000}]}`
    (timeout in ms; hooks on by default, `hooksConfig.enabled`). Output: `{"decision":"deny","reason":…}`
    makes Gemini retry with `reason` as the new prompt; stdout must be only that JSON (exit 0).
  - pi: `~/.pi/agent/mcp.json` `{"mcpServers":{"partyline":{"command":<abs>,"args":["channel","--tools-only"]}}}`
    — the same format `pi mcp add partyline -- <cmd> args…` writes; setup edits the file.
  - omp: no `omp mcp` subcommand; the binary reads `~/.omp/agent/mcp.json`
    `{"mcpServers":{name:{command,args}}}` (type defaults to stdio). Setup writes
    `args: ["channel"]` (full channel; omp forwards the notifications to the extension).
  - OpenCode: `$XDG_CONFIG_HOME/opencode` (default `~/.config/opencode`, confirmed with
    `opencode debug paths`) `opencode.json` `mcp.partyline = {"type":"local","command":[<abs>,"channel","--tools-only"],"enabled":true}`;
    plugin `plugins/partyline.ts`. A `opencode.jsonc` is not handled (JSON with comments
    fails as a parse error).
- Hook commands are `<shell-quoted abs> hook <name>` (`codex-stop`, `gemini-after-agent`,
  constants `harness.CodexStopHook`, `harness.GeminiAfterAgentHook`); a group is "ours" when
  one of its commands ends in ` hook <name>` and runs a binary named `partyline*`. Apply
  replaces our group in place or appends it; Remove deletes only our groups.
- JSON edits (`jsonedit.go`): span-based, every other byte kept; a missing member is added
  after the last member with that member's indentation (compact files stay compact),
  missing parents are created; removal is the exact inverse and prunes objects/arrays left
  empty; a file left `{}` is deleted. Invalid JSON (or a non-object top level) →
  `*harness.ParseError` naming the file, nothing written and no CLI run. Before changing an
  existing file setup copies it to `<file>.partyline.bak`; uninstall deletes that copy.
- Extension files: `integrations/{omp,pi,opencode}/partyline.ts` embedded by package
  `integrations` (`integrations.Files`), written as `// partyline <version>: written by
  \`partyline setup\`; \`partyline setup --uninstall\` removes it.\n` + source. The current
  sources are placeholders — Phase 3 (T11-T13) replaces them (and adds `make
  integrations-test`). An existing file without that marker is never overwritten (Apply
  error) nor deleted.
- Detection: CLI on PATH (claude, codex, gemini, opencode, pi, omp) or, for the file-based
  ones, the config directory exists.
- `setup --uninstall [--harness …] [--yes]`: per registrar `RemovePlan` + confirm + `Remove`;
  without `--harness` it also offers to delete the config file and reminds that the token
  stays valid until revoked. "Nothing to remove" when nothing is registered.
- HAR-02 for CLI-managed files (`~/.claude.json`, `config.toml`, the `mcpServers` part of
  Gemini's settings) is the harness CLI's own rewrite; byte-for-byte is guaranteed only for
  the files setup edits itself.

## doctor (ONB-06) and the claude launcher (HAR-03)
- `partyline doctor` prints `ok   <name>: <detail>` or `FAIL <name>: <detail>` +
  `     fix: <hint>`, then "All checks passed." (exit 0) or "<n> check(s) failed." (exit 1).
  Checks: `binary` (partyline on PATH; fix `export PATH="<dir>:$PATH"`), `config` (file
  present, private, URL and token; fix `run partyline setup` / `chmod 600|700 …`), `server`
  (`/healthz`), `token` (same check as setup), one line per registered entry of each
  detected harness (`<Title>: <What> runs <bin>`, binary must be an existing absolute
  executable file; fix `run partyline setup --harness <id>`; none registered → `harnesses`
  FAIL), `versions` (local `buildVersion()` vs server; two non-`dev` versions that differ, or
  a server that reports none, FAIL).
- `/healthz` now answers `{"status":"ok","version":"<server version>"}` (`api.Deps.Version`,
  set by the app from `app.Options.Version`; `dev` by default). testkit: `WithVersion(v)`.
- `partyline claude [args…]` looks up `claude` on PATH and execs (`syscall.Exec`; on Windows
  a child process with the exit code passed through) `claude
  --dangerously-load-development-channels server:partyline [args…]`; no claude → exit 1.

## Notes for Phase 3/4
- `--tools-only` is not implemented yet (T10); every registration except Claude Code and omp
  already passes it, so those harnesses fail to start the MCP server until T10 lands. T10
  must not use derived-name unique mode on its own (see Phase 1 notes).
- `codex queue --thread <id> --message <text>` (codex-cli 0.159.3) queues a message for an
  existing session — a possible push path for Codex worth checking in T14/T17.
- Re-run `make check` flake: `TestLinkPausesAfterReplaced` failed once more under load in the
  T6 gate (passed alone and in the following full gates).
