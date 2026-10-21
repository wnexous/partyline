# Harness Onboarding Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/harness-onboarding/design.md`
**Status**: Approved

---

## Test Coverage Matrix

> Guidelines: `AGENTS.md`, all `contract-notes.md`, `.specs/LESSONS.md` (confirmed lessons apply).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| CLI packages (config, name, registrars) | unit/integration with fake HOME and fake harness CLIs | Every AC + error paths; other config keys preserved byte-for-byte | `internal/**/*_test.go` | `make test` |
| Server endpoints (devices, pending, unique names, install.sh) | e2e (testkit) | Happy + edge + error; rate limits; auth matrix | `internal/api/*_test.go` | `make test` |
| install.sh | integration (sh against httptest release fixture) | ONB-01/02 incl. checksum failure | `scripts/install_test.go` | `make test` |
| TS adapters | unit (`bun test`) with fake host APIs | Mapping, framing, error logging | `integrations/**/*.test.ts` | `make integrations-test` |
| Web | unit (Vitest) | WIZ ACs | `web/src/**/*.test.tsx` | `cd web && bun run test` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Unit-only tasks | `go test -count=1 ./internal/...` / `make integrations-test` / `cd web && bun run test` |
| Full | Integration/e2e tasks | `make test` |
| Build | Before every commit (lesson L-029) | `make check` |

---

## Execution Plan

### Phase 1: Server and core

```
T1 → T2 → T3 → T4 → T5
```

### Phase 2: Setup, doctor, launcher

```
T6 → T7 → T8 → T9
```

### Phase 3: Push and turn adapters

```
T10 → T11 → T12 → T13 → T14
```

### Phase 4: Web and real-harness UAT

```
T15 → T16 → T17
```

### Phase 5: UAT follow-ups

```
T18 → T19 → T20 → T21 → T22
```

### Phase 6: Verifier fix iteration 1

```
T23 → T24 → T25 → T26 → T27 → T28 → T29 → T30 → T31
```

---

## Task Breakdown

### Phase 1: Server and core

#### T1: Version from build info

**What**: `partyline version` falls back to `debug.ReadBuildInfo` main module version when ldflags are absent.
**Where**: `internal/cli/version.go`
**Depends on**: None
**Reuses**: cli
**Requirement**: ONB-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] ldflags value wins; module version used otherwise; `(devel)` → `dev`
**Tests**: unit
**Gate**: build
**Commit**: `fix(cli): report the module version when installed with go install`

#### T2: Machine config file

**What**: `internal/cliconfig` Load/Save with 0600/0700 checks, XDG path, precedence flags > env > file; `channel` reads it.
**Where**: `internal/cliconfig/config.go`
**Depends on**: T1
**Reuses**: none
**Requirement**: ONB-04, ONB-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] 0644 file refused with chmod hint; env overrides file; atomic write leaves 0600
**Tests**: unit
**Gate**: build
**Commit**: `feat(cli): store instance credentials in a private config file`

#### T3: Derived names and unique mode

**What**: channel derives the name (git root → cwd) and sends `X-Partyline-Agent-Mode: unique`; gateway assigns `<name>-N` when the derived name is online; welcome carries the final name.
**Where**: `internal/agents/gateway.go`
**Depends on**: T2
**Reuses**: registry, slug rule
**Requirement**: NAME-01, NAME-02, NAME-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Two derived sessions → `proj`, `proj-2`; explicit name still replaces; cap still enforced; git root beats subdir
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): give derived agent names a free suffix per session`

#### T4: Device approval endpoints

**What**: migration 0009, device create/poll/get/approve/deny, rate limits, audit.
**Where**: `internal/api/devices_handlers.go`
**Depends on**: T3
**Reuses**: tokens service, rate limiter, secrets box
**Requirement**: DEV-01, DEV-02, DEV-03, DEV-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Approve → poll returns token once, then never again; deny/expiry statuses; 11th create/hour → 429; poll faster than 2 s → 429; auth matrix
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): approve new machines from the web app`

#### T5: install.sh

**What**: `scripts/install.sh`, embedded and served at `/install.sh` with the instance URL default; release base overridable for tests.
**Where**: `scripts/install.sh`
**Depends on**: T4
**Reuses**: release asset naming from `.goreleaser.yaml`
**Requirement**: ONB-01, ONB-02, ONB-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Installs from an httptest fixture into a temp dir; bad checksum → non-zero and no binary; PATH hint shown when needed; served script contains the instance URL
**Tests**: integration
**Gate**: build
**Commit**: `feat(cli): add a one-line installer`

