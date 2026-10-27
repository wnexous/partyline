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
