# Agent Channel UAT: real Claude Code smoke test

**Result**: PASS
**Date**: 2026-10-20
**Build**: `main` at `b1bbf9a` (`go build ./cmd/partyline`)
**Claude Code**: 2.1.303, model haiku, claude.ai login

## Setup

- `partyline serve` on a free loopback port with a temporary data dir.
- Admin account from `partyline admin create-user`; a user token for scripting, an agent token
  and a room `Smoke` created through the REST API.
- MCP config file (outside the repository, mode 0600) running the built binary as
  `partyline channel` with `PARTYLINE_URL`, `PARTYLINE_TOKEN` and `PARTYLINE_AGENT=smoke-agent`.
- Claude Code started in an Orca terminal:
  `claude --model haiku --strict-mcp-config --mcp-config <file> --dangerously-load-development-channels server:partyline`.

## Steps and observations

| # | Step | Expected | Observed |
| --- | --- | --- | --- |
| 1 | Start Claude Code | Folder trust prompt, then the development channels warning | Both shown; accepted with Down + Enter, then Enter |
| 2 | Session up | Banner "Channels (experimental) messages from server:partyline inject directly in this session" | Shown |
| 3 | Presence | `GET /agents` lists `smoke-agent` online (AGT-01) | `online: true` |
| 4 | Add the agent to the room, post "please call the reply tool ... pong" as the user | Message reaches the session (AGT-05) | Session showed `← partyline: Smoke test: ...` |
| 5 | Agent acts | `reply` tool stores the reply (TOOL-02) | Room history: seq 2, author_kind `agent`, author `smoke-agent`, text `pong`, `reply_to` = the user message id |
| 6 | Close the Orca terminal | Agent offline (AGT-03) | `online: false` |
| 7 | Server log | No message text, no tokens | 0 matches for message text and token prefixes |

## Notes

- With `--mcp-config` the startup banner also printed
  `server:partyline · no MCP server configured with that name`, yet the channel registered and
  delivered messages. It looks like a check that runs before servers from `--mcp-config` are
  loaded; the documented `.mcp.json` setup was not exercised in this run.
- The test data dir, tokens and config file were deleted after the run.
