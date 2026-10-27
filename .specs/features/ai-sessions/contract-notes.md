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