### Phase 2: Setup, doctor, launcher

#### T6: partyline setup (credentials)

**What**: interactive/non-interactive setup for URL + token or `--browser` device flow; verifies against the server; writes config.
**Where**: `internal/cli/setup.go`
**Depends on**: T5
**Reuses**: cliconfig, device endpoints
**Requirement**: ONB-04, DEV-01, DEV-02, DEV-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Token path and browser path against testkit; invalid token → error, no file written
**Tests**: integration
**Gate**: build
**Commit**: `feat(cli): add partyline setup`

#### T7: CLI-based harness registrars

**What**: Claude Code, Codex CLI, Gemini CLI registrars (MCP add with absolute path; Codex Stop hook and Gemini AfterAgent hook entries), plan/apply/remove, idempotent.
**Where**: `internal/harness/clis.go`
**Depends on**: T6
**Reuses**: injectable command runner
**Requirement**: HAR-01, HAR-02, HAR-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake CLIs record exact args; hook files edited preserving other entries; uninstall removes only ours
**Tests**: integration
**Gate**: build
**Commit**: `feat(harness): register partyline in claude code, codex and gemini`

#### T8: File-based harness registrars

**What**: OpenCode, pi, omp registrars: JSON edits preserving other keys, `.bak` backup, embedded extension/plugin files with version header.
**Where**: `internal/harness/files.go`
**Depends on**: T7
**Reuses**: go:embed of `integrations/`
**Requirement**: HAR-01, HAR-02, HAR-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Other keys byte-for-byte; invalid JSON untouched with error; uninstall restores
**Tests**: integration
**Gate**: build
**Commit**: `feat(harness): register partyline in opencode, pi and omp`

#### T9: doctor and claude launcher

**What**: `partyline doctor` checks (ONB-06) and `partyline claude` exec.
**Where**: `internal/cli/doctor.go`
**Depends on**: T8
**Reuses**: cliconfig, registrars
**Requirement**: ONB-06, HAR-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Each check fails with its hint in a crafted bad environment; launcher builds the exact argv (exec seam)
**Tests**: integration
**Gate**: build
**Commit**: `feat(cli): add doctor and the claude launcher`

### Phase 3: Push and turn adapters

#### T10: listen and tools-only

**What**: `partyline listen` (JSON lines + acks) and `channel --tools-only`.
**Where**: `internal/cli/listen.go`
**Depends on**: T9
**Reuses**: channel link
**Requirement**: PUSH-01, PUSH-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] listen prints the exact line format and acks after writing; tools-only opens no push socket
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): add listen and a tools-only channel`

#### T11: omp extension

**What**: `integrations/omp/partyline.ts` mapping channel notifications to `sendUserMessage` with framing.
**Where**: `integrations/omp/partyline.ts`
**Depends on**: T10
**Reuses**: omp extension API (`mcp_notification`)
**Requirement**: PUSH-03, PUSH-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake host: notification → one user message with framing; other servers/methods ignored
**Tests**: unit
**Gate**: build
**Commit**: `feat(integrations): add the oh-my-pi extension`

#### T12: pi extension

**What**: `integrations/pi/partyline.ts` spawning `partyline listen`, `sendMessage(…, {triggerTurn: true, deliverAs: "followUp"})`.
**Where**: `integrations/pi/partyline.ts`
**Depends on**: T11
**Reuses**: listen line format
**Requirement**: PUSH-04, PUSH-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake host + fake listen process: each line → one message; process exit → restart with backoff; stderr logged
**Tests**: unit
**Gate**: build
**Commit**: `feat(integrations): add the pi extension`

#### T13: OpenCode plugin

**What**: `integrations/opencode/partyline.ts` spawning listen, prompting the active session via the SDK client.
**Where**: `integrations/opencode/partyline.ts`
**Depends on**: T12
**Reuses**: listen line format
**Requirement**: PUSH-05, PUSH-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake client: lines → `session.prompt` on the active session id; no session yet → queued until one exists
**Tests**: unit
**Gate**: build
**Commit**: `feat(integrations): add the opencode plugin`

#### T14: wait_for_messages and turn-end hooks

**What**: `GET /agent/pending`, `wait_for_messages` tool, `partyline hook codex-stop|gemini-after-agent`.
**Where**: `internal/cli/hook.go`
**Depends on**: T13
**Reuses**: agent REST, cursors
**Requirement**: TURN-01, TURN-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Pending → exact block/deny JSON per harness; none → allow; wait times out at the given seconds (fake clock or short real bound)
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): deliver pending messages at turn end for codex and gemini`

