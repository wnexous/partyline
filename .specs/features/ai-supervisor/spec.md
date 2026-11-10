# AI supervisor and scheduled heartbeats

Status: Specify + Design written 2026-11-10, awaiting the owner's decisions (below). Requirement ids
SUP-xx (supervisor) and HB-xx (heartbeats). Owner requests (2026-11-10):
1. "AI always present": a Partyline supervisor on the machine keeps the AIs' sessions alive, resumes
   the same conversation when one dies, and wakes an AI when a message arrives while it is offline.
   This is the start of Partyline's own harness.
2. "Configurable heartbeat": per AI, schedules such as "every 1 h do X" or "every day at 9:00 do Y",
   a prompt delivered to the AI's session; the AI edits them through a tool, the owner in the profile.

## Problem
An AI exists only while a person keeps a terminal open with its harness. Seen in use:
- a session dropped and stayed offline until someone restarted it by hand;
- a session started without the launcher (`claude` instead of `partyline claude`) needed a manual
  restart to connect;
- DMs to an AI whose principal was offline reached nobody: they wait in the principal's queue (SESS-23)
  until a person happens to resume the pinned conversation.

And nothing makes an AI act on its own: every turn starts with a message from someone. A routine
("check the deploys every hour", "send the daily summary at 9:00") needs a person or an outside cron.

## What exists (and is reused)
- **The principal is pinned to a harness session id** (`agents.principal_session`, SESS-51..SESS-59):
  Claude Code's `CLAUDE_CODE_SESSION_ID`, pi's id through `PARTYLINE_HARNESS_SESSION`. Resuming that
  conversation gets the principal back; anything else joins as a parallel session. OpenCode and omp
  send no id: their folder's first live session is the principal (SESS-56).
- **Backlog on connect**: what was for an offline principal is delivered when the pinned session
  connects (SESS-23, SN-03). Platform events wait in the AI's queue (7 days, 5,000).
- **Client version** on every connection (SESS-63): the server knows what a session's CLI understands.
- **Environment hygiene** in the launcher (`consumedVars`, `ownHarnessSessionID`): a harness started
  from inside another session once inherited `CLAUDE_CODE_SESSION_ID` and took the real principal over.
- **Removed container agents** (AD-039, 2026-11-04): AIs in Docker on "runner" servers, a secret store,
  an agent image, unattended Claude Code fed Enters into a pty to pass the development-channel dialog.
  The owner rejected the model (a second product inside the server, secrets in Partyline, the AI cut
  from the folder it really works in), not the goal. This design keeps the AI on its own machine, in
  its own folder, with the harness's own login, and keeps the server out of running anything.

## Goals
- [ ] An AI the machine's user opted in comes back by itself after its session dies, in the same
      conversation, and a crash loop stops and is reported instead of spinning.
- [ ] A message for an offline opted-in AI starts its session and is delivered within ~30 s.
- [ ] A person can always open the AI by hand; the supervised session hands over, never fights.
- [ ] Each AI has schedules (interval or calendar, Brasília time by default) whose prompt reaches its
      principal session; the AI and its managers edit them; every run is visible and audited.
- [ ] Unattended work has an explicit permission policy per AI, chosen on the machine.

## Non-goals
- Running AIs anywhere but the machine and folder they already are (no containers, no runners).
- The server starting, choosing or configuring programs: it may only say "wake", "yield", "pause".
- Replacing the harnesses: they keep their model loop; Partyline drives their input and lifecycle.

## Requirements

### Heartbeats
1. Whoever manages an AI (the POS-04 rule) and the AI itself SHALL create, list, edit, enable,
   disable and delete the AI's schedules; a schedule has a name (1-60), a prompt (1-4000 characters),
   a rule (`every N minutes` or a 5-field cron), a timezone (IANA, default `America/Sao_Paulo`), a
   catch-up policy (`once` default, or `skip`) and `enabled`. (HB-01)
