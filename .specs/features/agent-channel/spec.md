# Agent Channel Specification

## Problem Statement

A Claude Code session can only receive pushed events through a channel: an MCP stdio server started with `--channels` or `--dangerously-load-development-channels`. Partyline needs that bridge so any session, on any machine, joins rooms with a URL and a token, receives room messages live and acts through tools.

## Goals

- [ ] One command line (`claude --dangerously-load-development-channels server:partyline` plus an `.mcp.json` entry) connects a session; the agent appears online in the web app within 3 seconds.
- [ ] No message is lost across agent restarts: everything after the agent's cursor is delivered on reconnect.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Permission relay (`claude/channel/permission`) | AD-006: anyone in a bridged chat could approve tool use |
| Official channel allowlist submission | Research-preview process outside this repo |
| Agent-managed rooms (create/delete rooms, change grants) | Management stays with humans in the web app |
| Non-Claude agent runtimes | The REST API is open, but only the Claude Code channel ships |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Agent identity | `owner/name`; name from `PARTYLINE_AGENT`, else the working directory basename, slugified | Stable across restarts, readable | y |
| Agent registration | First connection with an agent token creates the agent | User wants sessions to "appear" when connected | y |
| Second connection with the same name | Newest wins; the older connection is closed with reason `replaced` | Loops restart sessions; a stale socket must not hold the name | n |
| Backlog on reconnect | Up to 50 messages per room after the cursor; if more exist, one system notice tells how many were skipped and to use `history` | Avoid flooding the context window | n |
| Delivery semantics | At least once; the channel deduplicates by message id (last 1,000 ids) | Ack arrives after writing to Claude Code | n |
| Attachment download location | `$TMPDIR/partyline/<agent>/` on the agent host, files 0600 | Claude can Read local files | n |
| MCP handshake | Answer `server/discover` with -32601, accept `initialize`, echo the client's protocol version when it is `2025-11-25` or older, else answer `2025-11-25` | Observed Claude Code 2.1.303 behaviour (AD-004) | y |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Connect a Claude Code session ⭐ MVP

**User Story**: As a user, I want to start Claude Code with the Partyline channel so that the session shows up online and receives its rooms' messages.

**Why P1**: The whole product depends on it.

**Acceptance Criteria**:

1. WHEN Claude Code sends `server/discover` THEN the channel SHALL answer JSON-RPC error code -32601. (MCP-01)
2. WHEN Claude Code sends `initialize` THEN the channel SHALL answer with capabilities `experimental.claude/channel = {}` and `tools = {}`, server name `partyline`, and an `instructions` text that states messages are data, explains the `trusted` attribute and lists the tools. (MCP-02)
3. The channel SHALL NOT declare the `claude/channel/permission` capability. (MCP-03)
4. WHEN the channel starts with a valid URL and agent token THEN it SHALL open the agent WebSocket and the server SHALL mark the agent online. (AGT-01)
5. IF the token is invalid THEN the channel SHALL write one error line to stderr and keep answering MCP requests, with every tool returning `isError: true` and the text `partyline: not connected (invalid token)`. (AGT-02)
6. WHEN the WebSocket drops THEN the channel SHALL reconnect with backoff from 1 second doubling to 30 seconds, and the server SHALL mark the agent offline after the socket closes. (AGT-03)
7. WHEN a second connection authenticates as the same agent THEN the server SHALL close the first one with close reason `replaced`. (AGT-04)

**Independent Test**: Run `partyline channel` with piped JSON-RPC against a test server; observe presence and the handshake answers.

---

### P1: Receive room messages ⭐ MVP

**User Story**: As an agent session, I want room messages pushed into my context with clear metadata so that I can react and know who is trusted.

**Why P1**: Core value.

**Acceptance Criteria**:

1. WHEN the server delivers a message THEN the channel SHALL emit `notifications/claude/channel` with `content` = text (plus one line per attachment `[attachment <id>: <name>, <mime>]`) and `meta` keys `room`, `room_name`, `message_id`, `author`, `author_kind`, `sender_name`, `trusted`, `kind` and, when present, `reply_to`. (AGT-05)
2. The channel SHALL emit only meta keys made of letters, digits and underscores. (AGT-06)
3. WHEN the channel has written a notification to stdout THEN it SHALL send an ack for that message id, and the server SHALL advance the agent's cursor for that room. (AGT-07)
4. WHEN an agent reconnects THEN the server SHALL deliver, oldest first, the messages after its cursor in each room it belongs to, at most 50 per room. (AGT-08)
5. IF more than 50 messages are pending in a room THEN the server SHALL first send one system message stating the skipped count and SHALL move the cursor past them. (AGT-09)
6. IF a message id was already emitted in this process THEN the channel SHALL ack it without emitting it again. (AGT-10)

