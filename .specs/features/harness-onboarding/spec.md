# Harness Onboarding Specification

## Problem Statement

Connecting a session is error-prone. The web wizard tells users to `go install` (the binary lands in `~/go/bin`, often off `PATH`, giving a bare `ENOENT`), to paste a per-project `.mcp.json` that embeds the agent token (it gets committed to git) and a fixed agent name (two sessions with the same name keep replacing each other). Only Claude Code is supported, while users also run OpenCode, pi, oh-my-pi (omp), Codex CLI and Gemini CLI.

## Goals

- [ ] From zero to a connected session on a new machine in two commands (install, setup), no file editing.
- [ ] Every session on a machine connects automatically with a distinct, readable name; no token in project files.
- [ ] Claude Code, omp, pi and OpenCode receive pushed messages in a running session; Codex CLI and Gemini CLI receive them at turn end and through a waiting tool.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Removing Claude Code's per-start development-channels confirmation | Only an official-marketplace listing or an org allowlist can; not available to personal accounts |
| Waking an idle Codex/Gemini session | Their documented APIs cannot inject into an idle interactive session (research 2026-10-20) |
| Cursor CLI, Amp, Crush, Aider adapters | No confirmed push mechanism; generic MCP tools still work |
| Windows installer script | Release zips cover Windows; the shell installer targets Linux and macOS |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Credential storage | `~/.config/partyline/config.json` (mode 0600; dir 0700); env vars still override | No token in project files; one place per machine | y |
| Agent name | `PARTYLINE_AGENT` if set, else the git repository root's basename, else the working directory's basename, slugified | Stable per project, readable | y |
| Name collisions | When the name was derived (not set explicitly) and that agent is online in another session, the server assigns the next free `<name>-2`, `<name>-3`…; explicit names keep "newest wins" | Many sessions per project on one machine | y |
| Install location | `install.sh` puts the binary in `~/.local/bin` (or `$PARTYLINE_INSTALL_DIR`) and prints a PATH hint when needed | Works without Go | y |
| Setup login | Paste an agent token, or `--browser` device flow: the CLI shows a code, the user approves it in the web app, the CLI receives a new agent token | No copy-paste of secrets | y |
| Harness registration | `partyline setup` detects installed harnesses and registers user-scope config with the absolute binary path; asks before writing each one | One-time, explicit consent per harness | y |
| Push for pi/OpenCode | A small extension/plugin spawns `partyline listen` (JSON lines of incoming messages) and injects them; tools come from the MCP server started with `--tools-only` | Their MCP clients do not turn notifications into turns | y |
| Push for omp | Same channel MCP server; a small extension maps `notifications/claude/channel` (`mcp_notification` event) to `pi.sendUserMessage` | omp forwards MCP notifications to extensions | y |
| Codex/Gemini | `wait_for_messages` tool + turn-end hook (`Stop` / `AfterAgent`) that blocks with the pending messages as the continuation reason | Best documented mechanism | y |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Install and set up in two commands ⭐ MVP

**User Story**: As a user, I want `install` then `setup` so that my machine is ready without editing files.

**Why P1**: First real use failed on install and config.

**Acceptance Criteria**:

1. WHEN `install.sh` runs on Linux or macOS (amd64/arm64) THEN it SHALL download the latest release binary, verify its SHA-256 against `checksums.txt`, install it to `~/.local/bin` (or `$PARTYLINE_INSTALL_DIR`) and print a PATH hint IF that directory is not on `PATH`. (ONB-01)
2. IF the checksum does not match THEN `install.sh` SHALL exit non-zero and leave no binary. (ONB-02)
3. The server SHALL serve `install.sh` at `/install.sh` with the instance URL pre-filled as the default for setup. (ONB-03)
4. WHEN `partyline setup` runs THEN it SHALL ask for the instance URL and either an agent token or browser approval, verify them against the server, and write `~/.config/partyline/config.json` with mode 0600. (ONB-04)
5. WHEN `partyline channel` starts without `PARTYLINE_URL`/`PARTYLINE_TOKEN` THEN it SHALL read them from the config file; env vars and flags SHALL override the file. (ONB-05)
6. WHEN `partyline doctor` runs THEN it SHALL report pass/fail with a fix hint for: binary on PATH, config file present and private, server reachable, token valid, each registered harness entry pointing to an existing binary, and the local and server versions. (ONB-06)
7. `partyline version` SHALL print the module version from build info when built with `go install` (not `dev`). (ONB-07)

