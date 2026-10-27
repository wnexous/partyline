# AI Sessions Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Spec**: `.specs/features/ai-sessions/spec.md`
**Design**: `.specs/features/ai-sessions/design.md`
**Status**: Draft, awaiting approval

The product owner's brief fixes the behaviour (D1..D11 in the spec's Problem Statement). The design
settles the rest: the CLI worktree detection and the server key, the label rules, the presence model, the
delivery rows and the fallback, the author label, the tokenizer of `@handle:label`, the sibling routing
(D11), the worktree tools-only reader, migration `0027`, and the web surfaces.

Each task is one commit, about 300 changed lines and about 5 hand-written files, under an hour, and is
independently testable. T4, T6 and T8 are the largest: T4 is the CLI/server vertical slice of D1, T6
replaces the tests that assert the old refusal, and T8 is a labelled push, its acknowledgement and the
principal's fallback queue, which must land together or a labelled message would be stranded when its
session ends. D11's sibling target (T15) and the worktree tools-only reader (T16) are split out of T8 so
each stays one commit; the line hint is a target, not a cap, and each task stays one behaviour. Tests are
written first and derive from the spec's acceptance criteria. Generated files (`internal/store/db/*`,
`web/src/lib/api/types.gen.ts`) are never hand-edited.

## Test Coverage Matrix

> Generated from the codebase, the project guidelines and the spec. Guidelines found: `AGENTS.md` (tests
> derive from the spec's ACs; `make check` before every commit; no message text or secrets in logs;
> synthetic fixtures), `.specs/LESSONS.md`, `Makefile`, `scripts/check.sh`, `web/playwright.config.ts`,
> existing tests (`internal/agents/gateway_live_test.go`, `internal/agents/presence_test.go`,
> `internal/store/message_edit_migration_test.go`, `internal/api/agent_live_test.go`,
> `internal/cli/launcher_test.go`, `internal/channel/link_live_test.go`,
> `web/src/components/mention-suggestions.test.tsx`, `web/e2e/fit.spec.ts`).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Label rules (`LabelForFolder`, `AllocateLabel`) | unit (pure) | Every rule and clash of SESS-11; the 40-character bound; the fallback names; the remembered label | `internal/agents/labels_test.go` | `go test -count=1 ./internal/agents/...` |
| Address parser (`SessionLabel`) | unit (pure) | Every rule of SESS-21/22: boundaries, case, first match, invalid and over-long labels | `internal/delivery/session_test.go` | `go test -count=1 ./internal/delivery/...` |
| Migration `0027` and the session queries | integration (real SQLite) | Columns, view, CHECK, cascades, idempotent insert, fallback ownership, backlog exclusion | `internal/store/session_migration_test.go` | `go test -count=1 ./internal/store/...` |
| Presence (labels, races, takeover, reconnect) | unit (in-package) | Two labels from two connects; the race has one principal; takeover closes only the principal; the 10-minute memory | `internal/agents/presence_test.go` | `go test -count=1 ./internal/agents/...` |
| Gateway (parallel session, welcome, ack, feed, end) | integration (real SQLite + WebSocket) | No `identity_busy`; welcome labels; principal-only app/feed/cursor; a labelled ack never moves the AI cursor | `internal/agents/gateway_live_test.go`, `internal/api/agent_sessions_test.go` | `make test` |
| Routing and the fallback | integration (testkit, a fake or real session) | One session per message; the fallback row; no double consumption; the note; edits and deletes; paused rooms | `internal/delivery/dispatcher_test.go`, `internal/agents/session_delivery_test.go` | `make test` |
| Sessions addressing each other (D11) | integration (testkit, two real sockets) | The three examples (principal → sibling, session → principal refused, session → own label refused); `trusted` true; no fallback; edits and deletes; paused rooms | `internal/delivery/dispatcher_test.go`, `internal/agents/session_delivery_test.go` | `make test` |
| Worktree tools-only reader | integration + unit (fake API server) | The empty pending page consumes nothing and answers at once; the tool answer says the sentence once and never asks the server | `internal/api/agent_pending_test.go`, `internal/channel/pending_note_test.go` | `make test` |
| Author label | integration (testkit) | The live label, the worktree label, the principal, a user, an edit | `internal/api/agent_sessions_test.go` | `make test` |
| Profile, members, `GET /agent/session` | e2e (testkit, real app on `httptest`) | Two sessions listed; a human member has none; the auth of the routes is unchanged | `internal/api/intelligences_test.go`, `internal/api/rooms_handlers_test.go` | `make test` |
| CLI worktree detection and launcher | unit + e2e (fake git runner, fake stdio) | The two rev-parse outputs, the first worktree, a timeout; the header; no prompt; the notice | `internal/channel/worktree_test.go`, `internal/cli/launcher_test.go` | `go test -count=1 ./internal/cli/... ./internal/channel/...` |
| Web message, profile and autocomplete | unit (Vitest + Testing Library + vitest-axe) | The `via` chip; the session list; one candidate per label; the inserted text; the live invalidation | `web/src/components/message-item.test.tsx`, `web/src/components/mention-suggestions.test.tsx`, `web/src/routes/_app/contacts.test.tsx` | `cd web && bun run test` |
| Web end-to-end | e2e (Playwright, real binary, 1280 + 320/412 px) | Two real sessions; the label routing; the profile and the chip at 125% font; the autocomplete | `web/e2e/ai-sessions.spec.ts`, `web/e2e/fit.spec.ts` | `make e2e` |
| Docs | none (build gate) | No takeover prompt or `identity_busy` instruction remains; the headers, `author_label`, `for_label` and the merge cleanup are documented | `docs/agents.md`, `docs/api.md`, `README.md` | `make check` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | While iterating on a Go package | `go test -count=1 ./internal/agents/...` or `./internal/delivery/...` or `./internal/store/...` |
| Quick (web) | While iterating on the web app | `cd web && bun run test` |
| Full | Before an integration or channel change is done | `make test` |
| Build | **Before every commit** (AGENTS.md, L-029) | `make check` |
| E2E | T14 | `make e2e` |

Run `make generate` after touching SQL queries, migrations or `internal/api/dto`; commit the generated
files with the change. `make check` builds the web app, so the sessions UI reaches the embedded app.

## Execution Plan

The phases are ordered. Tasks inside a phase run in order on one worker; phases inside one group are
independent and run in parallel worktrees. Each phase merges back before the next group starts. Never
hand-merge generated files (`internal/store/db/*`, `web/src/lib/api/types.gen.ts`): after a merge run
`make generate`, commit the result with the merge, then run `make check`.

