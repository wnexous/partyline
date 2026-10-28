# AI Sessions Design

**Feature**: `ai-sessions` · **Spec**: [spec.md](spec.md) · **Tasks**: [tasks.md](tasks.md)
**Status**: Revised for approval (D11 and the worktree tools-only reader, 2026-10-27) · **Base**: `main` at `a1f6544`

## Answers to the brief's design questions

| Question | Answer |
| --- | --- |
| Where the single live session is enforced today | `agents.Presence` keeps one entry per AI (`sessions map[string]presenceEntry`, `internal/agents/presence.go:66-75`), refuses a second attach with `ErrBusy` (`:90-117`) and exposes `Busy` (`:120-128`); the gateway checks it before the upgrade and answers 409 `identity_busy` (`internal/agents/gateway.go:222-228`), and `Registry.Attach` is the only attach path (`internal/agents/folder.go:457-471`). The launcher prompts on `GET /agent/session` (`internal/cli/launcher.go:291-318`), and the channel's busy state, texts and retry loop live in `internal/channel/link.go:286-317, 450-458, 486-508`. |
| How it becomes per (AI, label) | `Presence` holds a map of labels per AI, allocates the label under the same mutex, publishes `Live` lists, and `Kick` ends every session. A second attach with a free preferred label becomes it; otherwise it becomes `<harness>-<n>`. `--take-over` acts on the principal label only. → [Sessions](#sessions) |
| Where D3's routing happens | The dispatcher computes a target label per receiving AI with `delivery.SessionLabel(text, handle)` and calls `Gateway.Push(agentID, target, msg)`. The gateway resolves the label against presence, records an `agent_session_deliveries` row and pushes there, or pushes to the principal with `for_label` set. Backlog and pending read the principal's cursor plus the fallen-back rows. → [Addressing](#addressing) |
| The delivery queue and backlog per session | There is still one AI cursor (`agent_cursors`, `0001_init.sql:161`). A labelled session has no cursor and no backlog: it receives live labelled messages only, and anything of another author's that it did not acknowledge falls back to the principal; a row of the AI's own message is deleted instead (SESS-38). A row in `agent_session_deliveries` is the only per-session state. → [The delivery rows](#the-delivery-rows) |
| Where a session addresses another session of its own AI (D11) | `message_views.author_label` (migration 0027) names the writing session. `AgentDispatcher.audienceOf` adds one target for the author AI, which `Plan` still excludes; `Gateway.Push` recognizes a message the AI wrote, pushes it to the live label and never falls it back; `DeleteSelfSessionDeliveries` drops its row when that session ends. → [Sessions addressing sessions](#sessions-addressing-sessions) |
| The mention parser and the web code | `delivery.SessionLabel` (new, pure) parses `@handle:label` with the same word-boundary rules as `delivery.Wants`/`containsBounded` (`internal/delivery/wants.go:69-134`). The web autocomplete (`web/src/components/mention-suggestions.tsx:72-110`, `web/src/components/composer.tsx:267-283, 540`) offers one entry per live label and inserts `@handle:label ` as one token. |
| The live frames and the channel | The welcome frame gains `session {label, harness, connected_at}` (`dto.AgentWelcomeFrame`, `internal/api/dto/agents.go:149-171`); a fallback frame gains `for_label` on `dto.AgentMessage` (`:60-64`). The `identity_busy` refusal and the busy error are removed from the server (the channel keeps its busy handling only for an older server); the channel prints one line and tells the harness once which session it is. → [Frames](#frames-and-the-channel) |
| Migration 0027 | `messages.author_label` (D6), the `agent_session_deliveries` table (the per-session delivery state and the fallback queue), and the `message_views` recreation that exposes `author_label`. Pure SQL, additive, no hook (AD-012). → [Data model](#data-model) |
| Web | "via :label" next to the author (the `messages.author_label` column → `dto.Message.author_label`), a live-session list in the AI profile, `@handle:label` in the autocomplete, and an extended 320/412 px fit check. → [Web](#web) |
| The riskiest part | The fallback/ack race and the principal's cursor: a labelled message is only ever owned by one place, because the fallback is an atomic `UPDATE ... RETURNING` and a labelled ack deletes a row only while `fell_back_at IS NULL`; a message the AI wrote to its own session is deleted at session end instead of falling back. → [Risks](#risks--concerns) |
| Every new/changed route | No new HTTP route. Two headers (`X-Partyline-MainFolder`, and `X-Partyline-Live` on tool calls), additive DTO fields, and the welcome frame's `session`. → [Protocol and API](#protocol-and-api) |

## Overview

```
CLI (host)                                    server
────────────────────────────────────────────  ───────────────────────────────────────────────
partyline claude in /repo/wt
  git rev-parse --git-common-dir vs --git-dir
  → PARTYLINE_MAIN_FOLDER=/repo
  sessionIdentity → X-Partyline-MainFolder
        │
        ▼
GET /agent/ws (folder=/repo/wt, main=/repo) ──► Registry.ResolveFolder
                                                  key = (owner, machine, /repo)      D1
                                                  AI.folder = /repo
                                                  preferred label = "wt"
                                                  Presence.AttachLabeled            D2/D4
                                                  ┌ principal "" live? ──► "wt" free? ──► "wt"
                                                  └ else "claude-2", "claude-3", …   (one lock)
        ◄── welcome {session:{label:"wt", …}}
                                                  publish agent.presence (D7)
```

```
message posted in a group
        │
        ▼
events.MessageCreated → delivery.AgentDispatcher
        │ per receiving AI: wanted? ── no ──► stored, not pushed (MEM-03/04)
        │ target = SessionLabel(text, handle_atlas)         D3
        ▼
Gateway.Push(agentID, target, msg)
        ├ target ""  ──────────────► principal session (cursor + live push)
        ├ target "wt", live ──────► agent_session_deliveries row + push to :wt
        ├ target "wt", not live ──► principal live/queue, frame.for_label="wt"
        ├ :wt ends before ack ────► FallBackSessionDeliveries RETURNING → principal + note
        ├ author is this AI, live ─► row + push to the sibling session, trusted      D11
        └ author is this AI, dead ─► nothing; the row is deleted at session end      D11
```

## Identity: the repository (D1)

### Detection on the CLI

A new pure helper in `internal/channel` (`worktree.go`, new) decides whether the session folder is a
linked worktree and returns the main worktree folder:

1. `git -C F rev-parse --git-common-dir` and `git -C F rev-parse --git-dir`, with a `context.WithTimeout`
   of one second for the pair and `cmd.Dir = F`;
2. the two outputs are made absolute against `F` (git prints the common dir relative to the worktree in
   some layouts) and compared after `filepath.Clean`; equal → **not a linked worktree**, return `""`;
3. different → the main worktree is the first `worktree <path>` line of
   `git -C F worktree list --porcelain`; a first block with `bare` (no `worktree` line) means no main
   folder (a bare repository has no working tree to be the identity); when the list fails, the parent of
   the common dir is used only if its base name is `.git`, otherwise no main folder;
4. the result is canonicalized with `channel.CanonicalFolder` (absolute, symlinks resolved, no trailing
   separator), like every folder the CLI sends;
5. any error, a missing `git`, a timeout or an unreadable output → `""` (the session starts; it resolves
   by its folder as today). Nothing is logged with the path.

The helper takes an injectable `run func(dir string, args ...string) ([]byte, error)` (the pattern of
`execLookPath` in `internal/cli/launcher.go:21`) so the unit tests do not need a real git; an integration
test runs `git worktree add` when `git` is on `PATH` and skips otherwise.

The folder the session reports does **not** change: `PARTYLINE_FOLDER` and `X-Partyline-Folder` stay the
worktree (`internal/cli/channel.go:53-70`, `internal/channel/identity.go:20-42`). The main folder travels
beside it.

### The header, the launcher and the tools-only process

- `channel.Identity` gains `MainFolder string`; `setHeaders` sends it percent-encoded (`url.PathEscape`)
  as `X-Partyline-MainFolder` (`dto.HeaderMainFolder`, new constant in `internal/api/dto/common.go`),
  only when non-empty, exactly like `HeaderFolder` (`internal/channel/identity.go:45-60`).
- The launcher computes it once per launch (`launch`, `internal/cli/launcher.go:178-270`) and puts it in
  the child's environment as `PARTYLINE_MAIN_FOLDER`, next to `PARTYLINE_FOLDER`; the variable joins
  `launcherVars`/`consumedVars` (`internal/cli/procenv.go:22-36`, `launcher.go:276-282`) so
  `withLauncherEnv` hands it to a tools-only process whose harness strips the environment.
- `sessionIdentity` (`internal/cli/channel.go:92-115`) reads `PARTYLINE_MAIN_FOLDER`; when it is empty and
  the session has no `PARTYLINE_LAUNCH_PARENT`, it computes it once with the helper (a plain
  `partyline listen` or a `channel --tools-only` started by hand); a child session never computes it, so
  `--parent` keeps its own AI (D9, below).
- `launch` sets the variable only when the launch asked for no parent: `place := placementOf(...)`
  (`launcher.go:136-149`); when `place.parent != ""`, the environment carries no
  `PARTYLINE_MAIN_FOLDER`. The AI the child creates is the AI of its own folder, and the parent link is
  recorded as today (`internal/agents/placement.go`). This is D9 made concrete.
- `partyline channel --tools-only` (the MCP server Codex and Gemini start) already sends the identity
  headers on every call (`internal/channel/identity.go` via `client.do`,
  `internal/channel/client.go:33-44`), so it needs no extra work: it resolves by D1 and acts as the
  folder's session (D10). Its reads are empty when its folder resolves to a label: see
  [The principal's queue and the tools-only reader](#the-principals-queue-and-the-tools-only-reader).

### The server: resolution by the main folder

`FolderID` (`internal/agents/folder.go:37-64`) gains `MainFolder string`; `FolderIDFromHeader` reads and
unescapes `X-Partyline-MainFolder` after the folder, and a value that fails `url.PathUnescape` is 422
`folder_invalid` on `main_folder`, like a bad folder. `ValidateFolder` is applied to it (SESS-04);
`main_folder == folder` is normalized to `""`.

`ResolveFolder` and `LookupFolder` (`internal/agents/folder.go:214-270`) then use one key:

```go
key := id.Folder
if id.MainFolder != "" {
    key = id.MainFolder
}
```

and everything that used `id.Folder` for identity keeps using `key`: `GetAgentByFolder`, `linkOrCreate`
(including `adoptionCandidate`/`createFolderAgent`, which name the AI from `folderBase(key)` and store
`folder = key`), and the `--org`/`--position` placement, which is unchanged. The session's own folder is
not lost: `handshake` keeps the `FolderID`, so the gateway can derive the preferred label from it (below)
and a tools-only process can derive its author label. `agents.folder` for a worktree session is therefore
the main folder (SESS-05), and the profile's folder label (`intelligences.FolderLabel`) shows it.

`Registry.RegisterFolder` (`folder.go:243-249`) follows the same path, so `GET /agent/register` and
`partyline channel --tools-only` resolve the same AI as the socket.

The AI is created by the first session that sees the repository, whether it runs in the main worktree or
in a linked worktree: the two sessions race in `linkOrCreate` under `allocMu` and the unique index
`agents_folder` (`0015_intelligences.sql:24-25`), so both end with the same row. The AI's home
organization and creator come from that first session's placement, as today; a later `--position` can
still move it (`internal/agents/folder.go:199-213`).

### Upgrade, old worktree AIs and `--parent`

- **Old worktree AIs stay.** Nothing migrates: an AI created before this feature with
  `agents.folder = /repo/wt` is still a valid AI, still listed, still in its rooms. The new CLI never
  resolves it again (it sends `/repo/wt` as the folder and `/repo` as the main folder, and the key is
  `/repo`), so it goes quiet. The owner merges it into the repository's AI with `POST /agents/{id}/merge`
  (`docs/api.md:170`), which requires it to be offline (`identity_online` 409): end its sessions first,
  then merge. Documented in `docs/agents.md` and `README.md` (SESS-08).
- **Mixed versions.** A server with the new migration accepts both: an old CLI sends no main folder and
  resolves/creates by its folder, exactly as today (`SESS-02`). A new CLI against an old server sends a
  header the old server ignores; the worktree creates an AI as before. No version negotiation is added.
- **Moved main worktree.** The identity is the path. Renaming or moving the main worktree creates a new
  AI on the next session; the old one is merged by hand, like any moved folder (AID-01's existing rule).
- **Main worktree missing.** `git worktree list` prints a prunable main; the CLI still sends it. The
  server validates only the path shape, so the AI lives at a path that no longer exists on disk and the
  next session reconnects to it when it reappears. This is the "folder is the identity" rule, not a new
  failure mode.
- **`--parent`/`PARTYLINE_PARENT`** (D9): no main folder is sent, so a child started in a worktree gets
  its own AI and its own session with label `""`. The record of who started it is unchanged.

## Sessions

### Presence today and the new model

`Presence` (`internal/agents/presence.go`) today has one `presenceEntry` per AI: the holder's socket, its
process id, its owner and the start time. It publishes `events.AgentPresence` on offline → online and
online → offline, and stamps `agents.last_seen_at` (`touch`). `Attach` checks and swaps under one mutex so
racing connects end with exactly one live session; `Busy` is the pre-upgrade check; `Kick`/`Drop` end the
session.

The new model keeps every guarantee and moves the key one level down:

```go
type Live struct { // the old struct, plus what a session is
    Label, Harness string
    Since          time.Time
}
type presenceEntry struct {
    ownerID string
    session Session
    label   string // "" for the principal
    harness string
    liveID  string
    since   time.Time
}
type Presence struct {
    ...
    sessions map[string]map[string]presenceEntry // AI id → label → entry
    retired  map[string]map[string]retiredLabel  // AI id → live id → label + expiry
}
```

- `Online(agentID)` stays true while the inner map is non-empty (D7).
- `Main(agentID)` returns the `""` entry; `Live(agentID)` keeps its signature and returns the principal's
  `Live` for `GET /agent/session` and the profile's `connected_at` (`internal/api/agent_handlers.go:93-131`,
  `internal/agents/folder.go:473`).
- `Sessions(agentID) []Live` returns the principal first, then labels in lexical order; it is what the
  profile and the members list read (SESS-32, SESS-33).
- `SessionByLabel(agentID, label)` is the routing lookup.
- `Detach` removes the connection's entry; when the inner map empties the AI is offline and one
  `events.AgentPresence{Online:false}` goes out, so the online/offline transitions keep working for
  everything that already listens (`internal/live/hub.go:406-410`). The event is also published on a
  session change while the AI stays online (SESS-32's live list, see [Presence surfaces](#presence-surfaces)).
- `Kick` collects every label under the lock and closes each socket with the same code and reason; the
  AI is offline when the map empties (SESS-19). `Drop` (the delete path, `internal/agents/registry.go:491`,
  `presence.go:186-206`) does the same without a write.
- `Busy` and `ErrBusy` disappear with the refusal; `CloseBusy`/`ReasonBusy`
  (`presence.go:25-35`) go too. The `dto.ErrorCodeIdentityBusy` constant and the channel's handler stay
  for an older server (design default in the spec).

### Labels

`internal/agents/labels.go` (new, pure) owns the rules:

- `LabelForFolder(folder string) string`: `folderBase` of the folder (the existing helper,
  `internal/agents/folder.go:143-150`), lowercased, every run of characters outside `[a-z0-9]` collapsed
  to one `-`, trimmed of `-`, cut to `dto.MaxSessionLabel = 40` and trimmed of a trailing `-`; an empty
  result is `session`. Examples: `/repo/wt` → `wt`; `/repo/Acme API!` → `acme-api`; `/repo/___` →
  `session`; `/repo/claude-2` → `claude-2`.
- `AllocateLabel(preferred, harness string, taken func(string) bool, remembered string) string`:
  1. `remembered` when not empty and free (the reconnect case);
  2. `preferred` when free (the principal `""` is a preferred label like any other);
  3. otherwise `<harness>-<n>`, `n = 2, 3, …`, the first free one; `harness` is cleaned by
     `cleanHarness` (`folder.go:412-422`) and `agent` when empty. The base is trimmed so the label stays
     at most 40 characters.
- `dto.SessionLabelPattern = "^[a-z0-9-]{1,40}$"` and `dto.MaxSessionLabel = 40` (kernel constants, used
  by the dispatcher's parser too). The label is not stored in a table; the label shape is the only
  contract.

`AttachLabeled` (`presence.go`) is the only allocation point and runs entirely under `p.mu`, so labels are
unique among one AI's live sessions and two racing connections cannot both take `""` (SESS-12):

```
same process (liveID non-empty and live) → replace that socket (1000 reconnected), keep label/since
takeover and preferred == "" and principal live → close the old principal (4002 taken_over), attach ""
otherwise → label = AllocateLabel(preferred, harness, taken(live), remembered(liveID)); attach fresh
```

`--take-over` for a worktree or a parallel connection is ignored: its preferred label is not `""`, so the
branch above never applies (SESS-13; D4). The principal label is the only one a takeover can replace.
`attached` returns the assigned `Live`, `fresh` (a new session vs. a reconnect) and no error: nothing
refuses a connection any more.

### Reconnect stickiness and the 10-minute memory

A process has one `X-Partyline-Live` for its lifetime (`channel.NewLiveID`,
`internal/channel/identity.go:100-111`; `internal/channel/link.go:196-199`). On `AttachLabeled`:

- if the AI already has an entry with that liveID, the new socket replaces it and keeps the label, the
  harness and `since` — this covers a harness restarting its socket and the `reconnected` path that
  exists today;
- if the entry is gone (the server dropped a silent socket with the 25 s ping / 10 s write timeout,
  SES-06), `retired[agentID][liveID] = {label, now + 10 min}` was written by `Detach`; the reconnect gets
  the remembered label when it is free, so `@atlas:claude-2` keeps meaning the same process.

The memory is pruned on every attach/detach of the AI, is never persisted, and expires after ten
minutes (SESS-14). A process that was a parallel `claude-2` and reconnects while another session took
`claude-2` gets the next free label; the person sees the new label in the notice. A server restart loses
every session and the memory; each process reconnects as a new session and the first parallel one gets
`claude-2` again if it is free.

### What a session is and is not

A session is a connection, not an identity (D5, SESS-17):

- no row in `agents` or `intelligences`; no profile, title, handle, avatar, bio, organization, position,
  membership, trust, token or rate limit of its own. The AI's post limiter (`Registry.AllowPost`) is
  shared by its sessions, as today.
- a session's harness is in presence only (for the profile list); `agents.harness`, `agents.connected_at`
  and the AI's `app` follow the **principal**: `Registry.Attach` sets them only when the assigned label
  is `""` (`internal/agents/folder.go:457-471`), so a parallel `pi` session does not relabel a Claude Code
  AI (SESS-17). The one exception is creation: a new AI's `agents.harness` is the harness that created it
  (`InsertFolderAgent`), even when the first session is a worktree one; from then on only the principal
  moves it.
- every session receives the AI's welcome: organization, position, managers, operated connections
  (`welcomeRefs`, `internal/agents/gateway.go:385-420`). The connections the AI operates are listed to
  every session, but their platform events go to the principal only (`c.feed` is built only when the
  assigned label is `""`; SESS-18). A labelled session that opted into `FeaturePlatform` is told nothing
  about it: its feed stays nil, and `GET /agent/pending?platform=1` still takes items for the AI, which
  only the principal does.
- ending the AI's session from the profile (`Registry.EndSession`, `internal/agents/lifecycle.go:76-120`,
  `presence.Kick`) closes every session with `4002` and `ended by @handle`; the dialog says that it ends
  all of them (SESS-19, design default).

### Frames and the channel

- The welcome frame (`dto.AgentWelcomeFrame`) gains
  `Session dto.SessionInfo {Label, Harness, ConnectedAt}` (`internal/api/dto/agents.go:149`); the gateway
  writes the assigned session into it (`Gateway.run`, `gateway.go:520-588`). An older channel ignores the
  field.
- `dto.AgentMessage` gains `ForLabel string json:"for_label,omitempty"` (`agents.go:60-64`). It is set
  only on a frame that fell back to the principal; the channel renders it as a meta key `for_label` and
  one lead line, `(addressed to :<label>)`, before the message content (`internal/channel/format.go:32-78`),
  so the model is told why the message is in this session (SESS-22).
- The identity notice (`identityText`, `internal/channel/link.go:332-380`) gains one sentence for a
  labelled session: `This session is :<label>.` The first welcome prints
  `partyline: opened as parallel session <label>` to stderr once (`announceIdentity`/`run`,
  `link.go:402-424`); the person sees the line the SES-01 prompt used to ask about (SESS-15). The notice
  and the line are skipped for the principal.
- **The busy path is removed server-side**: no `identity_busy`, no `Presence.Busy`, no `CloseBusy`.
  `GET /agent/session` keeps answering `Online`, `App`, `ConnectedAt` from the principal
  (`internal/api/agent_handlers.go:93-131`); its `SessionState` gains `Sessions []SessionInfo` so the
  launcher's tools-only line can say how many sessions the AI has (SESS-19). The channel keeps its
  `outBusy` branch and texts (`link.go:286-317, 486-508`) as a compatibility path for an older server;
  a new server never sends it. `--take-over` still closes the old principal with `4002 taken_over`, and
  the old channel still tells its harness `takenOverText` (SES-05, unchanged).
- The ping loop is per connection and unchanged (`Gateway.run`'s ticker, `gateway.go:555-575`): each
  session has its own 25 s ping and 10 s write timeout, so a silent session of any label is dropped within
  35 s and its label freed then (SESS-19).

### The launcher

- `askTakeover`, `takeoverPrompt`, `takeoverChoice` and the prompt wiring go
  (`internal/cli/launcher.go:288-330`); `launch` sets `takingOver = opts.takeOver ||
  PARTYLINE_TAKEOVER=1` and starts the harness (SESS-16). Without a terminal there is nothing to skip:
  the behaviour is the same as with one.
- The rest of `lookupSession` stays: a launch that asks for a placement still gets the server's refusal
  before the harness starts (`launcher.go:184-196, 383-430`), and Codex/Gemini still print the
  one informational "also open in another session" line (`announceToolsOnly`, `launcher.go:333-343`).
  `sessionName`/`openWhere` stay for that line.
- `--take-over`/`PARTYLINE_TAKEOVER=1` keeps its meaning: the session's first connection asks the server
  to replace the principal (SES-02). A worktree launch with `--take-over` is accepted and the flag is
  ignored by the server, because its label is not the principal one.

### Ending a session and the 35-second window

A socket that closes frees its label within 5 s (the read loop returns, `Detach` runs). A socket that goes
silent without closing keeps its label until the server's ping fails: at most 35 s (25 s tick + 10 s
write timeout, `DefaultPingInterval`/`writeTimeout`, `internal/agents/gateway.go:31-38`). During that
window another connection in the same folder gets a parallel label, not the silent one's, which is
correct: the zombie may still answer. A message pushed to the zombie is unacknowledged when it is
finally dropped and falls back to the principal at that moment (SESS-24), not before. The
end-of-session sweep runs only when `Presence.Detach` reports that the connection was still the current
session: a socket replaced by the same process's reconnect (`reconnected`) does not sweep, so it cannot
steal the messages the new socket is about to receive.

## Addressing

### The parser

`internal/delivery/session.go` (new, pure):

```go
// SessionLabel returns the session label a message's text addresses to the AI with handle,
// or "" when it addresses the AI without a label. The first valid address wins.
func SessionLabel(text, handle string) string
```

Rules (SESS-21):

- an empty handle never matches; the handle is matched case-insensitively with `containsBounded`'s left
  boundary (no letter, digit or `_` immediately before the `@`; `internal/delivery/wants.go:102-127`);
- the `@handle` must be immediately followed by `:` and then a label matching
  `dto.SessionLabelPattern` (1–40 of `a-z0-9-`); `@atlas-2:x` does not address `atlas` (the existing
  `continuesHandle` rule, `wants.go:129-134`), and `@atlas:CLAUDE-2` is no address (the label is
  lowercase);
- a `:` followed by an invalid or over-long label is not an address; the scan continues, so
  `@atlas:Bad @atlas:wt` addresses `wt`;
- a message that addresses the AI twice keeps the first label.

`Wants` itself is not changed: `@handle:label` already counts as an `@handle` mention today (the `:`
ends the handle), so the member's delivery mode still sees it. D3 only decides **which session** gets a
message the AI already wants (SESS-26). The loop guard (`rooms.agents_paused`, checked in
`AgentDispatcher.audienceOf`, `internal/delivery/dispatcher.go:112-146`) still suppresses every session.

### Where the routing happens

`AgentDispatcher.dispatch` (`internal/delivery/dispatcher.go:150-201`) already computes the receiving
AIs, their `trusted` flag and `wanted`; one line is added per AI:

```go
target := delivery.SessionLabel(a.view.Text, profile.Handle)
d.o.Agents.Push(agentID, target, dto.AgentMessage{Room: refs.of(agentID), Trusted: trusted, Message: msg})
```

`dispatchUpdate` (`:205-252`) computes the target of the text **before** the change (the edit's
`PrevText`, the delete's current text) and of the text **after** it, dedupes them and calls
`PushUpdate(agentID, target, frame, msg)` so an edit that adds a label reaches the newly addressed
session and a delete reaches the session that had the message (SESS-27). `AgentPusher`
(`dispatcher.go:16-25`) gains the target argument:

```go
Push(agentID, target string, m dto.AgentMessage) bool
PushUpdate(agentID, target string, t dto.AgentFrameType, m dto.AgentMessage) bool
```

The gateway (`internal/agents/gateway.go:152-177`) keeps the routing decision next to the presence it
needs:

```
Push(agentID, target, m):
  author is this AI             → target live? CreateSessionDelivery(fell_back_at=NULL),
                                  push to that session (a failed push closes the socket and
                                  its sweep deletes the row)                              [D11]
                                  dead target → false, no row, no principal push          [D11]
  target == ""                  → push to the principal session (if live)                 [D3: main]
  target != "", session live    → CreateSessionDelivery(message, AI, target, fell_back_at=NULL)
                                  push to that session; a failed push closes the socket
                                  (conn.enqueue), and its end-of-session sweep falls the
                                  row back and hands it to the principal                   [D3: label]
  target != "", no such session → CreateSessionDelivery(message, AI, target, fell_back_at=now)
                                  m.ForLabel = target                                        [D3: fallback]
  "principal"                   → push to the principal when live; else leave the row for
                                  the principal's next backlog/pending
```

A row is created for **every** message of another author addressed to a label, so the note survives even
when the principal is not live at that moment: the row is born fallen back and `ListSessionFallbacks`
delivers it with `for_label`. A message the AI wrote gets a row only when its sibling is live, and the row
is never born fallen back (D11, below). `PushUpdate(agentID, target, t, m)` follows the same target: a
live label receives the edit or the delete; a target with no live session is pushed to the principal,
because the principal may hold the message as a fallback (or is the only reader left), so an edit after a
session end is not lost — except for a message the AI wrote, where a dead target gets nothing (the
gateway's sibling rule, D11).

`Push` returning false means "nobody received it live": the row (or, for a plain message,
the cursor) keeps it and the principal's backlog or `GET /agent/pending` delivers it later (SESS-23). The
dispatcher only calls `Push` for an AI that is online (any session live); an AI with no live session at
all keeps the message in its cursor queue as today, so a `@handle:label` message for a completely
offline AI is delivered without the note but with the address in its text. A labelled session never receives a message the AI did
not want: the dispatcher only calls `Push` after `WantedBy` returned true.

### Sessions addressing sessions

D11 needs one recipient the plan deliberately excludes: the message's author AI (AD-009). `Plan` and its
exclusion stay as they are; one target is added next to them, for the author AI only:

- **Who wrote it.** The stored label is `message_views.author_label` (`a.view.AuthorLabel`, migration
  0027), set at post time by the authorship path (T9); the text is never parsed to decide who wrote a
  message (SESS-29).
- **Who it addresses.** `audienceOf` loads the author AI's handle (`IntelligenceOf`) when the author kind
  is `agent` and parses `delivery.SessionLabel(a.view.Text, handle)` with SESS-21's parser. When the
  result is non-empty and differs from the stored author label, `audience.self` carries
  `{agentID: a.view.AuthorID, label: target}` and `audienceOf` reports an audience even when no other AI
  is online.
- **Delivery.** `dispatch` pushes that frame after the normal loop:
  `d.o.Agents.Push(authorID, target, dto.AgentMessage{Room: refs.of(authorID), Trusted: true, Message: msg})`.
  The membership's delivery mode is not applied: it decides what the AI wants from others, and here the
  author is the AI itself (SESS-26 still governs every other author). The loop guard is not bypassed:
  `audienceOf` returns no audience for a paused room, so a paused room delivers nothing to any session.
  `Trusted` is true because the AI wrote it; AD-018's manager rule decides the trust of other authors
  (spec assumption).
- **The gateway never hands it back.** `Gateway.Push` recognizes a message the AI wrote
  (`m.Message.AuthorKind == agent && m.Message.AuthorID == agentID`): a live target gets a pending
  `agent_session_deliveries` row and the frame; a target with no live session gets `false`, no row and no
  push to the principal (SESS-38). `PushUpdate` uses the same recognition, so an edit or a delete stays
  with the addressed sibling and never reaches the principal.
- **The end of the session.** `FallBackSessionDeliveries` excludes rows whose message the same AI wrote
  (its `NOT EXISTS` clause), and the sweep calls `DeleteSelfSessionDeliveries(agentID, label)` for them,
  so such a row can never become the principal's (SESS-38). `AckSessionDelivery` is unchanged: a live
  sibling's acknowledgement deletes its row like any other row.
- **Edits and deletes.** `dispatchUpdate` computes the self target from `e.PrevText` and from the text
  after the change (`a.view.Text`), dedupes them and pushes to each live one (SESS-39); a target with no
  live session gets nothing. A delete of an AI message addressed to a live sibling reaches it as the
  usual `message_deleted` frame; a delete whose old text named no label reaches no session of the AI
  (SESS-38, which scopes SESS-27's principal rule to messages of other authors).

Examples, all in one room with the AI `atlas` live as `principal` and `:wt`:

| Author | Text | Delivered to |
| --- | --- | --- |
| `principal` (author label `""`) | `@atlas:wt do X` | `:wt` once, with `trusted: true` |
| `:wt` | `@atlas do Y` | no session of `atlas` (the room's other AIs still receive it) |
| `:wt` | `@atlas:wt do Z` | no session of `atlas` |
| `:wt` | `@atlas:claude-2 do W`, `claude-2` not live | no session of `atlas`, and no fallback to `principal` |
| a user | `@atlas:wt do X` | `:wt` (D3's normal path, delivery mode included) |

A tools-only process in a worktree is the folder's session for authorship (D10), so its
`@atlas:claude-2` reaches the sibling too, with its worktree label as the author label.

### The delivery rows

`agent_session_deliveries` is the only per-session state and exists so that a labelled push can be owned
exactly once:

```sql
CREATE TABLE agent_session_deliveries (
    message_id    TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    agent_id      TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    label         TEXT NOT NULL CHECK (label <> ''),
    created_at    INTEGER NOT NULL,
    fell_back_at  INTEGER,
    PRIMARY KEY (message_id, agent_id)
);
```

Lifecycle:

| Event | SQL | Result |
| --- | --- | --- |
| A message is pushed to a live label | `CreateSessionDelivery(..., fell_back_at NULL)` (`ON CONFLICT DO NOTHING`) | one row, pending; the message is that label's until it ends or acks |
| A message is addressed to a label that is not live | `CreateSessionDelivery(..., fell_back_at = now)` | one row, already the principal's: it is pushed with `for_label` when the principal is live, else it waits in the fallback list |
| A message the AI's own session wrote is pushed to its live label | `CreateSessionDelivery(..., fell_back_at NULL)` | one row; `Gateway.Push` never creates it fallen back (SESS-38), and a live sibling acks it like any row |
| The labelled session acks it | `AckSessionDelivery ... WHERE message_id = ? AND agent_id = ? AND fell_back_at IS NULL` | row gone; the AI cursor does **not** move (the principal never saw it) |
| The labelled session ends | `FallBackSessionDeliveries ... WHERE agent_id = ? AND label = ? AND fell_back_at IS NULL AND NOT EXISTS (the message's author is this AI) RETURNING message_id` | the rows of other authors become the principal's; the gateway pushes each to the principal when it is live, with `for_label` |
| The labelled session ends with a row of the AI's own message | `DeleteSelfSessionDeliveries(agent_id, label)` | row gone; it is never the principal's (SESS-38) |
| The principal acks it | `DeleteSessionDelivery` (no condition) + `AdvanceAgentCursor` | row gone; the principal's cursor moves as today |
| A tools-only process in the AI's own folder takes it | `DeleteSessionDelivery` (no condition) | same as an ack; the principal's cursor does not move backwards (a worktree tools-only process reads nothing, below) |
| The message is deleted | FK cascade | row gone |
| The AI is deleted | FK cascade | row gone |

The ack/fallback race has one winner: `AckSessionDelivery` only deletes a row that has not fallen back,
and `FallBackSessionDeliveries` only marks a row that is still pending, returning the ids it took. A late
ack of a fallen-back message is ignored instead of consuming it twice (SESS-24). `CreateSessionDelivery`
is idempotent, so a retried push cannot duplicate a row.

A labelled session has **no cursor and no backlog**: rows are only created while it is live, and whatever
it did not acknowledge is the principal's. This is D3's fallback read literally, keeps one cursor per AI
(the existing `agent_cursors`), and avoids a second cursor table that AD-012 would have to rebuild into
`agent_cursors`. A message addressed to `:wt` while `:wt` is not live never waits for a future `:wt`
(decided: it goes to the principal with the note), and a message the AI wrote to its own session is
deleted instead of becoming the principal's, because the AI must not receive its own text (SESS-38).

### The principal's queue and the tools-only reader

The principal's queue is the AI's existing cursor plus the fallen-back rows. Three reads change:

- `CountAgentBacklog`, `ListAgentBacklog`, `ListAgentPending`
  (`internal/store/queries/agents.sql:85-105`) gain
  `AND NOT EXISTS (SELECT 1 FROM agent_session_deliveries d WHERE d.message_id = <row>.id AND d.agent_id = sqlc.arg(agent_id))`,
  written against the query's own table (`messages` in `CountAgentBacklog`, `message_views` in the two list
  queries): a message that belongs to a live label, or that already fell back, is not read twice.
- `ListSessionFallbacks(agent_id, room_id)` returns the `message_views` of the rows with
  `fell_back_at IS NOT NULL` for that AI and room, by `seq`.
- `Gateway.backlog` (`gateway.go:589-715`) merges the cursor list and the fallback list by `seq` per room,
  writes the fallback messages with `ForLabel` set, and keeps `sent[room]` at the highest `seq` written, so
  a live push below it is still suppressed. A fallback message is delivered even when its `seq` is below
  the cursor (it was jumped over by an ack of a later message) — that is the point of the row. The
  delivery-mode filter is not applied again to a fallback row (it was wanted when the dispatcher pushed
  it); its `trusted` flag is recomputed for the principal (`g.d.Trust`).
- `Gateway.Pending` (`internal/agents/pending.go:19-73`) does the same merge per room and deletes the
  fallback rows it returns, because a pending read counts as delivered; the cursor advance still uses the
  last **cursor** message, never a fallback `seq`.
- `GET /agent/pending` resolves the request's session label with `SessionLabelOf(ctx, ai.ID, liveID,
  folderID.Folder)` (the authorship helper, T9). A tools-only process in a linked worktree has a folder
  different from the AI's folder, so `LabelForFolder` gives it a non-empty label; `Gateway.Pending` then
  returns an empty page — items and platform events — without reading a cursor, advancing one or deleting
  a delivery row (SESS-06). No live session holds that label, so nothing is addressed to it and its
  label's messages are the principal's; the principal's queue is the only queue, and the process must not
  drain it. The handler returns the empty page — items, platform events and the dropped count — at once
  (the `wait` parameter is not honoured for an empty page), so a sleeping reader never waits for messages
  it can never be given.
- The same rule covers a turn-end hook started in the worktree: a process whose folder resolves to a
  label holds no live socket, and a live socket receives its own pushes. A tools-only process in the
  AI's own folder (the principal label `""`) reads the AI's queue exactly as today, and a live labelled
  session never calls pending.
- The tool answer: `wait_for_messages` of a tools-only process whose identity is a linked worktree
  (`Identity.MainFolder` set and different from `Identity.Folder`) does not call the server at all, so it
  cannot drain a queue even against a server of an older version. Its first answer is
  `no new messages: This worktree session has no live channel: it only writes and reads history.` and
  later answers are `no new messages` (one `sync.Once` per process; `internal/channel/pending.go`).
  `ToolsOnlyInstructions` carries the same sentence, appended in `run.go` only for that identity, so a
  principal-folder tools-only process is told nothing new. The server's empty page (above) is the
  backstop for `partyline pending`, the turn-end hooks and a client that still asks.

### The fallback note

The frame carries `for_label`. The channel renders the content as
`(addressed to :wt)\n<text>` and sets `for_label` in the notification meta; the tools-only pending
response carries the same field on `dto.AgentMessage` and the pi/OpenCode adapters show it with the
message. The text is server-authored, never taken from the message (AD-006: content is data). Nothing is
written to the message row: the fallback is a delivery fact, kept in `agent_session_deliveries` (a row
born with `fell_back_at` when the label was not live, or marked later when its session ended), so the
note is still there when the principal reads the message from its queue. A message the AI's own session
wrote never carries the note, because such a row never falls back (SESS-38).

### Echo and sessions addressing each other

The delivery plan still excludes the author's AI from a room post (`Plan`,
`internal/delivery/rules.go`; `AD-009`): a message written by one session is authored by the AI, so its
other sessions are not targets of the normal path. Two sessions therefore cannot both answer the same
group message from a person (D3 addresses exactly one) and cannot hear each other's answers by accident.
D11 adds the one deliberate exception: a text that addresses a sibling with `@handle:label` reaches that
live sibling ([Sessions addressing sessions](#sessions-addressing-sessions)). The echo rule (SESS-25)
now means "never by accident": the writing session never receives its own message, a plain message is
never echoed, and a labelled message whose target is the author's own label goes nowhere.

## Authorship

A message a session writes is the AI's, with `messages.author_label` as its only session mark (SESS-28).
The dispatcher reads that same column to know which session wrote a message (D11), so it is one source
both for the `via` chip and for the sibling target.

The label is resolved server-side, never parsed from the text (SESS-29), by
`(*Registry).SessionLabelOf(ctx, agentID, liveID, sessionFolder)`:

1. `liveID != ""` and presence has a session with that `X-Partyline-Live` for the AI → its label. The
   header is the one the socket already uses (`dto.HeaderLive`), so a client that reconnects keeps
   posting with its current label; the tool client sends it on every REST call
   (`internal/channel/client.go`, `internal/channel/identity.go`), and `testkit`'s folder client already
   sends it (`internal/testkit/agent.go:236-246`).
2. otherwise, the request's folder differs from the AI's `agents.folder` → `LabelForFolder(folder)`; this
   is the tools-only process in a worktree (D10) and a client that does not send `HeaderLive`.
3. otherwise `""` (the principal and every old client).

The API sets `rooms.PostInput.SessionLabel` for agent principals only, in `postTo`
(`internal/api/rooms_handlers.go:215-243`) and the two agent tool post paths
(`internal/api/agent_handlers.go:680`, `:923`); `rooms.Service.Post` copies it into the draft, and
`insert` writes it to `messages.author_label` (`internal/rooms/messages.go:104-128, 288-320`). Edits and
deletes do not touch the column, so a message keeps the session that wrote it (SESS-31). A user's post
never carries a label. The DM-creation greeting (`internal/conversations/dm.go:183`) is posted without a
label for now (the response's `author_label` is empty); it is a system-like first message, and the design
default is to leave it.

`dto.Message` gains `AuthorLabel string json:"author_label,omitempty"` (`internal/api/dto/rooms.go:175-215`);
`convert.Message` copies it (`internal/api/convert/convert.go:55-71`), and `message_views` exposes it (the
view is recreated: `0023` is the precedent, `internal/store/migrations/0023_message_edit_delete.sql:10-31`).
The room timeline, the reply quote and the live `room.message` frame all carry it because they use the
same DTO.

## Presence surfaces

- **Profile**: `dto.AIDetail` gains `Sessions []dto.SessionInfo` (`internal/api/dto/intelligences.go:110-126`),
  filled in `toProfile` from `s.Agents.Sessions(a.ID)`
  (`internal/api/intelligences_handlers.go:94-100`). The principal is the entry with `label ""`; the list
  is always present (empty when offline) and the principal first (SESS-32).
- **Members**: `dto.RoomMember` gains `Sessions []dto.SessionInfo json:"sessions,omitempty"`, filled in
  `toMember` for agent members next to `Online` (`internal/api/rooms_handlers.go:39-62`); the mention
  autocomplete reads it (SESS-33).
- **Live event**: `Presence` publishes `events.AgentPresence` on every attach and detach, not only on
  online/offline transitions, keeping `Online` = any session live. The web handler
  (`web/src/lib/live/handlers.ts:290-330`) already patches presence and room-member `online`; it gains
  an invalidation of `queryKeys.intelligence(agent_id)` and of the room-member queries of rooms the AI is
  in, so the profile and the autocomplete refresh within one event without a reload (SESS-32, SESS-33).
  The payload stays `{agent_id, online, last_seen_at}`; a client that ignores the extra event is
  unaffected. (An alternative is to put `sessions` in the event; the invalidate keeps the DTO smaller and
  the event unchanged.)
- **`GET /agent/session`**: `SessionState` gains `Sessions []dto.SessionInfo` for the launcher's
  tools-only line and for any client that wants to show them (SESS-19).

## Data model

### Migration `0027_ai_sessions.sql`

```sql
-- One AI, several sessions (ai-sessions, SESS-01..SESS-39).
-- messages.author_label is the session that wrote an agent message ("" = the principal, and every
-- message stored before this migration). It is authorship, and the only way the dispatcher knows which
-- session wrote a message (D11); the session a message is addressed to is parsed from the text at
-- delivery time.
ALTER TABLE messages ADD COLUMN author_label TEXT NOT NULL DEFAULT '';

-- The delivery state of a message that was pushed to a labelled session, and the fallback queue of a
-- label whose session ended before it was acknowledged. A row exists only while the message belongs to
-- a label: the labelled session's acknowledgement deletes it, or the end of the session marks
-- fell_back_at and the principal consumes it. A row for a message the same AI wrote is deleted at the
-- end of that session instead, because the AI never receives its own message (SESS-38). The primary key
-- makes a retried push idempotent.
CREATE TABLE agent_session_deliveries (
    message_id   TEXT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    agent_id     TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    label        TEXT NOT NULL CHECK (label <> ''),
    created_at   INTEGER NOT NULL,
    fell_back_at INTEGER,
    PRIMARY KEY (message_id, agent_id)
);
CREATE INDEX agent_session_deliveries_pending
    ON agent_session_deliveries (agent_id, label) WHERE fell_back_at IS NULL;
CREATE INDEX agent_session_deliveries_fallback
    ON agent_session_deliveries (agent_id) WHERE fell_back_at IS NOT NULL;

-- The view lists its columns one by one, so it is recreated to expose author_label (0023's rule).
DROP VIEW message_views;
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_iid, m.author_user_id,
       m.sender_id, m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to,
       m.recipients_json, m.external_id, m.created_at, m.edited_at, m.deleted_at,
       m.author_label,
       CAST(CASE
            WHEN m.author_kind = 'system' THEN 'Partyline'
            WHEN m.author_kind = 'agent' THEN COALESCE(NULLIF(rm.display_name, ''), i.display_name, '')
            WHEN m.author_kind = 'user' THEN COALESCE(i.display_name, '')
            ELSE COALESCE(i.display_name, NULLIF(m.sender_name, ''), c.title, '')
            END AS TEXT) AS author_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS author_handle,
       CAST(COALESCE(i.kind, '') AS TEXT) AS author_ikind,
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS author_avatar
FROM messages m
LEFT JOIN intelligences i ON i.id = NULLIF(m.author_iid, '')
LEFT JOIN room_members rm ON m.author_kind = 'agent' AND rm.room_id = m.room_id
                         AND rm.kind = 'agent' AND rm.member_id = m.author_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;
```

AD-012 is respected: one `ADD COLUMN` with a default, one new table (nothing references it), and a view
recreation like `0023`'s. There is no Go hook: existing messages keep `author_label = ''`, and there is
nothing to back-fill, because a message written before this feature has no session label.

### Queries (`internal/store/queries/session.sql`, plus `agents.sql`)

- `CreateSessionDelivery(message_id, agent_id, label, created_at, fell_back_at)` — `INSERT … ON CONFLICT
  (message_id, agent_id) DO NOTHING`; `fell_back_at` is NULL for a live label and the dispatch time when
  the label has no session (the message is the principal's from the start).
- `DeleteSessionDelivery(message_id, agent_id) :exec` — deletes a **fallen-back or unowned** row (the
  principal's and a tool's consumption).
- `AckSessionDelivery(message_id, agent_id) :execrows` — `DELETE … WHERE message_id = ? AND agent_id = ?
  AND fell_back_at IS NULL` (a labelled session's acknowledgement; 0 when the principal owns it now).
- `FallBackSessionDeliveries(agent_id, label, fell_back_at) :many` — `UPDATE … SET fell_back_at = ?
  WHERE agent_id = ? AND label = ? AND fell_back_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = message_id AND m.author_kind = 'agent'
                  AND m.author_id = agent_id) RETURNING message_id` (a message the AI wrote is never
  handed back to it, SESS-38).
- `DeleteSelfSessionDeliveries(agent_id, label) :execrows` — `DELETE … WHERE agent_id = ? AND label = ?
  AND fell_back_at IS NULL AND EXISTS (the same author clause)`: the end-of-session sweep drops the
  rows of the AI's own messages instead of falling them back.
- `ListSessionFallbacks(agent_id, room_id) :many` — `message_views` joined to the rows with
  `fell_back_at IS NOT NULL`, by `seq`.
- `ListSessionDeliveriesByLabel(agent_id, label) :many` — the rows of a label (the end-of-session sweep
  needs the ids before it marks them; the `RETURNING` above is enough, so this is only for tests).
- `CountAgentBacklog`, `ListAgentBacklog`, `ListAgentPending` gain the `NOT EXISTS` clause (above).

## Protocol and API

No new route. The additive wire changes:

| Where | Change | Spec |
| --- | --- | --- |
| `X-Partyline-MainFolder` request header (percent-encoded) | The main worktree folder of a linked worktree session; absent for every other session and for a `--parent` launch | SESS-01..SESS-04, SESS-09 |
| `X-Partyline-Live` on tool calls | The process id the socket already sends; the server uses it to resolve the author label | SESS-29 |
| `dto.AgentWelcomeFrame.Session` | `{label, harness, connected_at}`; absent/empty label = principal | SESS-15 |
| `dto.AgentMessage.ForLabel` | `for_label` on a fallback frame | SESS-22 |
| `dto.Message.AuthorLabel` | `author_label` on every message | SESS-30 |
| `dto.AIDetail.Sessions` | the AI's live sessions | SESS-32 |
| `dto.RoomMember.Sessions` | an agent member's live sessions | SESS-33 |
| `dto.SessionState.Sessions` | `GET /agent/session` | SESS-19 |
| Close codes | `4002 taken_over` for a takeover of the principal; `4003 identity_busy` is never sent | SESS-13 |

`make generate` regenerates the sqlc and tygo outputs (AD-003) in the tasks that change queries or DTOs.
A request with an unusable `X-Partyline-MainFolder` is 422 `folder_invalid` with
`fields.main_folder`, the same envelope as a bad folder (`internal/agents/folder.go:78-80`).

## Web

### "via :label"

`web/src/components/message-item.tsx`'s author header (`:360-400`) adds, after the author name and before
the chat-via:

```tsx
{message.author_kind === "agent" && message.author_label ? (
  <span className="min-w-0 text-xs text-muted-foreground [overflow-wrap:anywhere]">
    {t("message.viaSession", { label: message.author_label })}
  </span>
) : null}
```

`en.ts`: `"message.viaSession": "via :{label}"`; `pt-BR.ts`: `"message.viaSession": "via :{label}"` (the
same words; the placeholder is the label). A grouped row keeps the name in the screen-reader text
(`authorDisplayName`) and adds the label there too, so a screen reader says who wrote it. The label has
no link and no menu action; it is a fact of the message.

### The profile's live sessions

`AIDetails` (`web/src/routes/_app/contacts.$id.tsx:363-430`) gains one `Section`
("Live sessions" / "Sessões ao vivo") with a `<ul aria-label>` when `ai.sessions.length > 0`:
each row is `principal` or `:{label}` in `font-mono text-xs`, the harness label
(`harnessLabel(session.harness)`, `web/src/lib/agents.ts`), and the connection time
(`formatAbsolute`/relative, the existing helpers). Rows use `flex flex-wrap items-baseline gap-x-2`,
`min-w-0` and `[overflow-wrap:anywhere]`, so a 40-character label wraps instead of widening the panel; a
`data-testid="session-label"` makes the fit test exact. When the AI is offline the section is absent (the
list is empty), exactly like today's "never seen" line. The live handler invalidates the profile on
`agent.presence`, so a second session appears without a reload (SESS-32).

### The mention autocomplete

`mention-suggestions.tsx`:

- `MentionCandidate` becomes `{ intelligence: IntelligenceRef; label: string }` (label `""` for the AI
  itself); `mentionCandidates(members, viewerId)` returns the AI once, then one candidate per live
  labelled session of each agent member (`member.sessions`, `label !== ""`).
- `matchMentions(candidates, query)` folds the display name, the handle **and** the label; the same eight
  cap applies.
- `mentionOptionId` uses `${intelligence.id}:${label || "principal"}`; the row shows the name and, for a
  labelled candidate, `@handle` plus a muted `:label` (`font-mono text-xs`), so a phone sees which
  session it is picking. The principal candidate is the plain `@handle`.
- `onPick`/`completeMention` (`composer.tsx:267-283, 540`) inserts `@handle:label ` for a labelled
  candidate and `@handle ` for the AI. The replacement covers the typed `@word` to the caret; the label
  is inserted whole, so nobody types the colon.
- A room whose members have no sessions offers exactly what it offers today (no regression for humans,
  chats or an offline AI).

### Mobile first

The chip, the profile rows and the autocomplete options are single-line flex rows that wrap with
`min-w-0`/`[overflow-wrap:anywhere]`; no fixed widths are added. The fit end-to-end check
(`web/e2e/fit.spec.ts`, 320/412 px, font at 125%, `expectFits`) seeds a second live session with a
40-character label and a message posted through it, then checks the conversation (the `via` chip), the
profile panel and the full profile page. The spec's SESS-34 is the assertion.

## Tests

The tests derive from the spec's acceptance criteria and are written before the code; the expected values
below are the assertions.

| Layer | Tests | Expected values |
| --- | --- | --- |
| Labels (`internal/agents/labels_test.go`, pure) | `LabelForFolder`, `AllocateLabel` | `"/repo/wt"` → `"wt"`; `"/repo/Acme API!"` → `"acme-api"`; `"/repo/___"` → `"session"`; 50 a's → 40 a's; `\`C:\work\Acme_2\`` → `"acme-2"`; `"/"` → `"session"`; principal free → `""`; principal taken, harness `claude` → `"claude-2"`; `wt` free → `"wt"`; `wt` taken → `"claude-2"`; `claude-2` taken → `"claude-3"`; empty harness → `"agent-2"`; remembered label free → it, taken → the next free |
| Address parser (`internal/delivery/session_test.go`, pure) | `SessionLabel` | `"hi @atlas:claude-2"` → `"claude-2"`; `"@atlas"` → `""`; `"@atlas:"` → `""`; `"@atlas:Bad"` → `""`; `"@atlas:claude-2x"` → `"claude-2x"`; `"@atlas:claude-2."` → `"claude-2"`; `"@atlas:Bad @atlas:wt"` → `"wt"`; `"@atlas-two:x"` for `atlas` → `""`; `"x@atlas:wt"` → `""`; `"(@atlas:wt)"` → `"wt"`; `"@ATLAS:wt"` → `"wt"`; `"@ATLAS:WT"` → `""`; 41-char label → `""`; empty handle → `""` |
| Migration/store (`internal/store/session_migration_test.go`) | 0027 on a database before it and on a fresh one | `messages` has `author_label` default `''`; `PRAGMA table_info` and the view return it; `agent_session_deliveries` has exactly `message_id, agent_id, label, created_at, fell_back_at`; the label CHECK refuses `''`; a second delivery insert for one (message, AI) is a no-op; deleting the message deletes the row; deleting the AI deletes the row; `ListSessionFallbacks` returns only the fallen-back row of its room; `AckSessionDelivery` deletes 1 pending row and 0 fallen-back rows; `FallBackSessionDeliveries` returns the pending ids of that label and no other |
| Presence (`internal/agents/presence_test.go`) | labels, races, takeover, reconnect, kick | first attach `""`; second `"claude-2"`; third `"claude-3"`; a worktree preferred `"wt"` → `"wt"`; two goroutines for the principal → labels `{"" , "claude-2"}`; same liveID → same label and `since`, old socket 1000 `reconnected`; takeover with preferred `""` → old 4002 `taken_over`, new `""`; takeover with `"wt"` → nothing closed; `Sessions` = principal first; `Online` true with one and two; `Kick` closes all and `Online` false; remembered label restored after a detach (now + 10 min), not after the TTL |
| Gateway (`internal/agents/gateway_live_test.go`, `internal/api/agent_sessions_test.go`) | parallel connection, welcome, no busy, cursor | two dials with different live ids → both 101; welcomes `session.label` `""` and `"claude-2"`; a second dial no longer answers 409 `identity_busy`; the old principal socket closes 4002 on `--take-over`; a labelled ack leaves `agent_cursors` at 0; the AI's harness/`connected_at` follow the principal only; `GET /agent/session` lists both sessions |
| Routing (`internal/delivery/dispatcher_test.go`, `internal/agents/session_delivery_test.go`, testkit) | group/DM/mention/label/fallback/edit/pause/echo | group "hello" → principal only; "@atlas:wt hi" → `:wt` only with a delivery row; "@atlas:wt hi" with `:wt` offline → principal with `for_label "wt"` and a row born fallen back; a second message while the principal is offline → no push, remains in the backlog with the note; `:wt` closes before ack → the row `fell_back_at` is set and the principal receives the message once; the late ack deletes nothing; an edit that adds `@atlas:wt` reaches `:wt`; a paused room delivers nothing; a plain message by `:wt` is not delivered to `:wt` or the principal |
| Sibling routing (D11) (`internal/delivery/dispatcher_test.go`, `internal/agents/session_delivery_test.go`) | a principal and a labelled session, the stored author label, edits | the principal writes "@atlas:wt do X" → `:wt` once with `trusted: true` and a delivery row; `:wt` writes "@atlas do Y" → no session of `atlas`; `:wt` writes "@atlas:wt do Z" → none; `:wt` writes "@atlas:gone do W" → none and no row; `:wt` closes before ack → `fell_back_at` stays NULL, the row is deleted and the principal gets nothing; an edit of the principal's plain message that adds `@atlas:wt` reaches `:wt`, a delete of it reaches `:wt`; a paused room → none |
| Worktree tools-only reader (`internal/api/agent_pending_test.go`, `internal/channel/pending_note_test.go`) | an empty page that consumes nothing, and the one-time note | a tools-only request with folder `/repo/wt` and main folder `/repo` → `items: []`, `platform: []`, `dropped` 0, the AI's cursor unchanged and its delivery row not deleted, answered at once even with `wait=60`, while the principal's next pending call still returns the message; a request with the AI's own folder → the queue as today; a linked-worktree `wait_for_messages` makes no request, its first answer contains `This worktree session has no live channel: it only writes and reads history.` and its second does not; a principal-folder tools-only process asks the server and never gets the sentence |
| Pending/backlog (`internal/agents/gateway_backlog_test.go`, `internal/api/agent_pending_test.go`) | fallback rows | the principal's backlog lists a fallen-back message below its cursor with `for_label`; `GET /agent/pending` returns it and consumes it (a second call returns it not); a pending row for a live label is not in the principal's backlog or pending |
| Authorship (`internal/api/agent_sessions_test.go`, `internal/rooms/messages_test.go`) | author label | a post with a live `X-Partyline-Live` of `:claude-2` → `author_label "claude-2"`; a post from a worktree folder without live → `"wt"`; the principal and a user → `""`; an edit keeps `"claude-2"`; `GET /rooms/{id}/messages` and the live `room.message` frame carry it |
| CLI (`internal/channel/worktree_test.go`, `internal/cli/launcher_test.go`, `internal/cli/channel_test.go`) | detection, header, launcher, notice | worktree outputs differ → main folder from the first `worktree` line; equal outputs → `""`; a failing/timed-out runner → `""`; the child env has `PARTYLINE_MAIN_FOLDER`; `--parent` does not set it; the launcher starts without asking when the AI is online; the channel prints `partyline: opened as parallel session claude-2` once |
| API surfaces (`internal/api/intelligences_test.go`, `internal/api/rooms_handlers_test.go`) | profile/members/session state | the profile's `ai.sessions` is `[{label:"",harness:"claude"},{label:"claude-2",harness:"pi"}]` by connection time; an agent member's `sessions` carries both; a human member has none; `GET /agent/session.sessions` has two; the auth of the existing routes is unchanged |
| Web unit (`web/src/components/message-item.test.tsx`, `.../mention-suggestions.test.tsx`, `.../contacts.test.tsx`, `web/src/lib/live/handlers.test.ts`) | chip, autocomplete, profile, live update | an agent message with `author_label "claude-2"` shows `via :claude-2`; `""` shows nothing; candidates are the AI plus one per labelled session; matching "claude" finds `:claude-2`; picking inserts `@atlas:claude-2 `; the profile lists `principal`, `:wt`, the harness and since; an `agent.presence` event invalidates the profile query |
| E2E (`web/e2e/fit.spec.ts`, `web/e2e/ai-sessions.spec.ts`) | 320/412 px, a two-session AI | the two sessions are seeded with a real second socket and a live id; the conversation shows `via :claude-2` on the message that socket posted; the profile and the panel fit at both widths and 125% font; the autocomplete offers `@atlas` and `@atlas:claude-2` and inserts the labelled address |
| Docs (`docs/agents.md`, `docs/api.md`, `README.md`, `.specs/STATE.md`, `.specs/features/intelligences/spec.md`) | none (the `make check` gate) | no `identity_busy` instruction or takeover prompt remains; `author_label`, `for_label`, `X-Partyline-MainFolder`, the sibling rule, the worktree tools-only reader and the merge cleanup are documented; AD-032 is in `STATE.md`; SES-01..SES-07 point at SESS-* |

## Code Reuse Analysis

| Existing component | Location | How the feature uses it |
| --- | --- | --- |
| `containsBounded`/`continuesHandle` | `internal/delivery/wants.go:102-134` | The address parser uses the same word-boundary rule as the mention matcher; no second notion of a mention |
| `Presence` and its mutex | `internal/agents/presence.go` | One map of labels under the existing lock keeps attach, takeover, detach and the online event race-free |
| `FolderID`/`ResolveFolder`/`LookupFolder` | `internal/agents/folder.go:37-270` | The main folder is one more field of the identity the server already resolves; `linkOrCreate`, adoption and placement are unchanged |
| `agent_cursors` and the backlog/pending reads | `internal/store/migrations/0001_init.sql:161`, `internal/store/queries/agents.sql:85-113`, `internal/agents/gateway.go:589-715`, `internal/agents/pending.go` | The principal's queue and the 50-message cap stay one implementation; the fallback rows are merged in |
| The agent dispatcher's plan/wants/trust | `internal/delivery/dispatcher.go:150-252` | Routing is one target per already-decided recipient; the loop guard and the delivery modes are untouched; D11 adds one target next to the plan and reads the stored author label |
| The gateway's ack and cursor | `internal/agents/gateway.go:717-780` | A labelled ack replaces the cursor advance with a row delete; the principal keeps the existing path |
| `CanonicalFolder` and the launcher variables | `internal/channel/identity.go:76-92`, `internal/cli/procenv.go` | The main folder is canonicalized and handed down like the folder, and reaches a harness that strips the environment |
| `message_views` recreation | `internal/store/migrations/0023_message_edit_delete.sql` | The same `DROP VIEW`/`CREATE VIEW` pattern for the new column |
| The web's mention list and the live handler | `web/src/components/mention-suggestions.tsx`, `web/src/lib/live/handlers.ts:290` | One candidate per label and one invalidation; the existing combobox, keyboard and phone behaviour is reused |
| The fit check | `web/e2e/fit.spec.ts:112-140` | One more seeded session and one more page check, with the same `expectFits` |

**Integration points**: the CLI and channel (`internal/cli`, `internal/channel`), the agents package
(presence, gateway, folder), the delivery dispatcher, the store (migration and queries), the API DTOs and
handlers, the web app and the Playwright suite. No new package and no archtest layer change: the code stays
in packages that already have their layer.

## Risks & Concerns

| Concern | Location (file:line) | Impact | Mitigation |
| --- | --- | --- | --- |
| Two sessions of one AI answer the same group message | `internal/delivery/dispatcher.go:150-201` | The person gets two answers, or two agents act on one instruction | D3 routes each message to exactly one session (the first `@handle:label`, else the principal); a session sees a sibling's answer only when the answer's text explicitly addresses it (D11); a test asserts "one recipient session per message" |
| A session's own message comes back to it | `internal/delivery/dispatcher.go`, `internal/agents/gateway.go` | An agent answers itself or acts twice on its own text | `messages.author_label` names the author, its own label is excluded, a plain message gets no self target, and the gateway never falls a self row back; a test posts the three examples and asserts who receives what |
| A worktree tools-only process drains the principal's queue | `internal/agents/pending.go`, `internal/api/agent_handlers.go` | The principal never sees a message, and a process without a live session acts on it | Its folder resolves to a label, so pending returns an empty page and consumes nothing; tested with a message the principal's next pending call still returns |
| Two connections race for the principal label | `internal/agents/presence.go` | Two sessions both think they are the principal | Label allocation and attach run under the existing presence mutex; a test starts two goroutines and asserts `{"" , "claude-2"}` |
| Two sessions of a new repository race to create the AI | `internal/agents/folder.go:214-239` | Two AIs for one repository | The existing `allocMu` plus the unique index `agents_folder`; the main folder makes both calls use the same key |
| A silent session keeps its label for up to 35 s | `internal/agents/gateway.go:31-38, 555-575` | A message addressed to it goes to a zombie and is not answered for up to 35 s | The message is unacknowledged and falls back to the principal when the ping drops the socket (SESS-24); a new session in the same folder gets a parallel label instead of stealing the label |
| A late ack after the fallback consumes the message twice | `internal/agents/gateway.go:753-780`, `internal/store/queries/session.sql` | The message disappears from the principal's queue | `FallBackSessionDeliveries ... RETURNING` owns the rows; a labelled ack only deletes `fell_back_at IS NULL`; a test acks after the sweep and asserts the row is still the principal's |
| The principal's cursor jumps over a fallback message | `internal/agents/gateway.go:589-715`, `internal/agents/pending.go` | A message for a dead label is lost | Fallback rows are read independently of the cursor and merged by `seq`; an ack of a later message can no longer hide them |
| A labelled session gets no room instructions notice | `internal/agents/gateway.go:589-715` | A parallel agent does not know the room's instructions | It has no backlog by design; the tools' `room_info` answers with the instructions and its trust; documented in the design and `docs/agents.md` |
| The label leaks a worktree name to the room | `dto.Message.author_label` | A worktree's name (possibly a customer or a branch) is visible to everyone in the room | D2/D6 require the label; a message's session is already visible from the AI's profile; the docs state it and a label is at most the folder base name (no path) |
| Old worktree AIs stay and multiply until merged | `docs/agents.md`, `POST /agents/{id}/merge` | An owner keeps seeing several AIs for one repository | D8: the feature stops new ones; the docs describe ending the old sessions and merging each into the repository's AI; no automatic merge |
| A mixed rollout creates a worktree AI with an old CLI | `internal/agents/folder.go`, `internal/cli/channel.go` | One identity appears once; the next new CLI folds it and leaves it to merge | The server accepts both; the docs tell the operator to upgrade the CLIs first; the old AI can be merged |
| A client claims another session's label | `X-Partyline-Live` on tool calls | A message shows the wrong `via` | Only a live session of the same AI (same token) can be claimed, so the author is still that AI; a label that matches no live session is ignored and the folder label is used |
| The fallback note could be mistaken for message content | `internal/channel/format.go:32-78` | A model treats the server's note as the author's words | The note is a separate meta key and a lead line in Partyline's voice; AD-006's `trusted` flag still describes the author |
| A worktree whose main worktree is gone | `internal/channel/worktree.go` | An AI is created at a path that does not exist | The rule is the folder is the identity; the session reconnects when the path returns; the docs' merge covers a deliberate move |
| `--parent` in a worktree | `internal/cli/launcher.go:178-270` | A child AI silently becomes the parent's AI | The launcher sends no main folder when a parent is asked for; a CLI test asserts the env has no `PARTYLINE_MAIN_FOLDER` |

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| A `sessions` table with one row per live session | Sessions are ephemeral (AD-010). A table would need a reaper for crashed servers and a restart would have to mark every row dead; presence already has the truth |
| A cursor per (AI, label) in `agent_cursors` | The primary key is (agent_id, room_id); adding a label means rebuilding the table, which AD-012 forbids. A second cursor table plus a backlog per label duplicates the queue semantics; the delivery-row table keeps one principal queue and is smaller |
| Storing the addressee label on the message (`messages.to_label`) | One message can address labels of several AIs in one text (`@atlas:wt … @alice:pi`), so the target is recipient-relative; a column cannot express it. Parsing at delivery time is the same rule the person typed and follows edits |
| A `message_targets` table with a row per (message, AI) | It would duplicate every message for every AI. The delivery row already exists only for messages that actually went to a label |
| Server-side worktree detection | D1: the server has no filesystem of the session's machine; a server-side git call would be wrong for a remote machine |
| The client sends the author label as a claim | `X-Partyline-Live` already names the process and the server owns the label list; a claimed label is one more thing to validate, and the liveID resolves the parallel case exactly |
| A new frame type for the fallback | Additive fields keep v0.5 channels working (AD-026's precedent); a new type would be ignored by an older client that then never learns why the message is there |
| Making the parallel session the new principal on arrival | D4: a second session must not disturb the one the person is talking to |
| Auto-merging worktree AIs | D8: a merge moves history and rooms; it must be a deliberate, audited action, and a mistake is not undone |
| A per-session unread cursor / notification | D5: the AI has one unread state; sessions are delivery targets, not accounts |
| Parsing the author session from the message text for D11 | The text is data, and an edit would rewrite who wrote it; `messages.author_label` is stored at post time and is the same source as the `via` chip |
| Falling a sibling's unacknowledged message back to the principal | The principal is the AI itself, so the fallback would hand the writing session its own text; the row is deleted at session end instead |
| Delivering pending messages to a worktree tools-only process | Nothing is addressed to a label with no live presence, and the principal's queue would be drained by a process that cannot answer on a channel |

## Operations and upgrade

- **Upgrade order**: server first (migration 0027 is additive), then the CLIs. A session of an old CLI
  keeps resolving by its folder; a new CLI against an old server ignores the refused/absent header.
- **Backup**: nothing new to back up; the migration adds one column, one table and a view. The usual
  database backup applies.
- **What an operator sees**: after the upgrade, a session in a linked worktree appears in the existing AI
  (the repository's) and adds a session row to its profile. Old worktree AIs keep their rows; to clean
  one up: end its sessions, then `POST /agents/{id}/merge` into the repository's AI (docs/agents.md).
- **Logs**: a session's label and the AI id may be logged (`agent: session attached`, `agent: session
  ended`); message text is never logged (the existing rule). No new secret or file.
- **Removal**: no table or column is removed by this feature. If a later feature removes sessions, the
  `agent_session_deliveries` rows must be consumed or deleted first (they are the message's only owner).

## Fixed principal (v0.11.0)

Spec: "P1: Fixed principal (v0.11.0)", SESS-51..SESS-62; decision AD-034.

### How a parallel session became the principal (before)

`Presence.AttachLabeled` (`internal/agents/presence.go`) allocates a label with
`AllocateLabel(preferred, harness, taken, remembered)` (`labels.go`). A session in the AI's own folder
prefers `""` (`gateway.register` sets no preferred label), and `AllocateLabel` returns `preferred` as soon as
`!taken("")`. So the label was a free-for-all decided at connect time: when the principal's socket dropped,
`Detach` freed `""`, and the first connection that arrived after it, whichever process or conversation it
belonged to, took it (B reconnecting, or any new `partyline claude`). The 10-minute memory was keyed by the
random live id of the process, so a resumed conversation (a new process) never recovered its label: A
reopened with `claude --resume` found `""` taken and was allocated `claude-2`. Delivery only ever asks "which
connection holds `""`" (`pushToPrincipal` -> `Presence.Session`), and the platform feed is attached when
`live.Label == ""`, so whoever held `""` received everything unaddressed.

### The pin

`agents.principal_session TEXT NOT NULL DEFAULT ''` (migration 0028) holds the harness session id of the
principal; no new table, since it is one value per AI and `GetAgent` already returns it. The id is
what `X-Partyline-Harness-Session` carries; the CLI reads `CLAUDE_CODE_SESSION_ID` for Claude Code (the
`partyline channel` process inherits it, and it stays the same across `--resume`) and
`PARTYLINE_HARNESS_SESSION` for any other harness. The server validates the header against
`dto.HarnessSessionPattern` in `gateway.register`, before `ResolveFolder`, so a bad value creates nothing
(422 on `harness_session`).

`Registry.AttachSession` reads the pin under `appMu`, the lock every attach already takes, calls
`Presence.Attach` and writes back the pin the attach decided to change. `decideRole` (pure, tested through the
API) is the whole policy:

| Case | Principal? | Pin after |
| --- | --- | --- |
| worktree label (preferred != "") | no | unchanged |
| `--principal`, or `--take-over` while another process holds `""` | yes | this session's id (empty clears) |
| an id is pinned | iff the session's id is the pinned one | unchanged |
| nothing pinned, `""` free, the same process or the same harness session | yes | this id, when it has one |
| nothing pinned, `""` held by another | no | the holder's id, when it has one |

A parallel session never gets `""`: the allocator treats the principal label as taken. When the role
changes for a process that is already attached (the same live id reconnects after the pin moved), the old
entry is dropped and the process attaches again in its new role. The session that takes `""` closes the
holder with 4002 `taken_over` when the replacement is explicit or the same conversation, else with 4004
`role_changed`, so a displaced session reconnects as parallel instead of stopping.

Labels: `Presence.remembered[agent][harness session id]` (24 hours) next to the `retired[agent][live id]`
memory (10 minutes); both are consulted on attach, the process first. A parallel session that ends writes
both; the principal only writes the live id one. Because presence is lost when the server restarts (a deploy
makes every session reconnect at once, in any order), the label of a session with an id is also stored:
`agent_session_labels (agent_id, harness_session, label, updated_at)` in migration 0028. `Registry.AttachSession`
reads the row before the attach (`AttachRequest.StoredLabel`, used when presence remembers nothing) and upserts
the label the session got after it (the principal's row is deleted; rows older than 30 days are pruned on
write; the AI's deletion cascades). Presence stays in memory: only this mapping and the pin are persisted.
After a restart the pinned session is the principal whenever it reconnects, because the role depends on the
stored pin and not on the order, and the parallel sessions get their labels back while they are free.

"Principal offline since": `Presence.principalGone[agent]`, set when the principal detaches or is ended (and
when the first parallel session is seen with no principal), cleared when a principal attaches. It is in memory
like every session fact; `Presence.PrincipalGone` returns it only while the AI has a live session and no
principal. The API exposes it as `principal_offline_since` on the AI of a profile, on `GET /agents` and on a
room member.

### Make principal

`POST /agents/{id}/principal {label}` -> `Registry.MakePrincipal`: authorize like `EndSession`
(`managedAgent`), under `appMu` find the live session by label, require it to have an id, write the pin,
then `Presence.CloseForRoleChange(agent, "", label)` (4004). Both connections end, their processes
reconnect, and `decideRole` gives the role to the pinned one whichever arrives first. The old principal
therefore needs no relabel of a live connection (its delivery rows, platform feed and welcome would all
have to change), and the same path handles a process that never comes back: the pin is already moved.

### Per-harness session id

| Harness | Source | Notes |
| --- | --- | --- |
| Claude Code | `CLAUDE_CODE_SESSION_ID` in the `partyline channel` environment | verified in production to stay the same across `--resume` |
| pi | `ctx.sessionManager.getSessionId()` in the extension, passed to `listen` as `PARTYLINE_HARNESS_SESSION` | read defensively (an absent or malformed id is none); not verifiable without pi in the build environment |
| OpenCode | none | one `listen` per plugin instance serves whichever conversation is active, so no id names the listener |
| omp | none | the channel is started by omp from `mcp.json`; `PARTYLINE_HARNESS_SESSION` is honoured if a wrapper sets it |
| Codex, Gemini, Antigravity | none | tools only, no live session |

### Tests

`internal/store/principal_migration_test.go` (migration and label table); `internal/api/agent_principal_test.go` (the incident, a server restart with the sessions reconnecting in the wrong order,
label stickiness, header validation, first pin, no-id fallback, `--principal`, `--take-over`, make principal
by API); `internal/channel/link_live_test.go`, `internal/cli/{procenv,launcher,channel_live}_test.go`
(the CLI side); `integrations/{pi,opencode}/partyline.test.ts` (the adapters);
`web/src/**` unit tests for the button and the offline line; `web/e2e/principal-session.spec.ts`.

## Decision record (proposed)

When the plan is approved, record AD-032 in `.specs/STATE.md`: *an AI is identified by the main worktree
of a repository (the CLI sends it), an AI may have several labelled live sessions held in presence,
messages are addressed to a session by `@handle:label` and authored with the session label, a labelled
delivery that is not acknowledged falls back to the principal through `agent_session_deliveries` (a
message the AI wrote itself is never handed back to it), a session may address another live session of
its own AI with `@handle:label`, and a tools-only process whose folder resolves to a label reads no
queue.* It amends AD-010 (at most one live session) and the intelligences spec's SES-01..SES-08, which
the docs task (T13) updates.

## What this supersedes in the intelligences spec

| Old requirement | New requirement | Behaviour |
| --- | --- | --- |
| SES-01 (the "Take over? [y/N]" prompt) | SESS-16, SESS-15 | The launcher starts; a parallel session is announced with one line |
| SES-02 (takeover makes the new session the identity's) | SESS-13 | Takeover replaces the **principal** only; a worktree/parallel takeover is ignored |
| SES-03 ("no"/enter exits 1) | superseded by SESS-16 | There is no question to answer |
| SES-04 (`identity_busy`, the busy texts, the retries) | SESS-10, SESS-12 | A second connection is a parallel session; `identity_busy` is never sent |
| SES-05 (the taken-over notice) | unchanged (SES-05) | The old principal still gets `takenOverText` when it is taken over |
| SES-06 (offline within 5 s / 35 s) | SESS-19 | Unchanged per session; a label is freed at the same moment |
| SES-07 (no terminal → no prompt) | superseded by SESS-16 | There is no prompt with or without a terminal |
| SES-08 (the queue waits) | SESS-23 | The principal's queue waits; other sessions do not get it |

## Proposal: polled sessions for tools-only harnesses (not built)

Status: proposal, 2026-10-28, from the Antigravity (`agy`) field test. Nothing here is implemented.

**The gap.** A session of a tools-only harness (Codex, Gemini, Antigravity) holds no socket. Opened in the
folder of an AI whose principal is live elsewhere, it cannot become a labelled parallel session like pi,
OpenCode or Claude Code do (SESS-10, SESS-11), so it only prints "…this session uses tools only". Today the
server treats it as follows:

- its tool calls act as the **principal** (`SessionLabelOf` gives `""` for the AI's own folder), so its posts
  carry no `author_label` and nothing can address it apart from the live principal;
- `GET /agent/pending` answers **409** (`errPushedSession`) while the principal is live, so its turn-end hook
  and `wait_for_messages` read nothing: the principal's session gets the messages;
- with no principal live it reads the principal's queue and shares its cursor (SESS-23, SESS-46).

**Option A (recommended when this is built): a polled session.** The process joins as a labelled session
that is kept alive by its own calls instead of a socket.

1. *Identity.* The launcher mints a live id for the tools-only harnesses too (`PARTYLINE_LIVE`, as for pi and
   OpenCode, SESS-45); the MCP server and the hook inherit it, so both halves are one session.
2. *Presence.* A new `polled` implementation of `agents.Session` (`Close` does nothing) is attached with
   `AttachLabeled` on the first call that carries a live id while the principal is live (or when the label is
   taken), and gets a label (`agy-2`). Each tool call, pending read and hook read renews it; a sweep in
   `Presence` removes it `polledTTL` (about 5 minutes) after its last call and runs the usual end-of-session
   fallback. It appears in `Sessions()` (so in `GET /agent/session` and the members list) but not in
   `Online()`/`Live()`, which keep meaning "has a socket": the 409 rule, the "also open elsewhere" notice and
   the dispatcher's push set all depend on that.
3. *Delivery.* `Push` for the label creates the `agent_session_deliveries` row owned by the polled session's
   live id instead of a connection; `Pending(label != "")`, which returns nothing today (SESS-06), returns the
   pending rows of that owner and acknowledges them; on expiry the existing sweep falls the rows back to the
   principal with the note (SESS-22, SESS-24).
4. *Routing.* The 409 rule stays for the principal's reader only; a reader with the live id of a polled
   session is answered with its own page. A polled session receives what any labelled session does: messages
   addressed `@handle:agy-2` and the principal fallbacks of its own rows. It would **not** receive a plain DM
   reply to its own message, because replies go to the principal today (SESS-38); fixing that is a separate
   routing rule ("a reply to a message with an `author_label` is addressed to that label").

**Cost.** New presence kind and sweeper, a second owner type for delivery rows, a `Pending` branch, launcher
and channel changes for three harnesses, new requirements (SESS-50..) and about 8 test files across
`agents`, `delivery`, `api` and `cli`. Risks: leaked polled entries (a bounded TTL covers it), a polled label
that a person addresses while its session is idle (the message waits until the next turn ends, up to the TTL,
then falls back), and the extra writes of a per-call renewal (kept in memory, nothing is written per call).

**Option B (smaller): tools-only follows the principal, openly.** Keep the single identity and say so: the
launcher already prints the "uses tools only" line; the hook and `wait_for_messages` would answer a one-line
"messages for this AI go to its live session" instead of an empty page, so the person and the model know why
nothing arrives. No new state, but a second session of the same AI still cannot receive anything.

**Option C: refuse to share.** `partyline agy` in a folder whose AI is live elsewhere asks the person to use
`--as`/another folder. Cheapest, and the most restrictive.

**Decision needed from the maintainers:** A, with the DM-reply routing rule as its own task, or B for now. The
interim behaviour (shipped with this note) is: tools-only activity updates the AI's last-seen time, and
`partyline agy` prints a one-line hint about when messages arrive.

## Decided questions

The three questions the brief left open were decided by the coordinator on 2026-10-27 and are recorded in
[spec.md](spec.md#decided-questions-formerly-open): a message for a label that is not live goes to the
principal with a note, a worktree tools-only process reads no queue, and "End session" ends every live
session. D11 (sessions address each other) is the fourth decision and is specified in
[Sessions addressing sessions](#sessions-addressing-sessions).