### Phase 4: Web and real-harness UAT

#### T15: Wizard per harness

**What**: connect wizard with install + setup commands, harness tabs, advanced JSON without inline token.
**Where**: `web/src/components/connect-agent-wizard.tsx`
**Depends on**: T14
**Reuses**: copy-field
**Requirement**: WIZ-01
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Commands contain the instance origin; each harness shows its start command; no token in the advanced JSON
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): guide each harness with install and setup commands`

#### T16: Device approval page

**What**: `/devices/$code` page with machine, IP, age, Approve/Deny.
**Where**: `web/src/routes/_app/devices.$code.tsx`
**Depends on**: T15
**Reuses**: queries
**Requirement**: WIZ-02, DEV-02
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Approve/deny call the API; expired shows expired state; login redirect returns to the page
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): approve machines from the browser`

#### T17: Real-harness UAT

**What**: on this devbox, against a local `partyline serve`: install via the built installer fixture, `setup` with a token, then a real Claude Code, omp, pi and OpenCode session each receives a room message and replies with a tool; record in `uat.md` (no secrets).
**Where**: `.specs/features/harness-onboarding/uat.md`
**Depends on**: T16
**Reuses**: Orca terminals (`orca terminal create/send/read/close`)
**Requirement**: PUSH-03, PUSH-04, PUSH-05, HAR-01
**Tools**: MCP: NONE; Skill: `orca-cli`
**Done when**:
- [x] Each harness result recorded pass/fail with what was observed; failures become fix tasks
**Tests**: e2e
**Gate**: build
**Commit**: `docs(specs): record real harness smoke tests`

### Phase 5: UAT follow-ups

From `uat.md` (T17) follow-ups 1 and 2, the omp instructions mismatch and a flaky gate test.

#### T18: Trust-aware framing line

**What**: the line after `<partyline …>` depends on `trusted` (PUSH-06 amended; AD-006 unchanged): owner wording for `trusted="true"`, third-party data wording otherwise. The channel and tools-only MCP instructions explain both cases.
**Where**: `internal/channel/frame.go`, `internal/channel/instructions.go`, `integrations/{omp,pi,opencode}/partyline.ts`, `integrations/testdata/framing.json`
**Depends on**: T17
**Reuses**: shared framing cases (Go/TS parity)
**Requirement**: PUSH-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `trusted="true"` frames end with the owner line, every other value with the third-party line, in Go and in all three adapters (shared `framing.json` cases, literal spec wording)
- [x] Hooks and `wait_for_messages` use the trust-aware line
- [x] Channel and tools-only instructions explain both cases
**Tests**: unit
**Gate**: build
**Commit**: `fix(channel): tell agents when a framed message comes from their owner`

#### T19: omp holds messages it could not deliver

**What**: the omp extension keeps undelivered messages in an in-memory queue (no model selected, send threw or rejected), retries with backoff (1 s doubling to 30 s) and on `agent_end`, in order, and shows one notice per outage.
**Where**: `integrations/omp/partyline.ts`
**Depends on**: T18
**Reuses**: `FakeTime` from `testdata/fakes.ts`
**Requirement**: PUSH-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Failing fake host: a failed send is kept and delivered by a later retry; order kept; one notice per outage; no model → nothing sent until a model is there; `agent_end` retries at once
**Tests**: unit
**Gate**: build
**Commit**: `fix(integrations): hold omp messages until the session can take them`

#### T20: `channel --harness omp`

**What**: `partyline channel --harness omp` serves the full channel with MCP instructions that describe the `<partyline …>` framing the omp extension produces; setup and the web's manual entry register `["channel", "--harness", "omp"]`.
**Where**: `internal/cli/channel.go`, `internal/channel/instructions.go`, `internal/channel/run.go`, `internal/harness/files.go`, `web/src/lib/agent-connect.ts`
**Depends on**: T19
**Reuses**: instruction paragraphs
**Requirement**: PUSH-03, PUSH-06, HAR-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `--harness omp` instructions describe `<partyline …>` framing, push delivery and the connect notice, no `<channel` and no `wait_for_messages`; unknown harness or `--tools-only` with it → exit 2
- [x] omp registrar writes `args: ["channel", "--harness", "omp"]`; the wizard's manual omp entry matches
**Tests**: unit
**Gate**: build
**Commit**: `feat(cli): describe the partyline framing to omp sessions`