| Group | Phases | Starts after |
| --- | --- | --- |
| G0 | Phase 1 (T1 and T2 in parallel) | — |
| G1 | Phase 2 (T3 and T4 in parallel) | G0 merged |
| G2 | Phase 3 (T5) | G1 merged |
| G3 | Phase 4 (T6) | G2 merged |
| G4 | Phase 5 (T7) | G3 merged |
| G5 | Phase 6 (T8) | G4 merged |
| G6 | Phase 7 (T9) | G5 merged |
| G7 | Phase 8 (T10) | G6 merged |
| G8 | Phase 9 (T11) | G7 merged |
| G9 | Phase 10 (T12) | G8 merged |
| G10 | Phase 11 (T15 and T16 in parallel) | G9 merged |
| G11 | Phase 12 (T13 and T14 in parallel) | G10 merged |

Dependency graph (every arrow is a `Depends on` of the task it points to):

```
T2 -> T3
T1 -> T8
T1 -> T9
T2 -> T5
T3 -> T8
T4 -> T6
T4 -> T9
T4 -> T16
T5 -> T6
T6 -> T7
T6 -> T8
T6 -> T9
T6 -> T10
T7 -> T13
T8 -> T13
T8 -> T15
T8 -> T16
T9 -> T11
T9 -> T13
T9 -> T15
T9 -> T16
T10 -> T12
T10 -> T13
T11 -> T14
T12 -> T14
T15 -> T13
T16 -> T13
```

### Phase 1: Store and rules

T1 lands the migration and the queries every later Go task writes to; T2 lands the pure label rules and
the kernel constants the parser and the presence read. They share no hand-written file and run in
parallel.

### Phase 2: The parser and the repository's identity

T3 is the pure address parser (it reads the constants T2 adds). T4 is the CLI + server vertical slice of
D1 (the worktree helper, the launcher variable, the header and the server key); it does not need the
parser and runs in parallel.

### Phase 3: Presence holds labels

T5 turns `Presence` into a map of labelled sessions while keeping the old single-session methods for the
principal, so the gateway keeps compiling and its busy tests keep passing until T6.

### Phase 4: A parallel session on the wire

T6 switches the gateway, the registry, the DTO and `GET /agent/session` to the labelled attach: no
`identity_busy`, the welcome carries the session, a labelled ack never moves the AI cursor. It also
updates the API tests that assert the old refusal.

### Phase 5: The clients present a session

T7 removes the launcher's takeover prompt and gives the channel the one-line notice and the identity
sentence. It is the first commit a person can see from a terminal.

### Phase 6: Routing

T8 is the largest task: the dispatcher computes the target, the gateway pushes to the label or to the
principal with the note, the delivery rows and the fallback exist, and backlog/pending merge the
fallback. Its tests are the integration of T1, T3 and T6. D11's sibling target and the worktree
tools-only reader are split into T15 and T16 so this task stays one commit.

### Phase 7: Authorship

T9 carries the label into `messages.author_label` through the rooms service, the API post paths and the
channel's `X-Partyline-Live`, and exposes it in the message DTO.

### Phase 8: Presence surfaces

T10 adds the session lists to the profile, the room members and `GET /agent/session`, and publishes the
presence event on every session change.

### Phase 9: The web shows the author's session

T11 is the `via :label` chip and its i18n/test.

### Phase 10: The web lists and mentions sessions

T12 adds the profile section, the autocomplete candidates and the inserted `@handle:label `, and
invalidates the cached profile on the presence event.

### Phase 11: Coordination and the tools-only reader