**Independent Test**: Fresh HOME: run install.sh against a local release fixture, run setup with a token, run doctor → all pass.

---

### P1: Browser approval for setup ⭐ MVP

**User Story**: As a user, I want to approve a machine in the web app so that I never copy tokens.

**Why P1**: Copying tokens is error-prone and risky.

**Acceptance Criteria**:

1. WHEN `partyline setup --browser` starts THEN the server SHALL create a device request with an 8-character code valid for 10 minutes and the CLI SHALL print the approval URL `<instance>/devices/<code>`. (DEV-01)
2. WHEN a logged-in user approves the request THEN the server SHALL create an agent token named after the machine hostname and deliver it once to the polling CLI. (DEV-02)
3. IF the request expires or is denied THEN the CLI SHALL exit non-zero with `approval expired` or `approval denied`. (DEV-03)
4. The server SHALL rate-limit device polling to 1 request per 2 seconds per request and creation to 10 per hour per IP. (DEV-04)

**Independent Test**: CLI starts, user approves in the web app, CLI saves the token.

---

### P1: Automatic, distinct agent names ⭐ MVP

**User Story**: As a user with many sessions on one machine, I want each session to get its own name automatically so that they do not replace each other.

**Why P1**: Fixed names made sessions fight.

**Acceptance Criteria**:

1. WHEN `PARTYLINE_AGENT` is unset THEN the channel SHALL derive the name from the git repository root's basename, else the working directory's basename, slugified. (NAME-01)
2. WHEN a derived name is already online in another session THEN the server SHALL register the session as the next free `<name>-N` (N ≥ 2) and the welcome frame SHALL carry the final name. (NAME-02)
3. WHEN the name is set explicitly THEN the server SHALL keep "newest wins" replacement. (NAME-03)

**Independent Test**: Two channels in the same directory register as `proj` and `proj-2`.

---

### P1: Harness registration and launcher ⭐ MVP

**User Story**: As a user, I want setup to register Partyline in every harness I use so that every new session connects.

**Why P1**: One-time setup per machine.

**Acceptance Criteria**:

1. WHEN setup detects a harness THEN it SHALL show the exact change and, after confirmation, register Partyline at user scope with the binary's absolute path: Claude Code (`claude mcp add --scope user`), OpenCode (global `opencode.json` + plugin), pi (`~/.pi/agent/mcp.json` + extension), omp (`~/.omp/agent/mcp.json` + extension), Codex CLI (`codex mcp add` + Stop hook), Gemini CLI (`gemini mcp add -s user` + AfterAgent hook). (HAR-01)
2. IF a Partyline entry already exists for a harness THEN setup SHALL update it in place and keep every other entry byte-for-byte. (HAR-02)
3. WHEN `partyline claude [args…]` runs THEN it SHALL exec `claude --dangerously-load-development-channels server:partyline [args…]`. (HAR-03)
4. `partyline setup --uninstall` SHALL remove exactly the entries and files setup added. (HAR-04)

**Independent Test**: Fake HOME with fake harness CLIs; setup writes/updates each config; uninstall restores the originals.

---

### P1: Push for omp, pi and OpenCode ⭐ MVP

**User Story**: As a pi, omp or OpenCode user, I want room messages to arrive in my running session like in Claude Code.

**Why P1**: Requested ("seria incrível").

**Acceptance Criteria**:

1. WHEN `partyline listen` runs THEN it SHALL hold the agent WebSocket, print one JSON line per incoming message (same fields as the channel meta plus `content`) and ack each message after the line is written. (PUSH-01)
2. WHEN `partyline channel --tools-only` runs THEN it SHALL expose the tools without opening the push socket, so it can share the agent name with `listen`. (PUSH-02)
3. The omp extension SHALL turn each `notifications/claude/channel` from the partyline MCP server into `pi.sendUserMessage` (deliver as follow-up while busy). (PUSH-03)
4. The pi extension SHALL spawn `partyline listen` and turn each line into `pi.sendMessage(..., {triggerTurn: true, deliverAs: "followUp"})`. (PUSH-04)
5. The OpenCode plugin SHALL spawn `partyline listen` and send each line to the active session with the SDK `client.session.prompt` (async). (PUSH-05)
6. Every injected message SHALL be wrapped with the same untrusted-data framing and `trusted` attribute as Claude Code channel events. (PUSH-06)