2. An AI SHALL change or delete only the schedules it created; it SHALL see those its managers set,
   read-only (403 `forbidden` on a write). A manager SHALL change any schedule of the AI. (HB-02)
3. IF a rule fires more often than every 15 minutes, OR the AI's enabled schedules together fire more
   than 48 times in any 24 hours, OR the AI already has 20 schedules THEN the write SHALL be refused
   with 422 `too_frequent` (or `too_many`) naming the limit. (HB-03)
4. WHEN a schedule is due and the AI's principal session is live THEN the server SHALL push one
   `heartbeat` frame to that session only (never a parallel one), and record the run `delivered` when
   the session acknowledges it. (HB-04)
5. WHILE a run of a schedule is unacknowledged the next run of that schedule SHALL NOT be pushed; it
   replaces the pending one (`superseded`), so runs never pile up. (HB-05)
6. WHEN a schedule is due and the principal is not live THEN with `catch_up: once` the run SHALL wait
   and be delivered when the principal connects, while it is within its window (the shorter of the
   period and 12 hours); later it is `expired`. With `skip` it is recorded `missed` and not delivered.
   The same holds for runs missed while the server was down: at most one catch-up run per schedule.
   WHEN the AI is supervised THEN a due run SHALL also wake it (SUP-12). (HB-06)
7. WHEN the principal's client is older than the first version that renders a heartbeat THEN the run
   SHALL NOT be sent (an old client ignores unknown frames) and SHALL be recorded `client_outdated`,
   shown on the profile like SESS-65. (HB-07)
8. The heartbeat SHALL reach the model as a turn, not as a notice: Claude Code as a channel event
   `type="heartbeat"`, pi/OpenCode/omp as an injected user message, carrying the schedule name, who set
   it (`origin="manager"` with the setter's handle, or `origin="self"`) and `trusted="true"` only when
   the setter manages the AI at delivery time. (HB-08)
9. WHEN an AI creates, edits or re-enables one of its own schedules THEN its nearest manager SHALL get
   one DM from the AI's identity, written by the server, naming the schedule, its rule and its prompt,
   with quick replies "Manter" and "Pausar" (Pausar disables it); at most one such DM per schedule
   per hour. (HB-09)
10. An AI SHALL make at most 20 schedule writes an hour (429 `rate_limited`). (HB-10)
11. Every create, edit, delete, enable/disable and "run now" SHALL be in the audit log (actor, AI,
    schedule, rule; not the prompt), and every run SHALL be kept 30 days with due time, fired time,
    status (`delivered | superseded | missed | expired | client_outdated`) and session. (HB-11)
12. The prompt SHALL be visible only to the AI and to whoever manages it; the members who merely see
    the AI SHALL see that it has routines, not their text. (HB-12)
13. The AI's profile in the web app SHALL list its schedules (name, "todo dia às 09:00" in words, next
    run, last run and status, who set it) and let a manager create, edit, pause, delete and run one
    now; mobile first at 360 px, `web/AGENTS.md`. (HB-13)