T15 is D11: the dispatcher adds the one target `Plan` excludes (the author AI's live sibling), the stored
`author_label` decides who wrote the message, and the gateway never hands the AI's own message back to
it. T16 is the worktree tools-only reader: its pending page is empty and consumes nothing, and its tool
answer says why once. The two touch disjoint files and run in parallel; both need T8's delivery rows and
T9's author label.

### Phase 12: Docs and end-to-end

T13 documents the headers, the routing, the author label, the sibling rule, the worktree tools-only
reader and the manual merge, records AD-032 and amends SES-01..SES-08. T14 drives the real binary with
Playwright: two real sessions, the labelled routing, the `via` chip, the profile and the autocomplete,
and the 320/412 px fit at 125% font. They touch disjoint files and run in parallel.

---

## Task Breakdown

### Phase 1: Store and rules

#### T1: Store the author's session and the per-session delivery rows [store]

**What**: migration `0027_ai_sessions.sql` exactly as the design's [Data model](../ai-sessions/design.md#data-model):
`messages.author_label` (`ADD COLUMN`, default `''`), the `agent_session_deliveries` table with its two
indexes and the label CHECK, and the `message_views` recreation that exposes `author_label`. The sqlc
queries in `internal/store/queries/session.sql` (`CreateSessionDelivery`, `DeleteSessionDelivery`,
`AckSessionDelivery`, `FallBackSessionDeliveries` with its `NOT EXISTS` clause for a message the AI
wrote, `DeleteSelfSessionDeliveries`, `ListSessionFallbacks`, `ListSessionDeliveriesByLabel`) and the
`NOT EXISTS` clause added to `CountAgentBacklog`, `ListAgentBacklog` and `ListAgentPending` in
`internal/store/queries/agents.sql`. Regenerate the sqlc output.

**Where**: `internal/store/migrations/0027_ai_sessions.sql`
**Also**: `internal/store/queries/session.sql`, `internal/store/queries/agents.sql`,
`internal/store/session_migration_test.go`, `internal/store/db/*` (generated)
**Depends on**: None
**Requirement**: SESS-24, SESS-28, SESS-30, SESS-37, SESS-38
**Tests first** (`internal/store/session_migration_test.go`):
- `TestMigration0027AddsTheSessionLabelAndTheDeliveryRows`: open a database migrated to `0026`
  (`openMigratedBefore(t, "0027")`), insert a user, an AI, a group, a message and a member; apply
  `0027_ai_sessions.sql`; the message's `author_label` is `''`; `PRAGMA table_info(messages)` has
  `author_label TEXT`; the view returns `author_label` for the agent message (`''`) and for a new message
  inserted with `'claude-2'`; `agent_session_deliveries` has exactly `message_id, agent_id, label,
  created_at, fell_back_at`; `INSERT` with `label = ''` fails the CHECK; deleting the message leaves no
  row; deleting the AI leaves no row.
- `TestSessionDeliveryIsIdempotentAndOwnedOnce`: a message and an AI; two `CreateSessionDelivery` calls
  for it (the second with a `fell_back_at`) → one row and `fell_back_at` is NULL (the first owner wins);
  `AckSessionDelivery` → 1 row deleted and `agent_cursors` is still `0`; insert it again and
  `FallBackSessionDeliveries(agent, "claude-2", 100)` returns `[message_id]` with `fell_back_at = 100`; a
  second call returns none; `AckSessionDelivery` now deletes 0 rows; a second message inserted with
  `fell_back_at = 50` has that value and `DeleteSessionDelivery` deletes it.
- `TestAnAIMsOwnMessageNeverFallsBack`: a message the AI itself wrote (`author_kind 'agent'`,
  `author_id` the AI, `author_label 'claude-2'`) with a pending row for `claude-2`;
  `FallBackSessionDeliveries(ai, "claude-2", 100)` returns none and `fell_back_at` stays NULL;
  `DeleteSelfSessionDeliveries(ai, "claude-2")` deletes 1 and a second call deletes 0; a user-authored
  message of the same label still falls back; a row of the label `wt` is untouched by a delete of
  `claude-2`.
- `TestFallbacksArePerRoomAndOrdered`: one AI, two rooms, rows for `:wt` in each (one fallen back), a new
  message without a row; `ListSessionFallbacks(agent, room1)` returns only the fallen-back message of
  `room1`, by `seq`; the new message is in `ListAgentBacklog` and the fallen-back one is not; a message
  with a pending (not fallen-back) row is in neither list.
**Done when**:
- [x] the migration passes on a database that existed before `0027` and on a fresh one
- [x] the three backlog/pending queries exclude every delivery row for the AI (pending and fallen back)
- [x] a row of a message the AI wrote is deleted by `DeleteSelfSessionDeliveries`, never marked
      `fell_back_at`
- [x] `make generate` ran and the sqlc output is committed
- [x] no Go hook is added (pure SQL)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add the session label and the per-session delivery rows`

#### T2: Name sessions and allocate their labels [agents]

**What**: the pure label module `internal/agents/labels.go` with `LabelForFolder` and `AllocateLabel` as in
the design's [Labels](../ai-sessions/design.md#labels), and the kernel constants
`dto.MaxSessionLabel = 40` and `dto.SessionLabelPattern = "^[a-z0-9-]{1,40}$"` in
`internal/api/dto/common.go`. No I/O, no clock, no store; `cleanHarness`/`folderBase` stay where they are.

**Where**: `internal/agents/labels.go`
**Also**: `internal/api/dto/common.go`, `internal/agents/labels_test.go`
**Depends on**: None
**Requirement**: SESS-11, SESS-21
**Tests first** (`internal/agents/labels_test.go`):
- `TestLabelForFolder`: `/repo/wt` → `wt`; `/repo/Acme API!` → `acme-api`; `/repo/___` → `session`;
  `/repo/` + 50 `a`s → 40 `a`s; `/repo/Acme_2` → `acme-2`; `C:\work\Acme_2` → `acme-2`; `/` → `session`;
  `/repo/claude-2` → `claude-2`; `/repo/a--b` → `a-b`.
- `TestAllocateLabel`: free principal → `""`; preferred `wt` free → `wt`; preferred `wt` taken with
  harness `claude` → `claude-2`; `claude-2` taken → `claude-3`; principal taken → `claude-2`; empty
  harness → `agent-2`; a 40-character preferred taken → `<harness>-2` at most 40 characters; a remembered
  label free → it even when the preferred is different; a remembered label taken → the next free one; a
  worktree preferred `claude-2` with `claude-2` taken → `claude-3`.
- `TestSessionLabelPattern`: `wt`, `claude-2` and 40 `a`s match; `""`, `Wt`, `claude_2`, `-x` and 41
  `a`s do not.
**Done when**:
- [x] every example of the design's label table is a case with a literal expected string
- [x] the file imports only `fmt`, `strings` and `internal/api/dto`
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(agents): name sessions and allocate their labels`

### Phase 2: The parser and the repository's identity

#### T3: Parse the session a message addresses [delivery]

**What**: the pure `SessionLabel(text, handle string) string` from the design's [The
parser](../ai-sessions/design.md#the-parser), reusing the word-boundary idea of
`containsBounded`/`continuesHandle` (`internal/delivery/wants.go:102-134`) without changing `Wants`.

**Where**: `internal/delivery/session.go`
**Also**: `internal/delivery/session_test.go`
**Depends on**: T2
**Requirement**: SESS-21, SESS-22
**Tests first** (`internal/delivery/session_test.go`):
- `TestSessionLabel`: with handle `atlas`: `"hi @atlas:claude-2"` → `claude-2`; `"@atlas"` → `""`;
  `"@atlas:"` → `""`; `"@atlas:Bad"` → `""`; `"@atlas:claude-2x"` → `claude-2x`;
  `"@atlas:claude-2."` → `claude-2`; `"@atlas:Bad @atlas:wt"` → `wt`;
  `"@atlas:claude-2 @atlas:wt"` → `claude-2` (first wins); `"@atlas-two:x"` → `""`;
  `"x@atlas:wt"` → `""`; `"(@atlas:wt)"` → `wt`; `"@ATLAS:wt"` → `wt`; `"@ATLAS:WT"` → `""`;
  a label of 41 `a`s → `""`; `""` and a `""` handle → `""`.
- `TestSessionLabelLeavesAPlainMentionAlone`: `"@atlas hello"` and `"@atlas, hello"` → `""`, so a plain
  mention still routes to the principal.
- `TestWantsStillCountsALabelledMention`: `Wants` with the `mentions` mode and `@atlas:wt` → true (the
  colon ends the handle); no change to `wants.go` is asserted by the existing suite.
**Done when**:
- [x] the parser is case-insensitive on the handle and case-sensitive on the label (lowercase only)
- [x] the file has no I/O and no import of `store`, `events`, `api` or `net/http`
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(delivery): parse the session a message addresses`

#### T4: Fold a linked worktree into the repository's AI [channel]

**What**: the CLI + server vertical slice of D1, as in the design's [Identity: the
repository](../ai-sessions/design.md#identity-the-repository-d1): the `internal/channel` worktree helper
(the two `rev-parse` calls, the first `worktree list` entry, a one-second timeout, an injectable runner),
`channel.Identity.MainFolder` and the `X-Partyline-MainFolder` header (percent-encoded),
`PARTYLINE_MAIN_FOLDER` in the launcher/`launcherVars`/`consumedVars` and in `sessionIdentity` (computed
when absent, never when `PARTYLINE_LAUNCH_PARENT` is set), and the server's `FolderID.MainFolder` +
`ValidateFolder` + the `key` in `ResolveFolder`/`LookupFolder`/`RegisterFolder`/`linkOrCreate`/
`adoptionCandidate`. A main folder equal to the folder is absent; a bad one is 422 `folder_invalid` on
`main_folder`. The tools-only process resolves the same AI and posts with its folder's label (SESS-06);
what it reads is T16's rule.

**Where**: `internal/channel/worktree.go`
**Also**: `internal/channel/worktree_test.go`, `internal/channel/identity.go`,
`internal/cli/launcher.go`, `internal/cli/procenv.go`, `internal/cli/channel.go`,
`internal/api/dto/common.go`, `internal/agents/folder.go`, `internal/agents/folder_test.go`,
`internal/testkit/agent.go`
**Depends on**: None
**Requirement**: SESS-01, SESS-02, SESS-03, SESS-04, SESS-05, SESS-06, SESS-07, SESS-09
**Tests first**:
- `internal/channel/worktree_test.go` (fake runner): different `--git-common-dir`/`--git-dir` outputs and a
  `worktree list` whose first entry is `/repo` → main folder `/repo`; equal outputs → `""`; a first block
  with `bare` → `""`; a runner error, a timeout and a missing `git` → `""`; a submodule layout (equal git
  dirs) → `""`; the returned folder is canonical (symlinks resolved).
- `internal/agents/folder_test.go`: `FolderID{Machine, Folder:"/repo/wt", MainFolder:"/repo"}` resolves
  the AI of `/repo` (create on first sight with `folder = "/repo"` and the handle of `/repo`'s base); a
  second resolve from `/repo/other-wt` with the same main folder returns the same id; `MainFolder` equal
  to `Folder` resolves as without it; a main folder with a trailing `/` or a relative path is 422
  `folder_invalid` with field `main_folder`; `LookupFolder` uses the same key.
- `internal/cli/launcher_test.go`: the child environment has `PARTYLINE_MAIN_FOLDER=/repo` for a
  worktree launch and not for a `--parent` launch; `sessionIdentity` reads it and does not run the helper
  when it is set; `partyline listen` in a worktree sends the header (the fake API records it).
**Done when**:
- [ ] no server code runs git; the only new `os/exec` use is in `internal/channel/worktree.go` (the
      launcher already runs the harness, `internal/cli/launcher.go:21`)
- [ ] the worktree command has a one-second bound and never fails the launch
- [ ] the AI's `agents.folder` is the main folder and its default name/handle derive from it
- [ ] a tools-only process in a linked worktree resolves the repository's AI and posts with the worktree
      label (its empty pending list is T16)
- [ ] Gate check passes: `make check`
**Tests**: unit + integration
**Gate**: build
**Commit**: `feat(identity): one AI for the repository's worktrees`

### Phase 3: Presence holds labels

#### T5: Hold several labelled sessions per AI [agents]

**What**: `Presence`'s map becomes `agentID → label → presenceEntry`, with `Live{Label, Harness, Since}`,
`AttachLabeled` (same-process replacement, principal-only takeover, `AllocateLabel` under the mutex,
`fresh`), `Sessions`, `SessionByLabel`, `Main`, the `retired` 10-minute memory (prune, restore), `Detach`
freeing the label, `Kick` closing every label, and the online/offline `events.AgentPresence` transitions
preserved. The existing `Attach`, `Busy`, `Live`, `Session`, `Online`, `Kick`, `Detach` signatures keep
working on the principal so the gateway is untouched in this commit (`Attach` = `AttachLabeled` with
preferred `""` and today's `ErrBusy` behaviour, `Busy` looks at the principal).

**Where**: `internal/agents/presence.go`
**Also**: `internal/agents/presence_test.go`, `internal/agents/labels.go` (read only, T2)
**Depends on**: T2
**Requirement**: SESS-10, SESS-11, SESS-12, SESS-13, SESS-14, SESS-19
**Tests first** (`internal/agents/presence_test.go`, one fake `Session` recording close code/reason and
an injected clock):
- `TestPresenceGivesEachSessionItsLabel`: attach `""` then `claude-2` then `claude-3` from three different
  live ids and the same harness; `Sessions` is `[{Label:""},{Label:"claude-2"},{Label:"claude-3"}]`;
  `Online` true; detaching `claude-2` frees it and the next attach with preferred `claude-2` gets it.
- `TestTwoConnectionsRaceForThePrincipal`: two goroutines with different live ids and preferred `""`;
  the labels are exactly `{"" , "claude-2"}` (in some order) and no error is returned.
- `TestTakeoverReplacesOnlyThePrincipal`: `""` live, a takeover attach with preferred `""` → the old
  principal's fake recorded `4002/taken_over` and the new label is `""`; a takeover attach with preferred
  `wt` → nothing is closed and the label is `wt`; the principal is still live.
- `TestReconnectKeepsTheLabel`: attach with `liveID "P"` → `claude-2`; detach; attach again with `liveID
  "P"` before the clock advances 10 min → `claude-2` and the same `since`; advance the clock 11 min →
  a new attach gets `claude-2` only because it is free (no memory), and with another session holding it
  gets `claude-3`.
- `TestKickClosesEverySession`: two live; `Kick(4002, "ended by @lead")` → both fakes recorded it and
  `Online` is false; `events.AgentPresence` was published `Online:true` once and `Online:false` once.
- `TestSameProcessReplacesItsSocket`: same live id twice → the old fake recorded `1000/reconnected`, the
  label and `since` are unchanged, `Sessions` has one entry.
**Done when**:
- [x] every label allocation happens under the presence mutex (one `p.mu.Lock` for check + insert)
- [x] the old single-session methods still behave as today for the principal, so the existing gateway
      tests pass unchanged
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(agents): hold several labelled sessions per AI`

### Phase 4: A parallel session on the wire

#### T6: Connect a second session instead of refusing it [agents]

**What**: the gateway uses `AttachLabeled`: the pre-upgrade `Presence.Busy` check and the `busyError`
are removed; `handshake` computes the preferred label (`""` when the session folder equals the AI's
folder, else `LabelForFolder`); `conn` carries its `label`; `Registry.Attach` returns the assigned
`Live` and sets `app`/`connected_at` only for the principal; the welcome frame carries
`Session dto.SessionInfo`; a labelled ack does not move the AI cursor; a labelled connection gets no
platform feed; `Gateway.Close`/end-of-connection frees the label; `GET /agent/session` gains
`Sessions`. Remove `Presence.Busy`, `ErrBusy`, `CloseBusy`, `ReasonBusy` and the `identity_busy` branches
the server no longer needs; keep the DTO constant. Update the tests that asserted the refusal.

**Where**: `internal/agents/gateway.go`
**Also**: `internal/agents/folder.go`, `internal/agents/presence.go`, `internal/api/dto/agents.go`,
`internal/api/agent_handlers.go`, `internal/api/agent_live_test.go`,
`internal/agents/gateway_live_test.go`
**Depends on**: T4, T5
**Requirement**: SESS-10, SESS-13, SESS-15, SESS-17, SESS-18, SESS-19
**Tests first**:
- `internal/agents/gateway_live_test.go`: two dials with different live ids of one folder → both upgrade
  and the welcomes carry `session.label ""` and `claude-2`; a second dial of a worktree folder (`Folder`
  `/repo/wt`, main folder `/repo`) → `session.label "wt"`; the old "the second connection is closed
  4003" test becomes "the second connection is a parallel session"; a takeover dial closes the old
  principal with `4002` and its welcome label is `""`; the AI's `harness` after a parallel `pi` attach is
  still `claude` and `connected_at` did not move; a labelled session's ack of a message id leaves the
  AI's `agent_cursors` row absent or `0`.
- `internal/api/agent_live_test.go`: the 409 `identity_busy` expectations become two online sessions;
  `GET /agent/session` returns `online: true`, the principal's `app`/`connected_at`, and
  `sessions: [{label:""},{label:"claude-2"}]`; an anonymous caller still 401 and a user token 403.
- `internal/agents/gateway_test.go`: a session with `FeaturePlatform` and a second without → only the
  principal receives a queued platform event (the feed is built for the principal only).
**Done when**:
- [ ] no code path answers 409 `identity_busy`; `grep -rn identity_busy internal` shows only the DTO
      constant and its detail keys and the channel's compatibility branch
- [ ] the welcome of every connection carries a session; the principal's label is `""`
- [ ] a labelled connection never writes `agents.harness` or `agents.connected_at`
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(agents): connect a second session instead of refusing it`

### Phase 5: The clients present a session

#### T7: Announce a parallel session instead of asking [channel]

**What**: the launcher no longer prompts: `askTakeover`, `takeoverPrompt`, `takeoverChoice` and their
wiring go; `takingOver = opts.takeOver || PARTYLINE_TAKEOVER=1`; `lookupSession` stays for the placement
refusal and `announceToolsOnly`. The channel reads the welcome's `session`: for a non-empty label it
prints `partyline: opened as parallel session <label>` to stderr once and adds the sentence
`This session is :<label>.` to the identity notice; the principal prints neither. The channel's
`outBusy`/busy texts stay as a compatibility branch (a 409 from an older server), and the SES-04 retry
loop is kept but is never entered against a new server.

**Where**: `internal/cli/launcher.go`
**Also**: `internal/channel/link.go`, `internal/channel/identity.go` (read only, T9 owns the live id),
`internal/channel/link_live_test.go`, `internal/cli/launcher_test.go`
**Depends on**: T6
**Requirement**: SESS-15, SESS-16
**Tests first**:
- `internal/cli/launcher_test.go`: with a live session on the server, `partyline claude` starts the
  harness (exit 0, the fake exec recorded the call) and asks nothing on stdin; with `--take-over` the
  child env has `PARTYLINE_TAKEOVER=1`; a `--parent` launch still checks the placement and exits 1 on a
  refusal (the existing test).
- `internal/channel/link_live_test.go`: a welcome with `session.label "claude-2"` → the fake notifier got
  the identity notice once with `This session is :claude-2.` and stderr contains
  `partyline: opened as parallel session claude-2` once across two connections of the same process; a
  welcome with `session.label ""` → neither; a 409 `identity_busy` still enters the busy branch (the
  compatibility test).
- `internal/channel/identity_test.go`: `mainFolder` is sent percent-encoded only when non-empty (the T4
  test owns the header; this task only asserts the notice/identity text).
**Done when**:
- [ ] the launcher never reads stdin for a takeover answer
- [ ] a second `partyline claude` in one folder starts (no exit code 1)
- [ ] the old-channel busy path still compiles and is covered by one test
- [ ] Gate check passes: `make check`
**Tests**: unit + integration
**Gate**: build
**Commit**: `feat(channel): announce a parallel session instead of asking`

### Phase 6: Routing

#### T8: Route a message to the session it addresses [delivery]

**What**: the largest task, exactly as the design's [Addressing](../ai-sessions/design.md#addressing):
`AgentPusher.Push`/`PushUpdate` gain the target label; `AgentDispatcher.dispatch` computes
`SessionLabel(text, handle)` and `dispatchUpdate` uses the before/after targets deduped; `Gateway.Push`
resolves the label and records a `CreateSessionDelivery` row — pending when the label is live, born
fallen back when it is not — then pushes to the label, or to the principal with `ForLabel`, or returns
false and leaves the row/queue for later; `Gateway.PushUpdate` pushes to the targeted live session or to
the principal when the label has no live session; the end-of-session sweep (after `Presence.Detach`
reported that the connection was the current one: `FallBackSessionDeliveries ... RETURNING`, then push
each to a live principal with `for_label`); the principal's ack deletes a row, a labelled ack only deletes
an un-fallen-back row;
`Gateway.backlog` and `Gateway.Pending` merge `ListSessionFallbacks` by `seq` with the cursor messages,
never apply the delivery filter again to a fallback row, and recompute `trusted`;
`dto.AgentMessage.ForLabel`; the channel's `Format` renders the note and meta `for_label`. The dispatcher
skips a message for a labelled session when the label is not live by pushing it to the principal (with
the note) — the same call. `PushUpdate` pushes to a live target and to the principal when the target
has no live session, so an edit or a delete after a session end is not lost.

**Where**: `internal/delivery/dispatcher.go`
**Also**: `internal/agents/gateway.go`, `internal/agents/session_delivery.go` (new: the row helpers, the
sweep and the trusted load), `internal/agents/pending.go`, `internal/api/dto/agents.go`,
`internal/channel/format.go`, `internal/channel/format_test.go`, `internal/delivery/dispatcher_test.go`,
`internal/agents/session_delivery_test.go`, `internal/agents/gateway_backlog_test.go`,
`internal/api/agent_pending_test.go`
**Depends on**: T1, T3, T6
**Requirement**: SESS-20, SESS-21, SESS-22, SESS-23, SESS-24, SESS-25, SESS-26, SESS-27
**Tests first** (`testkit`, two live sessions of one AI, a recording `Sender`/pusher where the test does
not use the real gateway):
- `internal/delivery/dispatcher_test.go`: a group with an AI whose handle is `atlas` and a user author;
  `"hello"` → 0 pushes to `:claude-2` and 1 to the principal; `"@atlas:claude-2 hi"` → 1 to
  `:claude-2` and 0 to the principal; `"@atlas:claude-2 and @atlas:wt"` → the first label only; a DM with
  `"@atlas:claude-2 hi"` → `:claude-2`; a paused room → 0 for both; a plain message authored by the AI →
  0 for its own sessions and 1 for the other AI member (D11's labelled sibling case is T15).
- `internal/agents/session_delivery_test.go` (real app, real store, two sockets):
  `@atlas:claude-2 hello` → the `:claude-2` socket got one message frame and a row exists; the principal
  got none; the `:claude-2` socket closes without ack → the row's `fell_back_at` is set and the principal
  receives the same message once with `for_label "claude-2"`; the principal acks it → the row is gone;
  a late ack from the closed socket does not delete it (assert the row before the principal's ack);
  `@atlas:claude-2 hi` with `:claude-2` offline → the principal receives it with `for_label` and a row
  born with `fell_back_at` set; with the principal also offline, the row stays and the principal's
  backlog/pending delivers it with the note (the next test file); a message with no label and an offline
  principal stays in its backlog and is not pushed to `:claude-2`; an edit of the message after
  `:claude-2` closed → the principal receives `message_edited` (the target has no live session).
- `internal/agents/gateway_backlog_test.go`: a fallen-back message below the principal's cursor is
  delivered by the backlog with `for_label` and the live-push `sent` map suppresses a late duplicate; a
  pending row for a live label is not in the principal's backlog.
- `internal/api/agent_pending_test.go`: `GET /agent/pending` of the principal returns the fallen-back
  message with `for_label` and consumes it (the second call does not return it); a pending row of a live
  label is not returned.
- `internal/channel/format_test.go`: a message with `ForLabel "wt"` → content starts
  `(addressed to :wt)\n` and meta `for_label=wt`; without it the content and meta are as today.
**Done when**:
- [ ] a message is owned by exactly one of (a live labelled session, the principal's queue) at any time
- [ ] the fallback and the ack are race-safe (T1's `RETURNING` + conditional delete) and covered by a
      test that acks after the sweep
- [ ] a plain message the AI wrote is not delivered to any of its own sessions (T15 adds the labelled
      exception)
- [ ] the delivery mode, the loop guard and the echo rule are unchanged for the principal
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(delivery): route a message to the session it addresses`

### Phase 7: Authorship

#### T9: Record which session wrote a message [rooms]

**What**: `messages.author_label` through the whole write path and the wire: `rooms.PostInput.SessionLabel`
and `draft.authorLabel`; `CreateMessage` stores it; `rooms.Message`/`message_views`/`dto.Message.AuthorLabel`
and `convert.Message` expose it; `(*Registry).SessionLabelOf(ctx, agentID, liveID, sessionFolder)` (presence
by `X-Partyline-Live`, else `LabelForFolder` of a folder different from the AI's, else `""`); the API fills
it for agent principals in `postTo` and the two agent post handlers; the channel's tool client sends
`X-Partyline-Live` (from the link's live id) on every call; an edit or delete keeps the label. Regenerate
the sqlc and tygo outputs.

**Where**: `internal/rooms/messages.go`
**Also**: `internal/api/dto/rooms.go`, `internal/api/convert/convert.go`,
`internal/api/rooms_handlers.go`, `internal/api/agent_handlers.go`, `internal/agents/presence.go`
(the live-id lookup), `internal/channel/client.go`, `internal/channel/identity.go`,
`internal/channel/run.go`, `internal/channel/client_test.go`, `internal/rooms/messages_test.go`,
`internal/api/agent_sessions_test.go`,
`internal/store/queries/rooms.sql`, `internal/store/db/*` (generated),
`web/src/lib/api/types.gen.ts` (generated)
**Depends on**: T1, T4, T6
**Requirement**: SESS-28, SESS-29, SESS-30, SESS-31
**Tests first**:
- `internal/api/agent_sessions_test.go` (testkit, two sessions live): a post with the `:claude-2` live id
  → `author_label "claude-2"`; the principal → `""`; a user post → `""`; a worktree tools-only post (no
  `HeaderLive`, folder `/repo/wt`, AI folder `/repo`) → `"wt"`; a post with a `HeaderLive` that matches no
  live session and no worktree → `""`; an edit of the labelled message keeps `"claude-2"`; the live
  `room.message` frame and `GET /rooms/{id}/messages` carry it; `author_label` is `''` for a message
  stored before the feature.
- `internal/rooms/messages_test.go`: `PostInput{SessionLabel:"wt"}` from an agent principal stores
  `author_label`; a user principal ignores the field; `PostFromChat` stores `''`.
- `internal/channel/client_test.go`: `Identity{LiveID:"P"}` sends `X-Partyline-Live: P` on a tool call;
  `run.go` gives the client the link's live id (asserted through the recorded request).
- `internal/api/convert/convert_test.go`: `AuthorLabel` is copied, empty stays empty.
**Done when**:
- [ ] the label is derived only from the live-id/folder rule, never from the text (a test posts
  `"@me:fake"` from the principal and asserts `author_label ""`)
- [ ] users, chats and system notices store `''`
- [ ] `make generate` ran and the generated files are committed
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms): record which session wrote a message`

### Phase 8: Presence surfaces

#### T10: List the live sessions in the profile and in rooms [api]

**What**: `dto.AIDetail.Sessions` and `dto.RoomMember.Sessions` (the `dto.SessionInfo` type and
`dto.SessionState.Sessions` come from T6), `agents.Registry.Sessions`, the fills in `toProfile` and
`toMember`, and `Presence` publishing `events.AgentPresence` on every attach and detach (Online = any
live) so an open profile and an open autocomplete refresh. No new route.

**Where**: `internal/api/rooms_handlers.go`
**Also**: `internal/api/intelligences_handlers.go`, `internal/api/dto/intelligences.go`,
`internal/api/dto/rooms.go`, `internal/agents/registry.go`, `internal/agents/presence.go`,
`internal/api/agent_sessions_test.go`, `internal/api/rooms_handlers_test.go`
**Depends on**: T6
**Requirement**: SESS-32, SESS-33
**Tests first**:
- `internal/api/agent_sessions_test.go`: an AI with a principal `claude` and a parallel `pi`; the
  profile's `ai.sessions` is `[{label:"",harness:"claude",connected_at:<t1>},
  {label:"claude-2",harness:"pi",connected_at:<t2>}]` with `t1 < t2`; a profile with no live session has
  `sessions: []`; closing `:claude-2` leaves `[{label:""}]`. (`GET /agent/session`'s two sessions are
  T6's test; this task keeps its `sessions` list filled once T6 defined the field.)
- `internal/api/rooms_handlers_test.go`: `GET /rooms/{id}/members` gives the agent member
  `online: true` and two sessions, a human member no `sessions`, and a chat member no `sessions`; the
  route's auth (401/404) is unchanged.
- `internal/agents/presence_test.go`: two attaches publish two `events.AgentPresence` with `Online:true`
  and the offline event only when the last detaches (the transition test moves here from T5).
**Done when**:
- [ ] the principal is always first and the list is never nil on the wire (an empty array, not `null`)
- [ ] no new route and no new permission: whoever sees the AI or the room sees its sessions
- [ ] `make generate` ran if a DTO changed the TypeScript (committed)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): list an AI's live sessions in its profile and rooms`

### Phase 9: The web shows the author's session

#### T11: Show which session wrote a message [web]

**What**: the `via :label` chip in the author header (`web/src/components/message-item.tsx`), only for
`author_kind === "agent"` with a non-empty `author_label`; it is a muted inline span that wraps, is in the
grouped row's screen-reader text, and has no link or menu. The i18n keys `message.viaSession` in
`en.ts`/`pt-BR.ts`; the unit test.

**Where**: `web/src/components/message-item.tsx`
**Also**: `web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`,
`web/src/components/message-item.test.tsx`, `web/src/components/message-timeline.test.tsx`,
`web/src/test/fixtures.ts` (the `Message` fixture gains `author_label`)
**Depends on**: T9
**Requirement**: SESS-30, SESS-34
**Tests first** (`web/src/components/message-item.test.tsx`, Testing Library):
- an agent message with `author_label: "claude-2"` → `via :claude-2` is visible next to the name;
- the principal (`""`) → no `via` text;
- a user message → no `via` text even if a fixture sets a label;
- a grouped row (same author) still exposes the label to a screen reader (the `sr-only` text contains
  `claude-2`);
- a 40-character label renders inside the row (`getByText` finds it) and the row keeps `min-w-0` (a
  class assertion, or the fit e2e covers the layout).
- the i18n parity test (`web/src/lib/i18n/*.test.ts`) stays green.
**Done when**:
- [ ] the chip is only for agent messages with a label
- [ ] `bun run lint`, `bun run typecheck` and the web tests are clean
- [ ] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): show which session wrote a message`

### Phase 10: The web lists and mentions sessions

#### T12: List sessions in the profile and mention them [web]

**What**: `AIDetails`'s "Live sessions" section (principal/`:label`, the harness label, since, wrapping
rows, `data-testid="session-label"`); the mention autocomplete's `MentionCandidate{intelligence,label}`,
one candidate per live labelled session from `member.sessions`, matching by display name/handle/label,
the `:label` in the option and the inserted `@handle:label `; the live handler invalidating the profile
query on `agent.presence`; the i18n keys.

**Where**: `web/src/components/mention-suggestions.tsx`
**Also**: `web/src/components/composer.tsx`, `web/src/routes/_app/contacts.$id.tsx`,
`web/src/lib/live/handlers.ts`, `web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`,
`web/src/components/mention-suggestions.test.tsx`, `web/src/components/composer.test.tsx`,
`web/src/routes/_app/contacts.test.tsx`, `web/src/lib/live/handlers.test.ts`
**Depends on**: T10
**Requirement**: SESS-32, SESS-33, SESS-34
**Tests first**:
- `mention-suggestions.test.tsx`: members with an AI (`atlas`) and sessions `""` and `claude-2` →
  candidates `[{handle:"atlas",label:""},{handle:"atlas",label:"claude-2"}]`; `matchMentions("claude")`
  finds the second; `matchMentions("atlas")` finds both; the option renders `@atlas` and `:claude-2`; two
  AIs with the same label are two options; a human member adds only its own handle.
- `composer.test.tsx`: typing `@at` and picking the labelled option inserts `@atlas:claude-2 ` (with the
  trailing space) and the caret lands after it; picking the AI inserts `@atlas `; the message text sent
  is the inserted text.
- `contacts.test.tsx`: a profile with two sessions shows "principal", ":claude-2", the harness label and
  the since; a profile with none shows no section; the route's a11y audit passes.
- `live/handlers.test.ts`: an `agent.presence` event for a cached profile invalidates
  `["intelligences", id]` (or refetches it), and still patches the members' `online`.
**Done when**:
- [ ] the principal is offered as `@handle`, never as `@handle:` with an empty label
- [ ] no request is added to the composer: the members query already carries the sessions
- [ ] `bun run lint` and `bun run typecheck` are clean
- [ ] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): list sessions in the profile and mention them`

### Phase 11: Coordination and the tools-only reader

#### T15: Sessions address each other [delivery]

**What**: D11 exactly as the design's [Sessions addressing
sessions](../ai-sessions/design.md#sessions-addressing-sessions): `audienceOf` reads the stored
`message_views.author_label` and the author AI's handle, parses `SessionLabel(text, handle)` and adds one
`audience.self` target when the text names a label that is not the author's own; `dispatch` pushes it
after the plan with `trusted: true`, outside `Plan`'s author exclusion and outside the delivery mode,
while the loop guard still applies; `dispatchUpdate` adds the before/after sibling targets deduped;
`Gateway.Push` recognizes a message the AI wrote (`m.Message.AuthorKind`/`AuthorID`), never creates a
born-fallen-back row for it and never pushes it to the principal, and `PushUpdate` does the same; the
end-of-session sweep calls `DeleteSelfSessionDeliveries` next to the fallback. No DTO or wire change.

**Where**: `internal/delivery/dispatcher.go`
**Also**: `internal/agents/gateway.go`, `internal/agents/session_delivery.go`,
`internal/delivery/dispatcher_test.go`, `internal/agents/session_delivery_test.go`
**Depends on**: T8, T9
**Requirement**: SESS-37, SESS-38, SESS-39
**Tests first** (`testkit`, an AI live as `principal` and `:wt`, messages posted through a session's live
id so the stored `author_label` is real):
- `internal/delivery/dispatcher_test.go`: `@atlas:wt do X` posted by the principal → one push to the
author AI with target `wt` and `trusted: true`, and none to the principal; `@atlas do Y` posted by `:wt`
→ no push to any session of `atlas`; `@atlas:wt do Z` posted by `:wt` → none; `@atlas:gone do W` posted
by `:wt` → none; the same texts posted by a user → D3's normal path and the delivery mode still applies;
a paused room → no self push; a plain AI message → none (T8's case stays).
- `internal/agents/session_delivery_test.go`: two real sockets; the principal's `@atlas:wt do X` → the
`:wt` socket gets one frame with `trusted: true` and a pending row; `:wt` closes before ack →
`fell_back_at` stays NULL, the row is gone (deleted by the sweep) and the principal receives nothing;
`:wt` posts `@atlas do Y` → no frame on either socket; `:wt` posts `@atlas:wt do Z` → none; `:wt` posts
`@atlas:gone do W` → no row and no frame; the principal writes a plain message, edits it to add
`@atlas:wt` → `:wt` receives `message_edited` once, and deleting it reaches `:wt` with
`message_deleted` while the principal receives neither.
**Done when**:
- [ ] the author label is `message_views.author_label`, never derived from the socket on this path
- [ ] a message the AI wrote reaches neither the writing session's own label nor the principal as a
      fallback
- [ ] the loop guard suppresses every session in a paused room, including a self target
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(delivery): let sessions address each other`

#### T16: Read no queue in a worktree tools-only process [agents]

**What**: a process whose folder resolves to a non-empty label has no live session, so it reads no queue,
exactly as the design's [The principal's queue and the tools-only
reader](../ai-sessions/design.md#the-principals-queue-and-the-tools-only-reader) says. `agentPending`
resolves the request's session label with `SessionLabelOf(ctx, a.ID, liveID, folder)` (T9's helper) and
passes it to `Gateway.Pending`, which, when it is non-empty, returns an empty page — items, platform
events and dropped count — at once (no `wait`), without advancing a cursor or deleting a delivery row;
the principal-folder case is unchanged. The tools-only channel never asks the server from a linked worktree:
`wait_for_messages` answers `no new messages: This worktree session has no live channel: it only writes
and reads history.` the first time and `no new messages` after, and `ToolsOnlyInstructions` carries the
same sentence for that identity only.

**Where**: `internal/agents/pending.go`
**Also**: `internal/api/agent_handlers.go`, `internal/api/agent_pending_test.go`,
`internal/channel/pending.go`, `internal/channel/instructions.go`, `internal/channel/run.go`,
`internal/channel/pending_note_test.go`
**Depends on**: T4, T8, T9
**Requirement**: SESS-06
**Tests first**:
- `internal/api/agent_pending_test.go`: an AI with a live principal, a message of the principal's and a
  fallen-back row; a tools-only pending call with folder `/repo/wt` and main folder `/repo` → `items: []`,
  `platform: []`, `dropped: 0`, `agent_cursors` unchanged, the delivery row still there, and the answer
  immediate even with `wait=60`; the principal's next pending call returns the message once; a pending
  call with the AI's own folder → the message as today; `?platform=1` from the worktree takes no platform
  event.
- `internal/channel/pending_note_test.go`: a fake API server that records every call; a tools-only server
  whose identity has `Folder /repo/wt` and `MainFolder /repo` → `wait_for_messages` makes no request, its
  first answer contains `This worktree session has no live channel: it only writes and reads history.`
  and its second does not; an identity with `Folder /repo` and no main folder → asks the server and gets
  the items, without the sentence; an identity whose `MainFolder` equals its `Folder` → the same.
- `internal/channel/instructions_test.go`: a linked-worktree tools-only session receives
  `ToolsOnlyInstructions` plus the note, a principal-folder one receives `ToolsOnlyInstructions` alone.
**Done when**:
- [ ] a tools-only process in a worktree consumes nothing: no cursor moved, no delivery row deleted, no
      platform item or dropped count taken
- [ ] the principal's queue is unchanged for every other reader
- [ ] the note appears once per process and never for a principal-folder tools-only process
- [ ] Gate check passes: `make check`
**Tests**: integration + unit
**Gate**: build
**Commit**: `feat(agents): read no queue in a worktree tools-only process`

### Phase 12: Docs and end-to-end

#### T13: Document the labels, the routing and the manual merge [docs]

**What**: the operator and API documentation of the feature, and the decision record: the main-worktree
rule and the `X-Partyline-MainFolder` header, the labels and `@handle:label`, sessions addressing each
other (D11: a session reaches a live sibling, never itself, never a fallback), the worktree tools-only
reader (an empty pending list and the one-time note), the `X-Partyline-Live` author label, `author_label`
and `for_label`, the welcome `session`, the removed takeover prompt and `identity_busy`, and the manual
merge of old worktree AIs (end the old AI's sessions, then `POST /agents/{id}/merge`). Remove the
prompt/`identity_busy` instructions from `docs/agents.md:216-236, 641, 719`, `docs/claude-code.md:17` and
`README.md:172-174`. Record AD-032 in `.specs/STATE.md` and amend
`.specs/features/intelligences/spec.md` SES-01..SES-08 with the superseding SESS requirements (the
design's table). No behaviour change.

**Where**: `docs/agents.md`
**Also**: `docs/api.md`, `README.md`, `docs/claude-code.md`, `.specs/STATE.md`,
`.specs/features/intelligences/spec.md`
**Depends on**: T7, T8, T9, T10, T15, T16
**Requirement**: SESS-08, SESS-35, SESS-36
**Tests first**: none (documentation; `make check` is the gate). Verify by hand that every header, field,
close code and limit in `internal/api/dto` and the welcome frame of T6 appears in `docs/api.md`, that no
`Take over? [y/N]` or `identity_busy` instruction remains (`grep -rn "Take over?\|identity_busy" docs
README.md` returns nothing user-facing), and that AD-032's text matches the design's decision record.
**Done when**:
- [ ] a person upgrading from the previous release reads what happens to the old worktree AIs and how to
      merge each one
- [ ] `docs/api.md` documents `X-Partyline-MainFolder`, `X-Partyline-Live`, `author_label`, `for_label`,
      the welcome `session`, the sessions in the profile/members and the close codes
- [ ] the docs state that a session may address a live sibling with `@handle:label` (never itself, never
      a fallback), and that a worktree tools-only process reads no queue and says so once
- [ ] `.specs/STATE.md` gains AD-032 without changing any other decision (it amends AD-010)
- [ ] the intelligences spec's SES-01..SES-08 point at SESS-* and no longer describe the prompt
- [ ] Gate check passes: `make check`
**Tests**: docs (no code; `make check` only)
**Gate**: build
**Commit**: `docs(ai-sessions): document the labels, routing and the manual merge`

#### T14: Cover the two-session flow and the phone fit [web]

**What**: `web/e2e/ai-sessions.spec.ts` against the real binary: two real agent WebSockets of one folder
(`openSession` gains an optional live id and harness and returns the id it sent; the second gets
`claude-2`), a message posted through the second socket's live id, a message with `@atlas` and
`@atlas:claude-2` in the group. It asserts the `via :claude-2` chip, the principal's message without a
chip, the profile's two sessions, and the autocomplete offering both and inserting the labelled address.
It also extends `web/e2e/fit.spec.ts`'s `beforeAll` with the second session (a 40-character label and a
message through it) and its profile/conversation checks at 320/412 px and 125% font. `helpers.ts` gains
the live-id header on `agentCall` (additive) and the live id/harness on `openSession`.

**Where**: `web/e2e/ai-sessions.spec.ts`
**Also**: `web/e2e/fit.spec.ts`, `web/e2e/helpers.ts`
**Depends on**: T11, T12
**Requirement**: SESS-30, SESS-32, SESS-33, SESS-34
**Tests first**:
- desktop: open the AI's profile → "Live sessions" lists "principal" and ":<label>"; open the group →
  the principal's message has no `via`, the message the second socket posted shows `via :claude-2`; type
  `@atl` and see both options; pick `:claude-2` and send → the text in the composer is
  `@atlas:claude-2 `; the message is stored (the timeline shows it).
- 320/412 px: the extended fit checks pass at 125% font with the long-label session and the chip.
- the test makes no real network call; all sockets are the test instance.
**Done when**:
- [ ] `make e2e` passes for the desktop and the phone projects
- [ ] no existing e2e test is weakened or skipped
- [ ] `make check` still passes
**Tests**: e2e
**Gate**: e2e
**Commit**: `test(e2e): cover the two-session flow and the phone fit`
