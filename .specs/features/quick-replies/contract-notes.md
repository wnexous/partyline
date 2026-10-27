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

## T9: the end-to-end flow and the phone fit

- **A long quick reply label wraps through its own flex item.** The fit spec seeds four labels at the
  server's 48-character limit ending in one unbroken run, which is the case the design's risk table
  promises to wrap ("buttons wrap, `break-words`"). A button is a flex container, so its text was an
  anonymous flex item whose automatic minimum width is the widest word: the 48-character label spilled
  out of the button at both viewports. The label is now its own `min-w-0 [overflow-wrap:anywhere]`
  span, inside the button and inside the read-only chip, so the bubble grows in height only (QR-24).
  The component's unit tests and both e2e specs cover the change; no server behaviour changed.
- **The AI authors more than one row.** Seeding the question and the wizard from the AI gives its name
  three links in the timeline, so the profile-panel fit check focuses the first one instead of the only
  one. It still opens the same panel, and every page the fit spec covered before is still checked.

## Review fix: the order of the answer refusals

- **The author check comes before deletion and before the question check.** Deleting a message
  clears `interactive_json` (QR-04), so after a real delete a question and a non-question are the
  same row: `deleted_at` set and no interactive document. `checkAnswer` therefore applies, in order:
  reply target in the room, the caller is not its author (403, also when the target is deleted),
  the target is not deleted (409), the target carries a question (422), and the caller has no live
  answer (409). An asker answering their own deleted question gets 403; a deleted non-question
  answered by someone else stays 409, because the server can no longer tell it from a deleted
  question. A live non-question stays 422. This follows the coordinator's decision for the review
  fixes; no marker column was added, so the placeholder still keeps nothing of what it asked.
- **`Post` renders an answer only after the room check, and only for a live target from another
  author.** The ordered refusals above run inside the post transaction in `checkAnswer`; a draft
  that reaches it unrendered (the caller's own target or a deleted one) is refused there before it
  can be inserted, so `draft.validate` does not measure its empty text.