### Supervisor
14. `partyline supervisor` SHALL be a long-running process of the machine's user, one per user and
    machine (a lock file; a second one exits 1 naming the first's pid); `partyline supervisor install`
    SHALL write a systemd user unit (`Restart=always`, the `loginctl enable-linger` hint) and
    `uninstall` remove it; `partyline setup` SHALL offer it. (SUP-01)
15. The supervisor SHALL manage only the AIs registered on this machine with `partyline supervise add
    [folder] --harness <h> [--mode always|on-demand] [--autonomous]` (stored in
    `~/.config/partyline/supervisor.json`, mode 0600); the folder SHALL already be an AI of the token's
    user (checked with `GET /agent/session`); `supervise list|remove|pause|resume` manage the list.
    (SUP-02)
16. The supervisor SHALL start only the four harness binaries Partyline supports, found on PATH, in a
    registered folder, with an argument vector built by the CLI (never a shell string, never a value
    from the server), and an environment built from an allowlist (HOME, USER, PATH, LANG, LC_*, TZ,
    TERM, XDG_*, the harness's own config variables), so no `PARTYLINE_*`, `CLAUDE_CODE_*` or
    `*_SESSION*` variable of wherever the supervisor itself was started reaches the harness. (SUP-03)
17. The supervisor SHALL resume the AI's pinned conversation (the id the server reports for the AI),
    and start a new conversation only when there is none or the harness cannot find it; a new one
    SHALL be pinned (SESS-57 semantics) and the nearest manager told that the context was lost.
    (SUP-04)
18. WHEN a supervised `always` session exits or its connection is lost for more than 60 s THEN the
    supervisor SHALL restart it with backoff (2 s doubling to 5 min, reset after 10 min up). (SUP-05)
19. IF a supervised session exits 5 times in 15 minutes THEN the supervisor SHALL stop restarting it
    (state `crash_loop`) until `supervise resume` or the web's Resume, and SHALL report it (SUP-11).
    An exit for a missing login (`needs_login`) or a usage limit (`rate_limited`, retried at the reset
    time the harness gave, else in 30 min) SHALL NOT count as a crash. (SUP-06)
20. The supervisor SHALL hold one connection to the server, `GET /api/v1/agent/supervisor/ws`, with the
    machine's agent token, report each managed AI's state (`starting | running | idle_stopped |
    yielded | paused | crash_loop | needs_login | rate_limited`, restarts in 24 h, last error) and
    accept only `wake`, `yield`, `pause` and `resume` for AIs it registered; any other frame or AI is
    ignored and logged by type. (SUP-07)
21. A supervised session SHALL connect with `X-Partyline-Supervised: 1`. IF the principal is live in
    another process THEN a supervised connection SHALL NOT take it over: the server SHALL refuse it with
    409 `principal_live` and the supervisor waits (state `yielded`). (SUP-08)
22. WHEN the server closes a supervised session with 4002 (a person took over, resumed the pinned id,
    or a manager ended it) THEN the supervisor SHALL stop that harness process and SHALL NOT restart
    it until the principal has been offline for 2 minutes; a manager's "end session" SHALL also pause
    supervision until resumed. (SUP-09)
23. WHEN `partyline <harness>` starts in a supervised AI's folder and resumes the pinned conversation
    or asks for `--principal`/`--take-over` THEN the launcher SHALL ask the server to make the
    supervised session yield, wait for it (at most 30 s: the current turn ends, then SIGTERM) and only
    then exec the harness, printing one line that says so; a launch that becomes a parallel session
    SHALL NOT disturb the supervised one. (SUP-10)
24. WHEN a supervised AI's session cannot be kept (crash loop, needs login, lost context) THEN the
    supervisor SHALL send one DM, as the AI, to its nearest manager with the state and the last error
    line (redacted), at most one per AI per hour; the profile SHALL show the same. (SUP-11)
25. WHEN a frame becomes pending for the principal of a supervised AI that is not live (a DM, a
    mention, a room message its delivery mode wants, a platform event, a due heartbeat) THEN the server
    SHALL send `wake` to that machine's supervisor, at most once per AI every 30 s, and the supervisor
    SHALL start the session unless it is `paused`, `yielded` or `crash_loop`; the backlog SHALL arrive
    by the existing connect path. (SUP-12)
26. WHEN an `on-demand` session has had no frame and no turn in progress for 20 minutes THEN the
    supervisor SHALL stop it gracefully (state `idle_stopped`); the conversation is kept for the next
    wake. (SUP-13)
27. Each managed AI SHALL have a permission policy fixed on the machine: `ask` (default: a tool use
    that needs permission becomes a question to the AI's nearest manager, denied after 10 minutes
    without an answer) or `autonomous` (`--autonomous`: the harness's bypass mode). The server SHALL
    NOT change it. (SUP-14)
28. The supervisor SHALL NOT log message content, prompts or tool output: only frame types, exit codes,
    the harness's stderr (last 200 lines per AI, rotated) and states. No token SHALL appear in argv,
    a log line or a report. (SUP-15)
29. The AI's profile SHALL show its supervision (machine, mode, state, since, restarts, last error,
    supervisor online or offline since) and offer Pause/Resume to whoever manages it. (SUP-16)

## Design

### Heartbeats (server)
- **Data (next migration, 0038 today):** `agent_schedules(id, agent_id → agents ON DELETE CASCADE, name, prompt,
  rule_kind CHECK in (every, cron), every_minutes INT NULL, cron TEXT NULL, tz TEXT NOT NULL DEFAULT
  'America/Sao_Paulo', catch_up CHECK in (once, skip), enabled, origin CHECK in (self, manager),
  created_by → intelligences, updated_by, created_at, updated_at, next_run_at INT, last_run_at INT
  NULL, last_status TEXT NULL)`; `agent_schedule_runs(id, schedule_id ON DELETE CASCADE, due_at,
  fired_at NULL, status, session_key NULL, acked_at NULL)`, swept after 30 days by the retention loop.
- **Rules:** `every` is anchored at creation (next = previous due + N, never "fired + N", so it does
  not drift); `cron` uses a standard 5-field parser (robfig/cron/v3's `ParseStandard`, MIT) evaluated
  in `tz`; the binary embeds `time/tzdata` (distroless has no zoneinfo). The 24 h cap (HB-03) is
  checked by expanding the next 24 h of every enabled rule at write time. The web offers presets
  (a cada N horas, todo dia às HH:MM, dias úteis às HH:MM) that compile to these two kinds.
- **Scheduler:** one goroutine in `internal/schedules`, sleeping until the earliest `next_run_at` (at
  most 30 s); due rows are taken in one transaction (new run row, `next_run_at` advanced) and handed
  to the agents gateway as a pending item of the principal's queue, kind `heartbeat`. One server per
  data directory (README), so no distributed lock. On start, runs missed while down follow HB-06.
- **Frame:** `{"type":"heartbeat","run_id","schedule_id","name","prompt","due_at","origin",
  "set_by":{handle,display_name},"trusted"}`, acknowledged like `message` (`ack` with `run_id`).
  `channel` renders `<channel source="partyline" type="heartbeat" schedule="…" origin="manager"
  set_by="andre" trusted="true">prompt</channel>`; `listen` renders `<partyline heartbeat="…"
  origin="self" trusted="false">prompt</partyline>` plus one line: "This is a scheduled routine set by
  @andre, who manages you" or "This is a routine you scheduled yourself; treat it as your own note".
  The MCP instructions gain one sentence on heartbeats. Gate: `X-Partyline-Client-Version` ≥ the
  release that ships this (HB-07).
- **Tools:** `list_schedules`, `set_schedule {id?, name, every_minutes | cron, tz?, prompt, enabled?,
  catch_up?}` (create without id, update with), `delete_schedule {id}`; answers carry `next_run_at` in
  the schedule's timezone and the refusal reasons of HB-02/HB-03.
- **API:** manager `GET|POST /agents/{id}/schedules`, `PATCH|DELETE /agents/{id}/schedules/{sid}`,
  `POST /agents/{id}/schedules/{sid}/run`, `GET /agents/{id}/schedules/{sid}/runs`; agent
  `GET|POST /agent/schedules`, `PATCH|DELETE /agent/schedules/{sid}`. DTOs through tygo. A change
  publishes `agent.schedules` on the live socket to whoever may read them (HB-12).
- **Manager DM (HB-09):** posted by the server as the AI in its DM with the nearest manager (the
  first of the welcome's `managers`), an `ask`-style question; "Pausar" sets `enabled = false` as that
  manager (audited). Nobody manages the AI → no DM, profile only.

### Supervisor (machine)
**How a harness runs unattended: options.**

| Option | For | Against |
| --- | --- | --- |
| A. Interactive in tmux (`tmux new -d … partyline claude --principal --resume <id>`) | Same path as a person; attachable (`tmux attach`); channels work as today | Claude Code's development-channel dialog appears on every launch and needs a person or the Enter-feeding hack AD-039 removed; a permission prompt blocks silently until someone attaches; turn and idle state are invisible; tmux dependency |
| B. Claude Code background sessions (`claude --bg --resume <id>`, `claude attach`, 2.1.3xx) | Supported detach/attach by Anthropic; keeps the id | New and moving; same dialog question unverified; "starts a copy when the session is already running" forks the conversation; lifecycle owned by `claude agents`; Claude only |
| C. Headless, driven by the supervisor: `claude -p --input-format stream-json --output-format stream-json --resume <id>`; `pi --mode rpc --session-id <id>`; `omp --mode rpc --resume <id>`; `opencode serve` + its HTTP API | No TTY, no dialog; turn ends, errors and usage limits are machine-readable (idle stop, crash reasons); permissions explicit (`--permission-prompt-tool`); the same conversation id can be reopened interactively by a person | Channels do not load in `-p`, so for Claude the supervisor is the bridge (it holds the agent socket and writes each frame as a stream-json user message, as `listen` does for pi); nobody sees the screen; one small backend per harness; stdout carries conversation content and must never be logged |

Recommended: **C**. It is the "own harness" the owner asked for: Partyline owns the input, the
lifecycle and the policy, the harness keeps the model loop and its login. Cost: an idle process spends
no tokens (only turns do) but holds 200-500 MB, so `on-demand` (wake + idle stop) is the default
and `always` is for the few AIs that must answer at once.

- **Backends** (`internal/supervisor/backend`): `claude` (stream-json bridge: the supervisor opens the
  AI's agent WebSocket as the principal with `X-Partyline-Harness-Session: <id>` and `Supervised: 1`,
  renders frames with the channel's own formatter, acks after the line is written to stdin, and gives
  the harness the tools through `--mcp-config` → `partyline channel --tools-only --harness claude`
  with a shared `PARTYLINE_LIVE`, the pi/OpenCode model of SESS-45); `pi` and `omp` (rpc mode; the
  existing extension runs `listen` inside it, to confirm in T0); `opencode` (`serve` on a loopback
  port with a random password, the existing plugin runs `listen`; OpenCode sends no session id, so its
  principal follows SESS-56 and SUP-08 keeps the supervised one from taking a person's place).
- **Turn and idle:** Claude's `result` event, pi/omp `agent_end`, OpenCode `session.idle` end a turn;
  idle time counts from the last turn end with nothing pending.
- **Graceful stop:** stop writing input, wait for the turn's end (≤ 30 s), close stdin, SIGTERM, then
  SIGKILL after 10 s; a stopped conversation is resumable.
- **Exit classification:** the stream's error events and stderr patterns per backend map to
  `needs_login`, `rate_limited` (with the reset time when given), `resume_failed` or a crash.
- **Supervisor socket:** `GET /agent/supervisor/ws`, headers `X-Partyline-Machine` and client version.
  Hello `{ais: [{folder, harness, mode}]}` → server resolves each (owner = token's user, machine,
  folder; never creates an AI) and answers `{ais: [{agent_id, handle, folder, pinned_session,
  principal_live, managers[0]}]}`. Server → supervisor: `wake {agent_id, reason}`, `yield {agent_id,
  request_id}`, `pause|resume {agent_id}`. Supervisor → server: `state {agent_id, state, restarts_24h,
  last_error}`, `yielded {request_id}`. A new socket of the same (user, machine) replaces the old.
  Table `agent_supervision(agent_id PK, machine, mode, state, restarts_24h, last_error, paused,
  supervisor_seen_at, updated_at)`, written from reports (SUP-16).
- **Yield (SUP-10):** `GET /agent/session` gains `supervised` and the pinned id's owner process; the
  launcher, when it will become the principal, calls `POST /agent/session/yield`, the server forwards
  `yield`, the supervisor stops gracefully and answers `yielded`, the server answers 204 (504 after
  30 s; the launcher then starts anyway and SESS-53 closes the old one). Same-machine guard: before a
  start the supervisor scans `/proc/*/environ` (the launcher already reads it) for a live process
  with `CLAUDE_CODE_SESSION_ID` / `PARTYLINE_HARNESS_SESSION` equal to the pinned id and never starts
  a second writer of one conversation.
- **Permission `ask` (SUP-14):** Claude: `--permission-prompt-tool mcp__partyline__permission`, a tool
  only the supervised tools server exposes, that posts an `ask` (Permitir / Negar / Permitir sempre
  nesta sessão) in the DM with the nearest manager and waits for the answer (deny at 10 min). pi and
  omp ask no permission at all, so `ask` cannot apply: `supervise add` for them requires
  `--autonomous` and says why. OpenCode: its `permission.asked` events are answered the same way (P4).
- **Files:** `~/.config/partyline/supervisor.json` (0600: folder, harness, mode, policy, the last id it
  ran), `~/.local/state/partyline/supervisor/<handle>.log` (stderr only), `supervisor.lock`.
- **Orca and other ADEs:** nothing depends on them. The unit runs outside any terminal; on a machine
  where everything lives in an ADE the supervisor can run in one of its terminals in the foreground.
  Sessions people open in ADE terminals are hand sessions: they win (SUP-08..SUP-10).

### Security
- The server can only name an AI the supervisor itself registered and ask wake/yield/pause/resume. It
  never sends a path, an argument, a command or an environment value (SUP-03, SUP-07).
- Opting in happens only on the machine, by its user (`supervise add`); the web can pause, never
  enable. `--autonomous` is a local flag with a warning printed once; the profile shows it.
- Secrets: the Partyline token stays in `config.json` (0600); harness logins stay in the harness's
  own files; nothing secret on a command line; the OpenCode server password is random per start.
- Unattended AIs act with the user's full power on that machine. `ask` routes every permission
  decision to a person; `autonomous` is the owner's explicit choice per AI. Heartbeat prompts are
  either a manager's (trusted) or the AI's own (marked `origin="self"`, never trusted) and every AI
  write is shown to a manager (HB-09), so an injected message cannot hide a persistent routine.
- Loops: heartbeats count toward nothing new; the existing loop guard and the 30 posts a minute still
  bound what an AI writes; HB-03 bounds turns started by schedules.

## Phases
0. **T0 spike (1 day, no product code):** on the installed versions (Claude Code 2.1.318, pi, omp,
   OpenCode), confirm: `-p` stream-json keeps the id on `--resume`; `--permission-prompt-tool` with an
   MCP tool; pi/omp rpc load the extension and its `listen`; `opencode serve` loads the plugin and
   resumes a session; RAM of each idle process. Results amend this spec.
1. **Heartbeats on the server** (HB-01..HB-12): tables, scheduler, API, tools, frame in channel/listen
   and the adapters, catch-up, version gate, manager DM, audit. Live sessions only; no supervisor.
2. **Heartbeats in the web profile** (HB-13): the owner edits them by hand.
3. **Supervisor keep-alive** (SUP-01..SUP-11, SUP-14, SUP-15): process, unit, `supervise`, Claude
   backend, restart policy, env allowlist, yield, supervisor socket, reports. `always` mode only.
4. **Wake on message and the rest** (SUP-12, SUP-13, SUP-16): wake from the dispatcher and the
   scheduler, `on-demand` and idle stop, pi/omp/OpenCode backends, supervision in the web profile.

## Open decisions for the owner
1. **Engine:** headless (C), tmux (A) or Claude `--bg` (B)? *Recommended: C.*
2. **Which AIs:** opt-in per folder on the machine, or every AI the machine hosts? *Recommended:
   opt-in (`partyline supervise add`); the web only pauses/resumes.*
3. **Default mode:** `on-demand` or `always`? *Recommended: on-demand (idle stop at 20 min); always
   for always-on assistant AIs.*
4. **Unattended permissions (Claude):** ask a manager in Partyline, `acceptEdits` + the folder's
   allowlist, or bypass? *Recommended: ask by default, `--autonomous` per AI when you want it.*
5. **Routines an AI creates:** free with a DM to the manager (Manter/Pausar), or only after approval?
   *Recommended: free + DM.*
6. **Minimum interval:** 5, 15 or 60 minutes? *Recommended: 15 min and at most 48 runs a day per AI.*
7. **Missed runs:** run once or skip? *Recommended: once, within min(period, 12 h).*
8. **Who reads the prompts:** managers only, or everyone who sees the AI? *Recommended: managers.*
9. **Where failures are reported:** DM from the AI to its nearest manager, or push only?
   *Recommended: DM, at most one per hour per AI.*

## Risks
- Claude Code's `-p` and channel behaviour change often (the dialog, `--bg`, permission flags); the
  backend is isolated and T0 is re-run on each Claude Code update the supervisor depends on.
- Two writers on one conversation transcript (a person resuming while the headless one runs): the
  yield protocol, the `/proc` guard and SESS-53 close it; a manual `claude --resume` without the
  launcher can still race and is documented.
- Cost: heartbeats and wakes start paid turns. HB-03, on-demand and the profile's runs list make it
  visible; a usage limit pauses (`rate_limited`) instead of retrying.
- RAM on a shared host (several always-on Node processes): on-demand by default; `supervisor status`
  prints each process's RSS.
- A conversation resumed forever grows; it relies on the harness's own compaction. A lost
  conversation is reported (SUP-04), never silent.
- Prompt injection that persists as a routine: HB-02, HB-08, HB-09.
- A laptop that sleeps or a machine that is off cannot be woken: the message waits as today and the
  profile says "supervisor offline since …".

## Out of scope
Containers or remote runners (AD-039); moving an AI between machines or failover; schedules for a
labelled session; per-schedule model or harness; creating a new AI from the web; launchd/Windows
services (later); a Partyline-owned model loop (the next step of the own harness, not this one).

## Tests to write first
- Scheduler: `every 60` anchored at creation does not drift across 3 runs fired late; `0 9 * * *` in
  `America/Sao_Paulo` fires at 12:00 UTC; a server down from 08:50 to 11:00 fires one catch-up run
  (`once`) and none (`skip`); a run older than its window is `expired`.
- HB-03: 14-minute interval → 422; four hourly schedules (96/day) → 422 on the third.
- HB-02: an AI's PATCH of a manager's schedule → 403; its own → 200; a manager's PATCH of the AI's → 200.
- HB-04/HB-05: two live sessions, only the principal gets the frame; an unacked run is superseded,
  not stacked; a parallel session never receives one.
- HB-07: a principal on a client older than the gate → `client_outdated`, no frame written.
- HB-08: `trusted` is false when the setter no longer manages the AI at delivery time.
- HB-09: an AI's `set_schedule` → one DM to the nearest manager; a second edit within the hour → none;
  "Pausar" disables it.
- SUP-03: supervisor started with `CLAUDE_CODE_SESSION_ID`, `PARTYLINE_TAKEOVER=1`, `PARTYLINE_LIVE`
  in its env → none of them in the harness's env (the regression of the inherited takeover).
- SUP-06: a fake harness that exits at once 5 times → `crash_loop`, no sixth start, one report; an exit
  classified `needs_login` → no retry, not counted.
- SUP-07: a supervisor frame naming an AI it did not register, or an unknown type → ignored, no start.
- SUP-08: supervised connect while a hand principal is live → 409 `principal_live`, the hand session
  stays; SUP-09: hand `--principal` closes the supervised one 4002 → no restart for 2 minutes after the
  hand session leaves.
- SUP-10: launcher on the pinned id asks yield, waits for `yielded`, then execs; on a 504 it execs anyway.
- SUP-12: DM to an offline supervised AI → one `wake` within 1 s; three DMs in 10 s → still one; a
  paused AI → no start.
- SUP-15: a full run of the fake backend leaves no message text in the supervisor's log.
