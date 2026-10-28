# Contract Notes (from ai-sessions T2: the label rules)

Binding for the parser (T3), the presence labels (T5) and the routing (T8). The rest of the design
stands; one line of it deviates from T2's own tests, and the tests win.

## The label pattern starts with a letter or a digit

The design's [Labels](../ai-sessions/design.md#labels) section and T2's **What** line give
`dto.SessionLabelPattern = "^[a-z0-9-]{1,40}$"`, while T2's **Tests first** line requires `-x` not to
match. A hyphen is inside that character class, so `-x` matches it: both lines cannot hold at once.
The tests are the contract, and no label the rules produce can begin with a hyphen (`LabelForFolder`
trims it and `AllocateLabel`'s base is a cleaned harness id), so the pattern became the strict
subset that keeps every produced label legal:

```go
dto.SessionLabelPattern = "^[a-z0-9][a-z0-9-]{0,39}$" // design said ^[a-z0-9-]{1,40}$
dto.MaxSessionLabel     = 40                           // unchanged
```

SESS-11 and SESS-21 say a label is `a-z0-9-` with 1 to 40 characters; the pattern is narrower by
the leading hyphen only. T3's parser therefore never accepts `@handle:-x`, which no session can hold
anyway, and every case T3 lists is unaffected. `web/src/lib/api/types.gen.ts` carries both constants
again after `make generate`.


Conventions and deviations the ai-sessions tasks settle while they are implemented. They bind the tasks
that follow, the same way the other features' contract notes do.

## T1: the author's session and the per-session delivery rows

- **The recreated `message_views` keeps 0026's two columns.** The design's [Data
  model](design.md#data-model) was written before quick replies landed: its `CREATE VIEW` lists the
  columns of 0023 and omits `interactive_json` and `answers_json`. Recreating the view exactly as the
  snippet shows would drop them, and the quick-reply reads (`GetMessageView`, `CreateMessage`'s
  neighbours) and `TestMigration0026AddsTheQuestionColumns` would break. The migration applies 0023's own
  rule the design cites ("the view lists its columns one by one, so it is recreated" to keep every column
  it had): it keeps 0026's column list and order and adds `m.author_label` after `m.answers_json`, before
  the computed author name. No other column moves.
- **`ListSessionDeliveriesByLabel` returns the full row.** Only the end-of-session sweep's
  `FallBackSessionDeliveries ... RETURNING` is needed in production; the by-label list exists for tests
  (T8's assertions on which rows the sweep left), so it selects `*` ordered by `created_at, message_id`
  rather than just the ids.

# Contract Notes (from ai-sessions T4: one AI for the repository's worktrees)

The rest of the design stands. Three lines of T4 are corrected here, because its own file list cannot
carry the behaviour it requires.

## The main folder reaches the channel through `Options` and `ListenOptions`

T4's **What** line requires `channel.Identity.MainFolder`, `X-Partyline-MainFolder` and
`PARTYLINE_MAIN_FOLDER` in `sessionIdentity`, and its test "`partyline listen` in a worktree sends the
header" needs the value to travel from the CLI to the connection. `internal/channel/run.go` builds the
`Identity` from `channel.Options` and `internal/channel/listen.go` from `channel.ListenOptions`, so
both gain a `MainFolder string` field (one field and one struct literal each). T4's **Also** line does
not name those files.

## The start check keys the AI by the main folder too

`Registry.CheckStart` (`internal/agents/placement.go`) looked the AI up by `FolderID.Folder` before it
judged `--org`/`--position`. For a worktree session that folder is the worktree, so the check would
have judged a creation instead of the repository's AI. It now uses the same `FolderID.key()` as
`ResolveFolder`/`LookupFolder`/`linkOrCreate`/`adoptionCandidate`. That is one line in a file T4's
**Also** line does not name, and it is what the design means by "everything that used `id.Folder` for
identity keeps using `key`".

## The label of a tools-only post is not T4's

T4's **Done when** line says a tools-only process in a linked worktree "resolves the repository's AI
and posts with the worktree label". T4 resolves the AI (tested through `Registry.RegisterFolder` and
`partyline listen`), but the label a post carries is `messages.author_label`, which T9 owns together
with the `X-Partyline-Live` rule (SESS-28..SESS-31); T4's file list does not include `rooms` or the
message DTO. Nothing derives an author label from the folder here; SESS-06's label arrives with T9 on
top of this resolution.

# Contract Notes (from ai-sessions T6: a parallel session on the wire)

The rest of the design stands. Removing the refusal touches a few files T6's **Also** line does not
name, and one label detail is settled by T5's merged contract.

## `Registry.Sessions` is the read `GET /agent/session` needs

The route's dependencies (`internal/api.Deps`) hold the `agents.Registry`, not the `Presence`, so the
handler cannot call `Presence.Sessions` directly. `internal/agents/folder.go` (T6's **Also** line) gains
the one-line `func (r *Registry) Sessions(agentID string) []Live { return r.presence.Sessions(agentID) }`,
which is the method T10's profile and members use too. No other registry change.

## The tests that asserted the refusal moved to the old-server compatibility path

SESS-10 supersedes SES-04: the server never refuses a second connection, so the tests that produced a
refusal by opening two real sessions cannot stay as they were. They now assert the new rule or drive the
channel's compatibility branch from a server of the previous release (409 `identity_busy` with the
`BusyDetail` details in front of the app, `internal/channel/session_lost_test.go`'s `heldAI`):

- `internal/agents/presence_test.go`: the two SES-04 tests become SESS-10/SESS-11 tests (a second
  process becomes a parallel session; two connections without a process id are two sessions and a
  takeover still replaces the principal).
- `internal/agents/folder_test.go`, `internal/agents/delete_test.go`,
  `internal/agents/delete_after_commit_test.go`, `internal/agents/registry_test.go`,
  `internal/api/agent_delete_after_commit_test.go`: `Presence.Attach` and the old
  `Registry.Attach` signature were removed, so these fixtures attach with `AttachLabeled`; the folder
  test asserts a parallel session instead of `ErrBusy`.
- `internal/api/agent_placement_test.go`, `internal/api/agent_welcome_org_test.go`: two comments named
  `identity_busy`; they now describe a reconnect only.
- `internal/channel/session_lost_test.go`, `internal/channel/tools_test.go`,
  `internal/channel/link_live_test.go`, `internal/cli/channel_live_test.go`: the busy texts and the
  SES-04 retry loop are the compatibility branch for an older server, so their setup simulates that
  server instead of relying on the new one to refuse; the CLI live test asserts that a channel without
  takeover attaches as a parallel session (SESS-10, SESS-16's client half is T7).

## A same-folder parallel label names the session's harness, not the AI's

T5's merged contract (`TestTwoConnectionsRaceForThePrincipal`, an AI stored as `agent` and sessions
reporting `claude` → `claude-2`) fixes `AllocateLabel`'s base to the harness the attaching session
reports. So a `pi` session joining a Claude AI in the same folder is `pi-2`, and T10's expected
`{label:"claude-2",harness:"pi"}` needs a preferred label (a worktree folder named `claude-2`, for
example) or the `pi-2` value. T6's tests attach the second session with the AI's harness when they assert
`claude-2`.

# Contract Notes (from ai-sessions T8: routing a message to its session)

The rest of the design stands. Two details the design's snippets cannot carry as written:

## The fallback's label needs a second read

`ListSessionFallbacks` (T1) returns `message_views` only, and `message_views` has no target label: its
`author_label` is who wrote the message, not the session it was addressed to. `Gateway.backlog` and
`Gateway.Pending` must render `for_label` from the delivery fact even after the message text was edited,
so `internal/store/queries/session.sql` also has `ListSessionFallbackLabels(agent_id, room_id)`, which
returns the `(message_id, label)` pairs of that room's fallen-back rows. The generated `db` code is the
only file this adds outside T8's list; `ListSessionFallbacks` itself keeps its `message_views` shape and
T1's tests.

## A consumed fallback advances the principal's cursor when it is above it

`Pending` deletes the fallback rows it returns, because a pending read counts as delivered. If such a
row's sequence is above the principal's cursor, deleting it alone would make the next cursor read return
the same message again. The room's cursor therefore advances to the highest consumed sequence, a fallback
or a cursor message; `AdvanceAgentCursor` keeps the maximum, so a fallback below the cursor changes
nothing. `Pending` still reads cursor messages in sequence with the fallback rows and never advances over
an unseen cursor message. `backlog` keeps the row, so only `Pending` needs this.

# Contract Notes (from ai-sessions T9: record which session wrote a message)

The rest of the design stands. Three details the task's file list does not carry as written:

## `CreateMessage` lives in `messages.sql`, not `rooms.sql`

The task's **Also** line names `internal/store/queries/rooms.sql`, but the message queries (and so
`CreateMessage`, which gains the `author_label` parameter and column) are in
`internal/store/queries/messages.sql`; `rooms.sql` holds the rooms and their members and was not
touched. `make generate` regenerated `internal/store/db/messages.sql.go` and
`web/src/lib/api/types.gen.ts`.

## `SessionLabelOf` is a `Registry` method in `presence.go`

It reads the live-id lookup (`Presence.SessionLabel`) and the AI's stored folder, so it lives next to
the presence code the task lists rather than in `registry.go`. It resolves in the design's order: the
live session whose `X-Partyline-Live` matches, else `LabelForFolder` of a session folder different from
the AI's own (an empty folder header names none), else `""`.

## One existing assertion asserted the old rule

`internal/channel/identity_test.go` asserted "REST calls carry no live id", which SESS-29 reverses.
`TestEveryConnectionAndCallSendsTheIdentity` now requires the tool call to carry the same live id the
connection sent (the tools-only process still sends none). It is a stronger assertion, not a weaker
one.

# Contract Notes (from ai-sessions T10: the live sessions in the profile and in rooms)

The rest of the design stands. The required `sessions` list updates tests outside T10's file list.

## The profile's `sessions` is a required TypeScript field

`dto.AIDetail.Sessions` has no `omitempty`, because the profile must answer `[]` and never `null`
(SESS-32), so tygo emits it as a required `sessions: SessionInfo[]`. Six fixture objects across four
existing web tests that build an `AIDetail` by hand gain `sessions: []`
(`web/src/components/profile-panel.test.tsx`, `web/src/routes/_app/profile.test.tsx` twice,
`web/src/routes/_app/rooms/conversation.test.tsx`, `web/src/routes/a11y.test.tsx` twice); T12 is the
task that renders the list.

## The presence change also reaches an existing event count

The AI's presence event now fires on every session change (SESS-32), so
`TestGatewayAttachesAConnectionThatLostTheRaceAsAParallelSession` sees three online announcements
(the winner's attach, the gateway's attach and the gateway's detach) instead of one. That file is
outside T10's **Also** line; the assertion is updated to the new rule, not weakened.
# Contract Notes (from ai-sessions T7: the clients present a session)

The rest of the design stands. One test T7's **Also** line does not name asserts the prompt that SESS-16
removes, so it moved with the behaviour; the main-folder assertion its **Tests first** bullet mentions
stays T4's, as that bullet itself says.

## The placement test that shared the prompt moved to the parallel session

`internal/cli/launcher_placement_test.go` had `TestLauncherAsksOnceForThePlacementAndTheTakeover`: it
proved one session lookup answered both the placement check and the takeover prompt (old SES-01/SES-02),
and that the answer started the harness with `PARTYLINE_TAKEOVER=1`. SESS-16 removes the prompt, and T7's
file list does not name that file, but leaving the test would fail the gate on behaviour the spec
supersedes. It is now `TestLauncherChecksThePlacementOnceWithoutAskingAboutTheLiveSession`: still one
lookup for the placement, with the launch starting with `PARTYLINE_LAUNCH_ORG` and without
`PARTYLINE_TAKEOVER=1`. No assertion was weakened; the old one is replaced, as T6 did with the tests that
asserted `identity_busy`.

## The main-folder header stays T4's test

T7's **Tests first** bullet also names `internal/channel/identity_test.go` for "`mainFolder` is sent
percent-encoded only when non-empty", then says the T4 test owns the header and this task asserts only the
notice and identity text. `internal/channel/identity.go` is read-only here, as T7's **Also** line says,
and T4's `Identity.setHeaders` is the rule that sends the header; T7's notice, the sentence and the
stderr line are tested in `internal/channel/link_live_test.go`
(`TestChannelAnnouncesItsParallelSessionOnce`).

# Contract Notes (from ai-sessions T14: the two-session flow and the phone fit)

The rest of the design stands. Two details the task's file list cannot carry as written:

## The 40-character label needs a linked worktree

T14 says the fit check seeds "a second live session with a 40-character label" and that `openSession`
gains "the live id/harness". A session in the AI's own folder can only hold the principal label (`""`)
or a `<harness>-<n>` label, so the fit session runs in a linked worktree whose folder base is exactly 40
characters; `openSession` therefore also takes the session's `folder` and the AI's `mainFolder` as raw
paths (it percent-encodes them, as the CLI does). `agentCall` needs only the live id, as T14 says: the
folder header still names the AI, and SESS-29 resolves the label from the live session.

## The profile's owner row made the page scroll sideways

The fit check now visits the full profile page. Its owner row put an `IntelligenceName` (an inline flex
whose name truncates) inside an inline link in a `min-w-0` cell; an inline box is sized at its
max-content width, so the fit owner's 60-character name widened the page to 430 px on a 360 px screen,
and no text element reported as spilling because the box itself spilled. The fix is inside the page,
which T14's **Where** line does not name: the link is `block min-w-0` and the name is `w-full`, so the
name truncates inside its cell (SESS-34).

## A session label's boundary lets a sentence's final "." through

The review asked that a label end at the end of the text or at a character that is not `[A-Za-z0-9-]` and
not `.`, `/` or `:`. Read literally, that also refuses `@atlas:claude-2.` at the end of a sentence, which
SESS-21 and its existing test accept. The boundary therefore treats `.` as a boundary only when it ends the
text or is followed by whitespace, so `@atlas:wt.example.com` and `@atlas:wt/x` do not route while
`ping @atlas:wt. Thanks` does. An address preceded by `/` (`https://example.com/@atlas:wt`) is not one
either.
## Delivery rows have an owner and a terminal state (store layer)

`agent_session_deliveries` (0027, edited in place) gains `owner_live` (the live id of the connection the
message was pushed to, `''` for a row born fallen back) and `acked_at`. An acknowledgement sets `acked_at`
and never deletes the row: the tombstone keeps the principal's backlog and pending queries excluding the
message, and a retried push (`ON CONFLICT DO NOTHING`) never re-creates ownership. The queries
`AckSessionDelivery` (owner only), `AckFallbackDelivery` (principal), `FallBackOwnedDeliveries` (one
connection's pending rows; the AI's own messages are acknowledged instead of falling back),
`ListOwnedPending` and `GetSessionDelivery` replace the label-keyed sweep. The fallback reads hide
acknowledged rows and the AI's own messages. The gateway still calls the old label-keyed queries
(`ReleaseSessionDelivery` is the old deleting ack under its new name, `DeleteSessionDelivery`,
`FallBackSessionDeliveries`, `DeleteSelfSessionDeliveries`) with unchanged behaviour until the next task
wires `internal/agents` to the owned rows and removes them; design.md still describes the deleting ack.