**Independent Test**: Unit tests for each adapter's mapping with a fake host API; `listen` e2e against testkit.

---

### P2: Codex CLI and Gemini CLI

**User Story**: As a Codex or Gemini user, I want pending room messages at the end of each turn and a tool to wait for messages.

**Why P2**: No push into idle sessions is possible.

**Acceptance Criteria**:

1. The MCP server SHALL expose `wait_for_messages(timeout_seconds ≤ 300)` returning pending messages or an empty list on timeout. (TURN-01)
2. WHEN `partyline hook codex-stop` or `partyline hook gemini-after-agent` runs with pending messages THEN it SHALL print the harness's "block" decision with the messages as the reason; with none, it SHALL allow the stop. (TURN-02)

**Independent Test**: Hook commands against testkit with and without pending messages.

---

### P2: Web wizard per harness

**User Story**: As a user, I want the connect wizard to show the two commands for my harness instead of JSON.

**Why P2**: Removes the copy-paste-JSON path.

**Acceptance Criteria**:

1. The wizard SHALL show `curl -fsSL <instance>/install.sh | sh` and `partyline setup --browser`, a harness selector (Claude Code, OpenCode, pi, omp, Codex, Gemini) with its start command, and keep the manual JSON (without the token inline) under "Advanced". (WIZ-01)
2. WHEN a device approval page `/devices/<code>` is opened THEN the app SHALL show the machine name and code with Approve/Deny. (WIZ-02)

**Independent Test**: Wizard renders commands; approval page approves a pending request.

---

## Edge Cases

- IF the config file has mode other than 0600 THEN `channel`, `listen` and `doctor` SHALL refuse to read it and print the chmod fix (ONB-05).
- WHEN the git root and the working directory differ THEN the git root's basename SHALL win (NAME-01).
- IF a harness config file is not valid JSON/TOML THEN setup SHALL not touch it and SHALL report the parse error (HAR-02).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| ONB-01 | P1: Install and setup | Tasks | Implementing |
| ONB-02 | P1: Install and setup | Tasks | Implementing |
| ONB-03 | P1: Install and setup | Tasks | Implementing |
| ONB-04 | P1: Install and setup | Tasks | Implementing |
| ONB-05 | P1: Install and setup | Tasks | Implementing |
| ONB-06 | P1: Install and setup | Tasks | Implementing |
| ONB-07 | P1: Install and setup | Tasks | Implementing |
| DEV-01 | P1: Browser approval | Tasks | Implementing |
| DEV-02 | P1: Browser approval | Tasks | Implementing |
| DEV-03 | P1: Browser approval | Tasks | Implementing |
| DEV-04 | P1: Browser approval | Tasks | Implementing |
| NAME-01 | P1: Agent names | Tasks | Implementing |
| NAME-02 | P1: Agent names | Tasks | Implementing |
| NAME-03 | P1: Agent names | Tasks | Implementing |
| HAR-01 | P1: Harness registration | Tasks | Implementing |
| HAR-02 | P1: Harness registration | Tasks | Implementing |
| HAR-03 | P1: Harness registration | Tasks | Implementing |
| HAR-04 | P1: Harness registration | Tasks | Implementing |
| PUSH-01 | P1: Push adapters | Tasks | Pending |
| PUSH-02 | P1: Push adapters | Tasks | Pending |
| PUSH-03 | P1: Push adapters | Tasks | Pending |
| PUSH-04 | P1: Push adapters | Tasks | Pending |
| PUSH-05 | P1: Push adapters | Tasks | Pending |
| PUSH-06 | P1: Push adapters | Tasks | Pending |
| TURN-01 | P2: Codex and Gemini | Tasks | Pending |
| TURN-02 | P2: Codex and Gemini | Tasks | Pending |
| WIZ-01 | P2: Web wizard | Tasks | Pending |
| WIZ-02 | P2: Web wizard | Tasks | Pending |

**Coverage:** 28 total, mapped in tasks.md

---

## Success Criteria

- [ ] On a clean machine: `curl …/install.sh | sh` and `partyline setup --browser`, then `partyline claude` in two project folders shows two online agents.
- [ ] Manual UAT with a real omp or pi session receiving a room message (recorded in uat.md).
