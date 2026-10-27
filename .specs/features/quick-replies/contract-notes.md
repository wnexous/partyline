# Contract Notes

Conventions and deviations the quick-replies tasks settle while they are implemented. They bind the
tasks that follow, the same way the other features' contract notes do.

## T5: the question and the answer in the AI frame

- **`Frame` gains two attribute entries.** The task's "only `Format` changes; `Record`, `Frame` and the
  adapters are untouched" assumed `Frame` already renders every meta key as an attribute. It renders a
  fixed list (`internal/channel/frame.go:47-60`), so a meta key alone never reaches the framed text. The
  smallest change that satisfies the task's own test (`Frame(Record(m))` carries the answer) adds
  `interactive` and `answers` to that list. `Record`, the attribute escaping (`attrEscaper`) and the
  TypeScript adapters are untouched; an adapter that ignores the two keys still shows the content and
  `reply_to` as before, which is the degradation the design allows.
- **The escaped form is `answers="{&quot;v&quot;:1…}"`.** `Frame` keeps the attribute delimiters as
  literal quotes, as it does for `room="Support"`. The test's original substring
  `answers=&quot;{&quot;v&quot;:1` would require escaping the delimiters themselves and would change
  every existing frame, including the shared cases in `integrations/testdata/framing.json`. The test
  asserts the actual form and unescapes the value back to the stored JSON; the escaping itself is
  unchanged.
