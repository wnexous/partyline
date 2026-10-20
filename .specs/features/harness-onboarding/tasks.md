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
- [ ] Each check fails with its hint in a crafted bad environment; launcher builds the exact argv (exec seam)
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
- [ ] listen prints the exact line format and acks after writing; tools-only opens no push socket
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
- [ ] Fake host: notification → one user message with framing; other servers/methods ignored
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
- [ ] Fake host + fake listen process: each line → one message; process exit → restart with backoff; stderr logged
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
- [ ] Fake client: lines → `session.prompt` on the active session id; no session yet → queued until one exists
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
- [ ] Pending → exact block/deny JSON per harness; none → allow; wait times out at the given seconds (fake clock or short real bound)
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
- [ ] Commands contain the instance origin; each harness shows its start command; no token in the advanced JSON
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
- [ ] Approve/deny call the API; expired shows expired state; login redirect returns to the page
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
- [ ] Each harness result recorded pass/fail with what was observed; failures become fix tasks
**Tests**: e2e
**Gate**: build
**Commit**: `docs(specs): record real harness smoke tests`
