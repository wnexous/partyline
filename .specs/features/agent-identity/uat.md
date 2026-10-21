# Agent Identity UAT: real Claude Code sessions

**Result**: PASS after one fix (startup backlog, see Finding 1)
**Date**: 2026-10-21
**Build**: `main` at `aee919a`, then `558eb7f` (the channel fix) for steps 8 to 11 (`go build ./cmd/partyline`)
**Claude Code**: 2.1.304, claude.ai login

## Setup

- `partyline serve` on a free loopback port with a temporary data dir; admin account through
  `POST /setup`, one agent token through `POST /tokens` (kept in a 0600 file, never on a command
  line).
- Four temporary HOMEs, each starting with only a copy of `~/.claude/.credentials.json`.
  `partyline setup --url <instance> --token-stdin --harness claude --yes` ran in each, so the
  partyline config file and the Claude Code MCP entry live in that HOME only.
- Sessions ran in Orca terminals as `partyline claude` with a clean environment (`env -i`, HOME
  set to the temporary one, no `PARTYLINE_*` variables), read with `orca terminal read --screen`
  and driven with `orca terminal send` (onboarding: theme, security notes, folder trust, the
  development channels warning).
- Project folder: a plain folder `proj` (no git), shared by sessions 1, 2 and 4.

## Steps and observations

| # | Step | Expected | Observed |
| --- | --- | --- | --- |
| 1 | Session 1 in `proj` | Temporary agent "Proj · Claude Code" (ID-01) | `proj-claude`, display "Proj · Claude Code", harness `claude`, machine = hostname, temporary, project Proj |
| 2 | Session 2 in `proj` (session 1 online) | "Proj · Claude Code #2" (ID-01) | `proj-claude-2`, "Proj · Claude Code #2", project Proj |
| 3 | Session 3 in its HOME root | Project `home` and one warning line (ID-03) | Terminal printed `partyline claude: started in the home folder; using project "home" (pass --project <name> or --name <agent> to choose)`; agent `home-claude`, "Home · Claude Code", project Home |
| 4 | Room "UAT" with project Proj as member | Member shows the active session (PROJ-07) | Add answered `kind: project`, `active_session` = `proj-claude-2` |
| 5 | Post "ping one" | Only the active session (#2) gets it (PROJ-07) | Session 2 showed `← partyline: UAT ping one…` and answered; session 1 showed nothing; project cursor moved to 1 |
| 6 | Close sessions 1 and 2, post "ping two" | Message waits (PROJ-07) | Both offline; nothing delivered |
| 7 | Open session 4 in `proj` | It receives "ping two" as backlog (PROJ-08) | **Failed at first**: the server delivered it and the channel acknowledged it (cursor 2), but Claude Code never showed it. See Finding 1 |
| 8 | Same check with the fix | Backlog shown | "ping seven", "ping eight" and, with the final build, "ping nine" (posted while no Proj session was open) appeared in the new session right after start; cursor moved to each one |
| 9 | Active session replies | Reply authored by that session through the project's room (PROJ-09, REST rule) | On "ping seven" Claude called the reply tool: room history seq 8, `author_kind agent`, author "Proj · Claude Code" |
| 10 | Live message to the new session | Pushed at once | "ping four" shown and answered |
| 11 | Server log | No message text, no tokens, no errors | 0 matches for message text, for `pl_agent_` and for level ERROR |

## Finding 1: Claude Code drops the backlog sent right after it initializes (fixed)

Claude Code 2.1.304 sends `initialize`, `notifications/initialized`, then `tools/list`
(recorded with a `tee` wrapper around the MCP command). Channel notifications written in that
window are acknowledged by the channel but never shown, so the connect backlog (agents and
projects alike) was lost; live messages after startup were fine. Holding notifications until
the first `tools/list` answer was not enough; 1.5 s after it was. The fix in
`internal/channel/mcp` holds every notification until `ReadyDelay` (2 s) after the first
`tools/list` answer, or `ReadyFallback` (5 s) after `notifications/initialized` for a client
that never lists the tools (`TestNotificationWaitsUntilAfterToolsList`,
`TestNotificationFallbackWithoutToolsList`). The channel acknowledges a message only after its
notification is written, so nothing is acknowledged early.

## Notes

- Claude answered the pings in the terminal on its own; it used the reply tool only when the
  message asked it to reply in the room.
- The project's sessions reuse the lowest free temporary name: session 4 became `proj-claude`
  again after sessions 1 and 2 had closed.
- Cleanup: every UAT terminal was closed, the server stopped, and the data dir, temporary HOMEs,
  Claude Code's per-folder scratch directories and scripts were deleted.
