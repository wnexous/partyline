# Clients that update themselves

Status: Specify + Design written 2026-11-11; the owner said "pode" (2026-11-11). Requirement ids AU-xx.
Owner request (2026-11-11): "it would be great if the partyline running in the harnesses did not need
updating all the time: it updates itself to what the server requires, without restarting the harness.
Today I have to stop the harness, update and run it again."

## Problem
Every release reaches the server at once, but each harness session keeps running the client it started
with. New tools and fixes only reach a session after the person stops the harness, runs
`partyline update` and starts it again, on every machine.

## Goal
A session's client follows the server's version by itself: it installs the server's release and starts
again on it inside the same harness session, which carries on with the new tools.

## Requirements
1. WHILE a session's client (`partyline channel`, full or tools-only, and `partyline listen`) runs a
   release version older than the server's (`GET /healthz`), it SHALL download the server's release,
   verify it against the release's checksums.txt as `partyline update` does, and replace its own
   executable. It checks one minute after it starts and every 15 minutes after that. (AU-01)
2. It SHALL never go to a version older than its own, nor to one the server does not run (it does not
   follow GitHub's latest release); a server or client whose version is not a release (`dev`, a build
   from git) updates nothing. (AU-02)
3. The sessions of one machine SHALL install one copy at a time: a session that finds the executable
   already at the server's version, or newer, installs nothing and only starts again on it. (AU-03)
4. `partyline channel` SHALL keep the harness's MCP connection across the change: it runs the channel
   in a child process and, once no request of the harness is waiting for an answer, hands the
   connection to a child of the new executable, replaying the harness's `initialize`, so the harness
   sees neither the restart nor a second handshake. Requests that arrive during the change wait and
   are answered by the new child. (AU-04)
5. WHEN the new child serves other tools than the old one THEN the harness SHALL get
   `notifications/tools/list_changed` (the initialize answer declares `tools.listChanged`). (AU-05)
6. The new child SHALL be the same session: it neither takes the AI over nor asks for the principal
   role again, and it does not repeat the identity notice. (AU-06)
7. `partyline listen` SHALL end with exit 0 once the new executable is in place, so the pi and OpenCode
   adapters start it again on it (they restart listen after one second). (AU-07)
8. `PARTYLINE_AUTO_UPDATE=0` SHALL turn all of it off; Windows, which cannot replace a running
   program, and an executable whose folder is not writable SHALL keep running as today, with one line
   on stderr saying why. (AU-08)
9. IF the new child cannot start, does not answer the replayed `initialize` within 30 seconds, or ends
   by itself within a minute of the handover THEN the supervisor SHALL fall back to the old executable
   where the system still has it (`/proc/self/exe`), answering the calls the dead child left with an
   error, and otherwise end as the channel ends today. An old child that does not end within 10
   seconds of its input closing is killed. (AU-09)
10. The first child SHALL run the program the session started with (`/proc/self/exe` where it exists),
    so a session that never updates behaves as before whatever the file on disk holds; every child of
    one supervisor connects with the same live id, so the server sees one process replacing its socket
    (SESS-14) and the session keeps its label, start time and role; a client on plain releases follows
    no release candidate. (AU-10)
11. `partyline doctor` SHALL say whether this machine's sessions update themselves: on, off because
    `PARTYLINE_AUTO_UPDATE=0`, or a warning with the reason when they cannot (Windows, or the
    executable's folder takes no new file, with how to install where it can). A build that is not a
    release gets no line. A harness shows the session's stderr to nobody, so this is where the person
    sees it. (AU-11)

## Contract the supervisor freezes
The supervisor stays at the version the harness started for the whole harness session, so every later
version must keep: `partyline channel <same args>` as the child's command, `PARTYLINE_SUPERVISED`,
`PARTYLINE_RESUMED`, `PARTYLINE_LINK_LIVE`, newline-delimited JSON-RPC on stdio, and a child that never
sends requests to the harness (the supervisor tracks only the harness's requests).

## Out of scope
- The TypeScript adapters of pi, OpenCode and omp themselves: a change to them still needs a restart
  of the harness (they changed twice in the 16 releases before this one).
- Instructions sent in the first `initialize` answer: the harness keeps the first ones.
- A config file key for the opt-out; the variable is enough until someone asks.

## Design
- `internal/cli/autoupdate.go`: `autoUpdater` polls `<instance>/healthz`, decides with `releaseIsNewer`
  (only when the running version is a plain release), takes an exclusive lock
  (`<user cache>/partyline/update.lock`), reads the version of the executable on disk
  (`<exe> version`, 10 s) and installs the server's tag with the existing `updater.install` when the
  disk copy is older. It reports the version now on disk.
- `internal/cli/supervise.go`: `partyline channel` without `PARTYLINE_SUPERVISED=1` (and with auto
  update on, on a release build, not Windows) starts `<exe> channel <same args>` with
  `PARTYLINE_SUPERVISED=1` and pipes the lines both ways. It keeps the harness's `initialize` and
  `notifications/initialized` lines, the ids of the harness requests still waiting for an answer and
  the last `tools/list` answer. On an update it holds the harness's lines, closes the child's stdin
  once nothing waits, forwards what the child still writes, starts the new child with
  `PARTYLINE_RESUMED=1` (no takeover, no principal, no identity notice), replays `initialize` and
  `notifications/initialized` under its own ids, lists the tools, sends `tools/list_changed` when they
  differ, then passes the held lines on.
- `partyline listen` runs the same `autoUpdater` and returns 0 when it reports a newer executable.

## Tests
`internal/cli/supervise_test.go` (handover with fake children that are real MCP servers: held requests,
replayed initialize, list_changed, string ids, unexpected child exit, harness EOF, fallback on a start
failure, a handshake timeout and a crash right after the handover, an old child that does not end),
`internal/cli/autoupdate_test.go` (decisions: older/newer/dev/not a release, disk already updated, a
local build on disk, unwritable, opt-out, Windows, the listen restart, the doctor line). Checked by hand with two real
builds (v0.30.3 → v0.30.9) under a scripted MCP client: the harness kept its connection, the new child
ran the new build with the harness's pid and answered ping and tools/list. Against a live server: a v0.30.9-labelled
client and a local v0.31.0 instance — it downloaded the real v0.31.0 release, swapped its child and
kept answering tool calls every 5 seconds through the change.
