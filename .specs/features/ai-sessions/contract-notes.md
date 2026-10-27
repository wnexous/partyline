# Contract Notes

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