#### T21: Deterministic paused-room notice test

**What**: `TestAgentWSInstructionsNoticeInPausedRoom` pauses the room before posting, so the live dispatcher can never see the room unpaused and push the message.
**Where**: `internal/api/room_info_test.go`
**Depends on**: T20
**Reuses**: `TestDispatchSkipsPausedRoom` ordering
**Requirement**: INS-03 (routing-context), DLV-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Passes with `-count=20`, and with an injected dispatcher delay that fails the old ordering
**Tests**: e2e
**Gate**: build
**Commit**: `test(api): pause the room before posting in the paused notice test`

#### T22: Record the follow-up fixes

**What**: contract notes for T18-T21, `uat.md` notes on the fixes, traceability.
**Where**: `.specs/features/harness-onboarding/contract-notes.md`, `uat.md`, `spec.md`, `design.md`
**Depends on**: T21
**Reuses**: none
**Requirement**: PUSH-03, PUSH-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Contract notes, uat.md and traceability describe the shipped behavior
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record the harness uat follow-up fixes`

### Phase 6: Verifier fix iteration 1

From `validation.md` (iteration 1): ranked gaps 1-5, the two spec-precision gaps and the security
weaknesses the maintainer chose to fix. Every task: a discriminating test first (checked against a
mutant in a scratch worktree), then the fix; `make check` before each commit, `make e2e` too when
the web changes.

#### T23: UI-14 drives the harness wizard

**What**: rewrite the e2e UI-14 for the wizard of T15: install command with the instance origin, `partyline setup --browser`, harness tabs with their start commands, and Advanced entries with no token.
**Where**: `web/e2e/smoke.spec.ts`
**Depends on**: T22
**Reuses**: `CodeBlock` figures (`role=figure`, caption = label)
**Requirement**: WIZ-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] UI-14 asserts the exact install and setup commands, the six tabs and start commands, and that no Advanced entry contains `pl_agent`, `PARTYLINE_` or `token`; it creates no token
- [x] `make e2e` passes 8/8
**Tests**: e2e
**Gate**: build + e2e
**Commit**: `test(web): drive the harness wizard in the UI-14 smoke test`

#### T24: Typed-code device approval

**What**: the CLI prints `<instance>/devices` and the code; the page asks the user to type the code, then shows machine, address and age with Approve/Deny; `/devices/<code>` redirects to `/devices`. The server takes the code in the body (`POST /devices/lookup|approve|deny`) and refuses a user's 6th unknown code within a minute (429). The poll that delivers the token names the approver; the CLI prints `Approved by @<username> on <instance>` and asks before saving (declined → nothing saved, exit 1; `--yes` accepts).
**Where**: `internal/devices/devices.go`, `internal/api/devices_handlers.go`, `internal/api/dto/devices.go`, `internal/cli/setup.go`, `web/src/routes/_app/devices*.tsx`, `web/src/lib/api/queries.ts`, i18n
**Depends on**: T23
**Reuses**: device service, `useSecondsLeft`
**Requirement**: DEV-01, DEV-02, DEV-05, DEV-06, WIZ-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `approve_url` is `<instance>/devices`; the CLI output has no URL with the code
- [x] lookup/approve/deny read the code from the body; old `{code}` routes are gone
- [x] 5 unknown codes per user and minute, the 6th → 429 even with a valid code; another user unaffected; a minute later accepted
- [x] delivering poll has `approved_by`; CLI prints `Approved by @<user> on <instance>`, declined confirmation saves nothing and exits 1
- [x] page: code field first, details after lookup, Approve sends the typed code; `/devices/<code>` lands on `/devices` with an empty field
**Tests**: e2e (API), unit (CLI, web), e2e (web)
**Gate**: build + e2e
**Commit**: `feat(devices): approve machines by typing the code from the terminal`

#### T25: Saved tokens stay with their URL

**What**: `cliconfig.Resolve` drops the saved token when the URL comes from a flag or env and differs from the saved URL; setup saves the URL it started with (the poll's URL is ignored); `--token` warns that it shows in `ps`, `--token-stdin` reads it from stdin; `setup --uninstall` revokes the saved token on the server (`DELETE /api/v1/agent/token`, best effort) and reports the result.
**Where**: `internal/cliconfig/config.go`, `internal/cli/setup.go`, `internal/cli/setup_harness.go`, `internal/api/agent_handlers.go`
**Depends on**: T24
**Reuses**: `users.RevokeToken`
**Requirement**: ONB-04, ONB-05, ONB-08, ONB-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] env/flag URL ≠ saved URL → saved token ignored (missing token error); same URL (normalized) → saved token used
- [x] browser setup saves the start URL even when the poll reports another
- [x] `--token` prints the `ps` warning on stderr; `--token-stdin` works and conflicts with `--token`/`--browser`
- [x] uninstall revokes the token (later calls 401) and prints the result; a failed revoke is reported, not fatal
**Tests**: unit, e2e (API)
**Gate**: build
**Commit**: `fix(cli): keep the saved token with the instance it was issued for`

#### T26: Installer runs whole or not at all

**What**: `install.sh` body moves into `main`, called on the last line; `http://` URLs are refused except for localhost, 127.0.0.1 and ::1.
**Where**: `scripts/install.sh`, `scripts/install_test.go`
**Depends on**: T25
**Reuses**: install test harness
**Requirement**: ONB-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] a script cut before its last line does nothing (no download, no file)
- [x] `PARTYLINE_URL=http://example.test` exits 1 with a message; `http://127.0.0.1:…`, `http://localhost:…`, `http://[::1]:…` accepted
**Tests**: integration
**Gate**: build
**Commit**: `fix(scripts): run the installer only when it downloaded completely`

