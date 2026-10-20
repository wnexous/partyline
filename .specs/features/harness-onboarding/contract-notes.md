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
