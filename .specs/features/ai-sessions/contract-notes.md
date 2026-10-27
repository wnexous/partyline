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