#### T27: Safer harness config edits

**What**: keep the first `.partyline.bak`; write through symlinks (atomic write next to the target, link kept); write the extension/plugin before the config entry and roll back on failure.
**Where**: `internal/harness/harness.go`, `internal/harness/files.go`, `internal/harness/clis.go`
**Depends on**: T26
**Reuses**: `writeFileAtomic`
**Requirement**: HAR-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] second Apply keeps the original backup bytes
- [ ] symlinked config: link stays a link, target gets the entry
- [ ] config write failure leaves no extension behind; extension write failure leaves the config untouched
**Tests**: unit
**Gate**: build
**Commit**: `fix(harness): keep backups, symlinks and a consistent state when editing configs`

#### T28: omp delivers one message at a time

**What**: the omp queue awaits each send; a rejection keeps the message at the head and stops the flush until the next retry.
**Where**: `integrations/omp/partyline.ts`
**Depends on**: T27
**Reuses**: `FakeTime`
**Requirement**: PUSH-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] two async rejections: messages delivered A then B; nothing sent while a send is in flight
**Tests**: unit
**Gate**: build
**Commit**: `fix(integrations): keep omp messages in order when sends reject later`

#### T29: Missing evidence

**What**: tests for `listen` refusing a non-0600 config, CSRF on the device approve/deny routes, and a real invalid `config.toml` for the Codex registrar.
**Where**: `internal/cli/listen_test.go`, `internal/api/devices_handlers_test.go`, `internal/harness/clis_test.go`
**Depends on**: T28
**Reuses**: existing fixtures
**Requirement**: ONB-05, DEV-05, HAR-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] each test fails against a mutant that removes the check
**Tests**: unit, e2e
**Gate**: build
**Commit**: `test: cover config permissions in listen, device csrf and invalid codex toml`

#### T30: Spec precision for HAR-02 and PUSH-05

**What**: HAR-02 separates files setup edits (byte-for-byte) from CLI-managed files (the harness CLI rewrites them; only the partyline entry may change); PUSH-05 names `promptAsync` with the `prompt` fallback.
**Where**: `.specs/features/harness-onboarding/spec.md`
**Depends on**: T29
**Reuses**: contract notes
**Requirement**: HAR-02, PUSH-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] spec wording matches the shipped behavior and the tests that cover it
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): reword HAR-02 and PUSH-05 to match the harness behavior`

#### T31: Record fix iteration 1

**What**: contract notes for T23-T29, design risk table, traceability.
**Where**: `.specs/features/harness-onboarding/contract-notes.md`, `design.md`, `spec.md`
**Depends on**: T30
**Reuses**: none
**Requirement**: all touched
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] contract notes and traceability describe the shipped behavior
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record the harness-onboarding fix iteration 1`
