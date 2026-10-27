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

## T6: the `ask` tool and the numbered platform fallback

- **`platform_sends.text` is the numbered text only while the send is not over.** The task asks for
  "201 and the stored `platform_sends.text` is `"Deploy?\n\n1. Sim\n2. Não"`", but CID-18 deletes a
  send's text as soon as the platform takes it (`MarkPlatformSendSent ... text = ''`), and the
  existing test asserts that. `TestConnectionSendNumbersTheOptions` therefore asserts both halves the
  design states: a send the platform takes answers 201 and the platform receives the numbered text;
  a send the platform does not take answers 202 `pending` and keeps the numbered text in
  `platform_sends`, so the retry sends the same text. A refused label set answers 422 on
  `quick_replies` and writes no `platform_sends` row at all.
- **An empty `quick_replies` is no list.** `ask` and the connection send treat `[]` as absent (the
  rule DM-04 already applies to empty targets), so a model that sends every parameter with an empty
  array still asks its wizard, and a plain `send_message` through a connection keeps its text as it
  is.