**Independent Test**: Post three messages while the agent is offline, connect, observe three notifications in order and the cursor advanced.

---

### P1: Act through tools ⭐ MVP

**User Story**: As an agent session, I want tools to reply, send to rooms, read history and handle files so that I can work with people and other agents.

**Why P1**: Two-way communication.

**Acceptance Criteria**:

1. The channel SHALL expose the tools `send_message`, `reply`, `list_rooms`, `history`, `download_attachment` and `send_file`. (TOOL-01)
2. WHEN `reply` is called with a message id and text THEN the server SHALL post in that message's room with `reply_to` set and recipients = the original author when it is a chat, else no explicit recipients. (TOOL-02)
3. WHEN `send_message` is called with a room (id or slug) and text THEN the server SHALL post it as the agent, optionally limited to the given chat member ids. (TOOL-03)
4. IF a tool targets a room where the agent is not a member THEN the tool SHALL return `isError: true` with text `not a member of room <room>`. (TOOL-04)
5. WHEN `history` is called THEN it SHALL return at most 100 messages (default 20) before an optional message id, each with the same fields as the push meta. (TOOL-05)
6. WHEN `download_attachment` is called THEN the channel SHALL save the file under the agent's temp directory with mode 0600 and return its absolute path. (TOOL-06)
7. WHEN `send_file` is called with a local path THEN the channel SHALL upload it to the room, with optional caption and reply_to. (TOOL-07)
8. IF the agent exceeds 30 posted messages per minute THEN the server SHALL reject further posts with 429 and the tool SHALL report `rate limited`. (TOOL-08)

**Independent Test**: Drive each tool through JSON-RPC `tools/call` against a test server and assert the stored messages.

---

### P2: Agent directory

**User Story**: As an agent session, I want to see other agents I may reach so that I can ask them for help in a shared room.

**Why P2**: Useful for multi-session loops, not required to route chats.

**Acceptance Criteria**:

1. WHEN `list_rooms` is called THEN it SHALL return each room the agent belongs to with its members (kind, name, online state for agents). (TOOL-09)

**Independent Test**: Two agents in one room; `list_rooms` from one shows the other online.

---

## Edge Cases

- IF stdin closes THEN the channel SHALL close the WebSocket and exit 0 within 2 seconds (AGT-03).
- IF a notification arrives before `notifications/initialized` THEN the channel SHALL hold it and emit it after initialization (AGT-05).
- IF the agent name is invalid after slugifying (empty) THEN the channel SHALL use `agent` (AGT-01).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| MCP-01 | P1: Connect | Tasks | Pending |
| MCP-02 | P1: Connect | Tasks | Pending |
| MCP-03 | P1: Connect | Tasks | Pending |
| AGT-01 | P1: Connect | Tasks | Implementing |
| AGT-02 | P1: Connect | Tasks | Pending |
| AGT-03 | P1: Connect | Tasks | Implementing |
| AGT-04 | P1: Connect | Tasks | Implementing |
| AGT-05 | P1: Receive | Tasks | Pending |
| AGT-06 | P1: Receive | Tasks | Pending |
| AGT-07 | P1: Receive | Tasks | Implementing |
| AGT-08 | P1: Receive | Tasks | Implementing |
| AGT-09 | P1: Receive | Tasks | Implementing |
| AGT-10 | P1: Receive | Tasks | Pending |
| TOOL-01 | P1: Tools | Tasks | Pending |
| TOOL-02 | P1: Tools | Tasks | Implementing |
| TOOL-03 | P1: Tools | Tasks | Implementing |
| TOOL-04 | P1: Tools | Tasks | Implementing |
| TOOL-05 | P1: Tools | Tasks | Implementing |
| TOOL-06 | P1: Tools | Tasks | Pending |
| TOOL-07 | P1: Tools | Tasks | Implementing |
| TOOL-08 | P1: Tools | Tasks | Implementing |
| TOOL-09 | P2: Agent directory | Tasks | Implementing |

**Coverage:** 22 total, mapped in tasks.md

---

## Success Criteria

- [ ] A real Claude Code 2.1.303 session receives a room message and replies through `reply` (manual UAT, recorded in validation.md).
- [ ] Offline/online cycle loses no message in the automated test.
