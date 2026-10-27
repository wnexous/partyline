# Quick Replies and Question Wizards Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Spec**: `.specs/features/quick-replies/spec.md`
**Design**: `.specs/features/quick-replies/design.md`
**Status**: Draft, awaiting approval

The product owner's brief fixes the behaviour (see the spec's Problem Statement). The design settles the
questions the brief left to it: migration `0026` and the canonical document, the server-assigned ids and
limits, the answer as a reply with a rendered text plus a structured copy, one answer per member, the
`ask` tool, the frame attributes, the numbered platform fallback, the bubble's states, focus and fit, and
the derived answered state.

Each task is one commit, at most about 300 changed lines and about 5 hand-written files, under an hour,
and is independently testable. Tests are written first and derive from the spec's acceptance criteria.
Generated files (`internal/store/db/*`, `web/src/lib/api/types.gen.ts`) are never hand-edited.

## Test Coverage Matrix

> Generated from the codebase, the project guidelines and the spec. Guidelines found: `AGENTS.md` (tests
> derive from the spec's ACs; `make check` before every commit; no message text or secrets in logs;
> synthetic fixtures), `.specs/LESSONS.md`, `.specs/STATE.md` (AD-012, AD-025, AD-026), `Makefile`,
> `docs/api.md`, `docs/agents.md`, existing tests (`internal/store/push_migration_test.go`,
> `internal/rooms/message_edit_test.go`, `internal/api/message_edit_test.go`,
> `internal/channel/format_test.go`, `web/src/routes/_app/rooms/reactions.test.tsx`,
> `web/e2e/fit.spec.ts`).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Migration `0026`, queries | integration (real SQLite) | Both columns on a pre-`0026` database and a fresh one; the view exposes them; the one-answer index accepts a second author and a plain reply and refuses the same author; `MarkMessageDeleted` clears both | `internal/store/quick_replies_migration_test.go` | `go test -count=1 ./internal/store/...` |
| Pure rules (`questionFrom`, `answersFor`, `answerText`, `NumberedOptions`, `Parse*`) | unit | Every limit of the design's table; ids `r1…`, `q1…`, `q1o1…`; single/multi/text rules; rendered lines; unknown version → nil | `internal/rooms/interactive_test.go` | `go test -count=1 ./internal/rooms/...` |
| Service post/answer | integration (`rooms` fixture, real SQLite + bus) | Post a question and an answer; own, deleted, second, not-a-question and history refusals; edit keeps the question; delete clears it; an answer cannot be edited | `internal/rooms/interactive_post_test.go` | `go test -count=1 ./internal/rooms/...` |
| Room and connection routes | e2e (testkit, real app on `httptest`) | 201 with `interactive`/`answers` on both room routes; 422 field names; 403 own; 409 `already_answered`; 409 `conflict` deleted; the connection send stores the numbered text | `internal/api/quick_replies_test.go` | `make test` |
| Channel frames | unit | `interactive` and `answers` meta JSON; content; the attribute round-trip; other messages unchanged | `internal/channel/format_test.go` | `go test -count=1 ./internal/channel/...` |
| Channel `ask` tool | unit (testkit + fake platform) | Both targets, both shapes, every refusal, the server's 422 text | `internal/channel/tools_test.go` | `go test -count=1 ./internal/channel/...` |
| Web quick replies | unit (Vitest + Testing Library + vitest-axe) | Offer, tap, pending, chosen, status, own/no-post/frozen, reload state, a plain reply is not an answer, 500 and 409 handling | `web/src/components/question-bubble.test.tsx` | `cd web && bun run test` |
| Web wizard | unit (Vitest + Testing Library + vitest-axe) | Steps, validity, focus, submit payload, summary, failure, keyboard, 8 questions | `web/src/components/question-bubble.test.tsx` | `cd web && bun run test` |
| Web end-to-end flow | e2e (Playwright, real binary, 1280 + 360 px) | Tap a quick reply, complete a wizard, one reply, chosen and summary after a reload | `web/e2e/quick-replies.spec.ts` | `make e2e` |
| Web end-to-end fit | e2e (Playwright, real binary, 320 + 412 px at 125%) | A 4-button question and an 8-question wizard seeded in the conversation: no sideways scroll, no spilled text, Next/Send reachable | `web/e2e/fit.spec.ts` | `make e2e` |
| Docs | none (build gate) | `docs/api.md`, `docs/agents.md` and `.specs/STATE.md` name every field, limit, tool, meta key and the decision | `docs/api.md`, `docs/agents.md`, `.specs/STATE.md` | `make check` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | While iterating on the store or the rooms rules | `go test -count=1 ./internal/store/... ./internal/rooms/...` |
| Quick (API) | While iterating on the routes or the channel | `go test -count=1 ./internal/api/... ./internal/channel/...` |
| Quick (web) | While iterating on the bubble | `cd web && bun run test` |
| Full | Before any integration, route or tool change is done | `make test` |
| Build | **Before every commit** (AGENTS.md) | `make check` |
| E2E | T9 | `make e2e` |

Run `make generate` after touching the migration, the SQL queries or `internal/api/dto`; commit the
generated files with the change. `make check` builds the web app, so the bubble's i18n and the fit
fixtures are compiled in the same gate.

## Execution Plan

The phases are ordered. Tasks inside a phase run in parallel worktrees; a phase starts after the
previous group merged.

| Group | Phases | Starts after |
| --- | --- | --- |
| G0 | Phase 1 (T1 and T2 in parallel) | — |
| G1 | Phase 2 (T3) | G0 merged |
| G2 | Phase 3 (T4) | G1 merged |
| G3 | Phase 4 (T5, T6 and T7 in parallel) | G2 merged |
| G4 | Phase 5 (T8) | G3 merged |
| G5 | Phase 6 (T9 and T10 in parallel) | G4 merged |

**Merge rule for parallel worktrees.** Never hand-merge generated files (`internal/store/db/*`,
`web/src/lib/api/types.gen.ts`). After merging, run `make generate`, commit the result with the merge,
then run `make check`. T5 and T6 both live in `internal/channel` but touch disjoint files
(`format.go`/`format_test.go` vs `tools.go`, `connection_tools_test.go` and the API's
`agent_handlers.go`/`dto/connectors.go`), so their merge is mechanical.

Dependency graph (every arrow is a `Depends on` of the task it points to):

```
T1 -> T3
T2 -> T3
T2 -> T6
T3 -> T4
T4 -> T5
T4 -> T6
T4 -> T7
T4 -> T10
T6 -> T10
T7 -> T8
T7 -> T9
T7 -> T10
T8 -> T9
T8 -> T10
```

### Phase 1: The message and its rules

T1 adds the columns, the view and the index that everything below reads. T2 adds the shapes and the
pure validation, which need no database. They share no file.

### Phase 2: Posting and answering

T3 is the one write path: it validates through T2 and stores through T1, including the one-answer rule
and the edit/delete behaviour.

### Phase 3: The room API

T4 exposes the fields on the two room routes and in `dto.Message`, which regenerates the web types that
T7 needs and feeds the channel frames of T5 and T6.

### Phase 4: The AI side and the bubble

T5 (frames) and T6 (`ask` and the platform fallback) are both in `internal/channel`, on disjoint files.
T7 is the web bubble for quick replies; it can start as soon as T4 merged because its only server
contract is the message DTO and the post route.

### Phase 5: The wizard

T8 extends the bubble T7 introduced with the step machine, the summary and the focus rules; it is the
same component and the same test file, so it follows T7.

### Phase 6: End to end and documentation

T9 extends the fit spec and adds the flow spec; it needs both bubble halves (T7, T8) and the room route
(T4). T10 writes `docs/api.md`, `docs/agents.md` and `.specs/STATE.md` from the merged behaviour.

---

## Task Breakdown

### Phase 1: The message and its rules

#### T1: Store a question and its answer on the message [store]

**What**: migration `0026_quick_replies.sql` with the two `ADD COLUMN`s (`interactive_json`,
`answers_json`, both `NOT NULL DEFAULT ''`), the `message_views` recreate (0023's view plus the two
columns) and the partial unique index `messages_answer_once ON messages (reply_to, author_iid) WHERE
answers_json <> ''`, exactly as the design's [Data model](../quick-replies/design.md#data-model). The
sqlc queries: `CreateMessage` gains both columns, `MarkMessageDeleted` clears both, and the new
`CountAnswersByAuthor`. Regenerate the sqlc output.

**Where**: `internal/store/migrations/0026_quick_replies.sql`
**Also**: `internal/store/queries/messages.sql`, `internal/store/quick_replies_migration_test.go`
**Depends on**: None
**Requirement**: QR-01, QR-04, QR-08
**Tests first** (`internal/store/quick_replies_migration_test.go`, following
`internal/store/push_migration_test.go`: open before `0026` with `openMigratedBefore(t, "0026")`):
- `TestMigration0026AddsTheQuestionColumns`: insert a user, their intelligence, a room and a message
  before applying `0026`; after it, `PRAGMA table_info(messages)` lists `interactive_json` and
  `answers_json`, `NOT NULL`, default `''`; `SELECT interactive_json, answers_json FROM message_views`
  returns `""`, `""`.
- `TestTheViewExposesAStoredQuestion`: `UPDATE messages SET interactive_json = '{"v":1,"quick_replies":
  [{"id":"r1","label":"Sim"}]}'`; reading the message through `message_views` returns the same string.
- `TestOneStructuredAnswerPerAuthorAndQuestion`: a question `q`; an answer by `u1` (`reply_to = q`,
  `answers_json` non-empty) is stored; a second one by `u1` fails with a UNIQUE constraint naming
  `messages.reply_to, messages.author_iid`; one by `u2` is stored; a plain reply by `u1`
  (`answers_json = ''`) is stored; deleting `u1`'s answer (`MarkMessageDeleted`) then inserting a new
  one by `u1` is stored.
- `TestMarkMessageDeletedClearsBothParts`: deleting the question returns text `""`,
  `interactive_json ""`, `deleted_at` set, `edited_at` NULL; deleting an answer returns text `""`,
  `answers_json ""`, the same id, `seq` and `reply_to`.
**Done when**:
- [x] the migration runs on a database that existed before `0026` and on a fresh one
- [x] `make generate` ran and the sqlc output is committed
- [x] no Go hook is added (the migration is pure SQL)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): store a question and its answer on the message`

#### T2: Validate and render questions and answers [rooms]

**What**: the wire and stored shapes in `internal/api/dto/quickreplies.go`
(`MessageQuestionKind` `single`/`multi`, `QuickReply`, `WizardOption`, `WizardQuestion`, `Wizard`,
`MessageInteractive`, `MessageAnswerItem`, `MessageAnswers`, `WizardRequest`, `WizardQuestionRequest`,
`AnswerSubmission`, `AnswerItem`, the exported limits (`MaxQuickReplies`, `MaxWizardQuestions`,
`MaxQuestionOptions`, `MaxQuestionLabel`, `MaxQuestionTitle`, `MaxAnswerText`, so the web imports them
like `MaxTextLength`) and `ParseMessageInteractive`/`ParseMessageAnswers` returning nil for
empty, corrupt or unknown-version JSON) and the pure rules in `internal/rooms/interactive.go`:
`questionFrom(quickReplies, wizard)`, `answersFor(question, submission)`, `answerText(answers)`,
`ValidateQuickReplyLabels(labels)` and `NumberedOptions(text, labels)`. Limits, field names and
rendered text exactly as the design's [Validation](../quick-replies/design.md#validation) and
[The answer](../quick-replies/design.md#the-answer); ids assigned `r1…`, `q1…`, `q1o1…`; labels
trimmed; duplicates in one set refused case-insensitively.

**Where**: `internal/rooms/interactive.go`
**Also**: `internal/api/dto/quickreplies.go`, `internal/rooms/interactive_test.go`
**Depends on**: None
**Requirement**: QR-02, QR-03, QR-06, QR-07
**Tests first** (`internal/rooms/interactive_test.go`, package `rooms`):
- `TestQuestionQuickReplies`: `[" Sim ", "Não"]` → ids `r1`,`r2`, labels trimmed; `MaxQuickReplies`
  labels pass; one more → `*validation.Error` field `quick_replies`; an empty label, 49 `a`s, `"a\nb"` and `["sim","SIM"]` each
  422 on `quick_replies`.
- `TestQuestionWizard`: a question without `kind` is `single`, `allow_text` false, options ids `q1o1`,
  `q1o2`; `MaxWizardQuestions` questions and `MaxQuestionOptions` options pass; one more of either is 422
  on `wizard`; an empty title, a 201-character
  title, a 49-character option, duplicate options, no options, and `quick_replies` together with a
  wizard each 422 (`wizard`, or `quick_replies` for the last).
- `TestAnswersQuickReply`: `{quick_reply:"r2"}` on a three-button question (`Sim`, `Não`, `Depois`) → `{v:1, quick_reply:"Não"}`;
  an unknown id is 422 on `answers`; `items` without `quick_reply` is 422.
- `TestAnswersWizard`: questions `q1` (single) and `q2` (multi, `allow_text`), answered in reverse
  order → stored in `q1`, `q2` order; single with two options, with no option and no text, and with a
  text while `allow_text` is false each 422; single with text only and `allow_text` true → `values []`,
  `text set`; single with one option plus a text and `allow_text` true → both; multi with no option
  422; multi with two options and a text → both; 281-character text 422; a missing question, a
  duplicate question id and an option of another question each 422.
- `TestAnswerText`: `"Sim"`; `"Quando?: Na sexta\nO que entra?: API, Web (antes do almoço)"`; a single
  answered by text only → `"Quando?: depois das 10h"`; a single with an option and a text →
  `"Quando?: Na sexta (depois das 10h)"`; an answer with no text has no parentheses.
- `TestNumberedOptions`: `["Sim","Não","Depois"]` → `"Deploy?\n\n1. Sim\n2. Não\n3. Depois"`; one
  label; `ValidateQuickReplyLabels` refuses 5, a 49-character label and `["a","A"]` with field
  `quick_replies`.
- `TestParseTheStoredDocuments`: `""`, `"not json"` and `{"v":2,…}` → nil; a v1 question and a v1
  answer decode to the expected structs.
**Done when**:
- [x] every limit in the design's table is a case and each refusal names its field
- [x] the two files import neither `store` nor `net/http`
- [x] `make generate` ran (the new DTO types reach `web/src/lib/api/types.gen.ts`) and it is committed
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(rooms): validate and render questions and their answers`

### Phase 2: Posting and answering

#### T3: Post a question and answer one [rooms]

**What**: `PostInput` gains `QuickReplies []string`, `Wizard *dto.WizardRequest` and
`Answers *dto.AnswerSubmission`; `draft` gains `interactive` and `answers` (the canonical JSON);
`Post` calls `questionFrom`/`answersFor`, sets the rendered text, and `post` runs `checkAnswer`
(`reply_to` set and empty `text` with `answers`; the target carries a question and is not the caller's;
not deleted; `CountAnswersByAuthor` is 0) before `insert` writes both columns in the same transaction.
A unique-constraint failure on `messages_answer_once` maps to `rooms.ErrAlreadyAnswered` (409). Only a
text post can carry a question: a reaction message (`PostInput.Kind = reaction`) with one is 422 on
`quick_replies` (`PostFromChat` has no interactive input at all). `EditMessage` refuses a
message with a non-empty `answers_json` (422 `invalid` on `text`) and leaves `interactive_json` alone;
`DeleteMessage` needs no change (T1's statement clears both).

**Where**: `internal/rooms/interactive_post.go`
**Also**: `internal/rooms/messages.go`, `internal/rooms/message_edit.go`,
`internal/rooms/interactive_post_test.go`
**Depends on**: T1, T2
**Requirement**: QR-01, QR-04, QR-05, QR-08, QR-09
**Tests first** (`internal/rooms/interactive_post_test.go`, package `rooms`, the existing `newFixture`):
- `TestPostAQuestionStoresTheCanonicalDocument`: Alice posts `{Text:"Deploy?", QuickReplies:["Sim",
  "Não"]}` → `Interactive` ids `r1`,`r2`; `SELECT interactive_json, answers_json` equals the canonical
  JSON and `""`; the bus carries one `MessageCreated`.
- `TestAnswerAQuickReply`: Bob answers `{ReplyTo:q, Answers:{QuickReply:"r1"}}` → text `"Sim"`,
  `answers_json` `{"v":1,"quick_reply":"Sim"}`, `reply_to` the question, one `MessageCreated`.
- `TestAnswerAWizard`: a 2-question wizard answered → text `"Quando?: Na sexta\nO que entra?: API, Web"`,
  the structured answer in the question's order.
- `TestTheRefusals`: Alice answers her own → `access.ErrForbidden`; `answers` without `reply_to` → 422
  field `answers`; `answers` to a plain message → 422 `answers`; `answers` with a file → 422 `answers`; `answers` with a
  non-empty `text` → 422 `text`; a deleted question →
  `ErrMessageDeleted`; Bob answers twice → `ErrAlreadyAnswered`; after Bob deletes his answer he answers
  again successfully; a reaction post with quick replies → 422 `quick_replies`.
- `TestEditAndDeleteAQuestion`: the text changes and `edited_at` is set while `interactive_json` is
  unchanged; the delete leaves text `""` and `interactive_json ""`; an edit after the delete is
  `ErrMessageDeleted`.
- `TestAnAnswerCannotBeEdited`: Bob's edit → 422 field `text`; his delete clears the text and
  `answers_json`; answering again succeeds.
- `TestAHistoryOnlyDMRefusesAnAnswer`: the fixture's person DM → `access.ErrHistoryOnly`.
**Done when**:
- [x] no new package is created (so `internal/archtest` is untouched)
- [x] the answer's text and structured copy are written in one insert, never updated afterwards
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms): post a question and answer one`

### Phase 3: The room API

#### T4: Serve questions and answers on the room routes [api]

**What**: `dto.PostMessageRequest` gains `QuickReplies`, `Wizard` and `Answers`; `dto.Message` gains
`Interactive *MessageInteractive` and `Answers *MessageAnswers`; `convert.Message` decodes both with
`dto.ParseMessageInteractive`/`ParseMessageAnswers`; `postTo` passes the three fields into
`rooms.PostInput`; `mapError` maps `rooms.ErrAlreadyAnswered` to 409 `already_answered`, a new
`dto.ErrorCode` value. Regenerate the TypeScript types. The agent room route accepts the same fields
because it shares `PostMessageRequest`.

**Where**: `internal/api/dto/rooms.go`
**Also**: `internal/api/dto/quickreplies.go`, `internal/api/dto/common.go`, `internal/api/convert/convert.go`,
`internal/api/rooms_handlers.go`, `internal/api/router.go`, `internal/api/quick_replies_test.go`
**Depends on**: T3
**Requirement**: QR-01, QR-05, QR-06, QR-07, QR-18
**Tests first** (`internal/api/quick_replies_test.go`, `testkit.New(t)` with two users and an AI):
- `TestPostAQuestionOverTheAPI`: the owner posts `{"text":"Deploy?","quick_replies":["Sim","Não"]}` →
  201 with `interactive.quick_replies == [{id:"r1",label:"Sim"},{id:"r2",label:"Não"}]`;
  `GET /rooms/{id}/messages` returns the same message with the same part.
- `TestPostAWizardOverTheAPI`: 201 and `interactive.wizard.questions[1].kind == "multi"`, option ids
  `q2o1`, `q2o2`.
- `TestAnswerOverTheAPI`: the other user answers `{"reply_to":q,"answers":{"quick_reply":"r1"}}` → 201,
  text `"Sim"`, `answers.quick_reply == "Sim"`; the same body again → 409 with code
  `already_answered`; a plain reply without `answers` → 201 with no `answers` field.
- `TestQuestionValidationOverTheAPI`: 5 quick replies → 422 `invalid` field `quick_replies`; 9
  questions → 422 field `wizard`; both → 422; a bad option id → 422 field `answers`; the author
  answers their own → 403 `forbidden`.
- `TestAnswersToADeletedQuestionAreRefused`: delete the question → 409 `conflict`.
- `TestTheAgentRouteTakesTheSameFields`: the AI posts a question with its token → 201 with
  `interactive`; its own answer to it → 403 `forbidden`.
**Done when**:
- [x] every status and code is asserted, not just the happy path
- [x] `make generate` ran and `web/src/lib/api/types.gen.ts` is committed
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): serve questions and answers on the room routes`

### Phase 4: The AI side and the bubble

#### T5: Carry the question and the answer in the AI frame [channel]

**What**: `channel.Format` adds `meta["interactive"]` (the compact JSON of the message's
`Interactive`) and `meta["answers"]` (the compact JSON of its `Answers`), and nothing else changes:
the content stays the human text, `reply_to` already names the question. `Frame` already escapes the
attribute values. A message with neither keeps the frame it has today.

**Where**: `internal/channel/format.go`
**Also**: `internal/channel/format_test.go`
**Depends on**: T4
**Requirement**: QR-21, QR-22
**Tests first** (`internal/channel/format_test.go`):
- `TestFormatCarriesAQuestion`: `Interactive` with `r1`/`Sim` → `meta["interactive"] ==
  "{\"v\":1,\"quick_replies\":[{\"id\":\"r1\",\"label\":\"Sim\"}]}"`, no `answers` key, content
  `"Deploy?"`.
- `TestFormatCarriesAnAnswer`: `Answers{QuickReply:"Sim"}` → `meta["answers"] ==
  "{\"v\":1,\"quick_reply\":\"Sim\"}"`, `meta["reply_to"] == "m-q1"`, content `"Sim"`.
- `TestFormatWithoutAQuestionIsUnchanged`: no `interactive` and no `answers` keys.
- `TestTheFrameCarriesTheJSONAsOneAttribute`: `Frame(Record(m))` contains
  `answers=&quot;{&quot;v&quot;:1` and, once unescaped, parses back to the same JSON; the content is
  `Sim`.
**Done when**:
- [x] only `Format` changes; `Record`, `Frame` and the adapters are untouched
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(channel): carry a question and its answer in the AI frame`

#### T6: Ask from the AI tools and degrade on platforms [channel]

**What**: the `ask` tool with the schema and description of the design's [`ask`](../quick-replies/design.md#ask):
exactly one target (`room` or `connection` with `chat`, no `to`), `text` required, at least one of
`quick_replies` or `wizard`, `reply_to` only with a room; a room posts
`POST agent/rooms/{room}/messages`, a connection posts
`POST agent/connections/{id}/chats/{chat}/messages` with `quick_replies` and refuses a wizard. Its own
refusals (`askNoTarget`, `askHalfTarget`, `askSeveralTargets`) are worded like those of `send_message`
(`msgNoTarget`, `msgHalfTarget`, `msgSeveralTargets`) without `to`, and it returns `sent (message_id …)`
or `platformSent(...)`. `dto.SendToChatRequest` gains `QuickReplies []string`;
`agentConnectionSend` validates the labels with `rooms.ValidateQuickReplyLabels` and sends
`rooms.NumberedOptions(text, labels)` as the text, so `platform_sends.text` is what was sent.

**Where**: `internal/channel/tools.go`
**Also**: `internal/api/dto/connectors.go`, `internal/api/agent_handlers.go`,
`internal/channel/connection_tools_test.go`, `internal/channel/tools_test.go`,
`internal/api/quick_replies_test.go`
**Depends on**: T2, T4
**Requirement**: QR-19, QR-20
**Tests first**:
- `internal/channel/tools_test.go`: `TestToolsList` gains `ask` with `required: ["text"]` (the exact
  list grows by one). `TestAskInARoom` — the session calls `ask` with a room and `["Sim","Não"]`;
  `e.messages(e.room.ID)[0]` holds the question with ids `r1`/`r2`, and the tool answers
  `sent (message_id …)`. `TestAskRefusals` — no target, both targets, `connection` without `chat`, a
  `to` with no room, neither shape, a wizard on a connection, and a server 422 whose text is passed
  through (each as the tool error it names).
- `internal/channel/connection_tools_test.go`: `TestAskOnAConnection` — `connectionChan`, the tool with
  `connection` and `chat` sends through the fake platform once, the sent text is
  `"Deploy?\n\n1. Sim\n2. Não"`, no room message is stored, and the answer is the platform sentence.
- `internal/api/quick_replies_test.go`: `TestConnectionSendNumbersTheOptions` — the AI posts
  `{"text":"Deploy?","quick_replies":["Sim","Não"]}` → 201 and the stored `platform_sends.text` is
  `"Deploy?\n\n1. Sim\n2. Não"`; 5 labels, a 49-character label and `["a","A"]` → 422 field
  `quick_replies` and nothing stored.
**Done when**:
- [x] the tool description says what happens on a chat (numbered text, no wizard) and that the answer
      arrives as a message
- [x] no new route, no new package and no new rate limit
- [x] `make generate` ran (`SendToChatRequest` changed) and the TypeScript types are committed
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(channel): ask questions and number options on a platform`

#### T7: Answer quick replies in the bubble [web]

**What**: `web/src/components/question-bubble.tsx` with the quick reply group (a labelled group, one
button per label, `min-h-11`, wrapping, pending/pressed/disabled states, a `role="status"` line),
`web/src/hooks/use-answer-question.ts` (its own `useMutation` with the key
`["answer-question", roomId]` and the post body of the composer, the toasts, and the `already_answered`
refresh of the room messages), the `myAnswer` map in `MessageTimeline` (answers by
`reply_to` and `author_id`), the no-grouping rule for a question and an answer, the `QuestionBubble`
slot in `MessageItem` between the text and the attachments, and the `qr.*` keys in both dictionaries.
The answer is posted as `{text:"", reply_to, answers:{quick_reply:id}}`; the stored message arrives
through the live `room.message` event.

**Where**: `web/src/components/question-bubble.tsx`
**Also**: `web/src/hooks/use-answer-question.ts`, `web/src/components/message-item.tsx`,
`web/src/components/message-timeline.tsx`, `web/src/components/question-bubble.test.tsx`,
`web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`
**Depends on**: T4
**Requirement**: QR-10, QR-11, QR-12, QR-13
**Tests first** (`web/src/components/question-bubble.test.tsx` via `renderRoute("/rooms/r1")`, `mockApi`
and `makeMessage`):
- `TestOffersTheButtons`: a `post`-level room with a question from `u2` ("Sim", "Não", "Depois") shows
  a group named "Quick replies" with three buttons.
- `TestATapPostsOnce`: click "Não" → `POST /rooms/r1/messages` with `{text:"", reply_to:"m-q",
  answers:{quick_reply:"r2"}}`; while pending the buttons are `aria-disabled="true"` and the status
  reads "Sending your answer…"; after it resolves "Não" is `aria-pressed="true"`, every button is
  `aria-disabled="true"`, and the status reads "Answer sent: Não".
- `TestALoadedAnswerIsChosen`: the timeline holds the viewer's reply with `answers.quick_reply` "Sim" →
  "Sim" is pressed and a click posts nothing.
- `TestAPlainReplyIsNotAnAnswer`: a reply without `answers` leaves the buttons acting.
- `TestTheAskersAndReadersSeeNoAction`: the question's author sees the labels and "Your question"; a
  `view` level sees "You need permission to post in this conversation"; a frozen conversation sees
  "This conversation is in history"; none of them posts.
- `TestAFailedAnswerComesBack`: a 500 shows `qr.failed` and the buttons act again; a 409
  `already_answered` shows `qr.alreadyAnswered` and triggers a second `GET /rooms/r1/messages`.
- `TestTheRowIsNotGrouped`: a question followed by the answer are two rows (each with its author).
- the existing `a11y.test.tsx` route audit still passes.
**Done when**:
- [x] no new route and no new query: one `useMutation` on the existing post path, and the live event
      remains the only writer of the timeline
- [x] every new string is in both dictionaries and the parity test is green
- [x] `bun run lint`, `bun run typecheck` and `cd web && bun run test` are clean
- [x] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): answer quick replies in the message bubble`

### Phase 5: The wizard

#### T8: Answer a wizard step by step [web]

**What**: the wizard half of `question-bubble.tsx`: one question per step with "Step 1 of n", Back,
Next and Send, a `radiogroup` for `single`, a checkbox `group` for `multi`, a `textarea` whose
`maxLength` is `MaxAnswerText` when `allow_text`, Next/Send disabled until the step is valid, the step heading focused on a
step change, the read-only summary (`dl` of question and chosen values) with focus on its heading after
Send, and a failure that returns the editable steps. The pure validity rules live in
`web/src/lib/questions.ts` (`singleValid`, `multiValid`, `submissionOf`) so the JSX stays flat. The
answered state comes from T7's `myAnswer`.

**Where**: `web/src/components/question-bubble.tsx`
**Also**: `web/src/lib/questions.ts`, `web/src/components/question-bubble.test.tsx`,
`web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`
**Depends on**: T7
**Requirement**: QR-14, QR-15, QR-16, QR-17
**Tests first** (`web/src/components/question-bubble.test.tsx`):
- `TestStepsAndValidity`: a 3-question wizard shows "Step 1 of 3"; Next is disabled until a radio is
  chosen; on step 2 Next is disabled until one checkbox is checked; step 3 shows Send.
- `TestOneSubmissionWithAllAnswers`: answering single "Na sexta", multi "API" + "Web" and a note
  "antes do almoço" posts one `POST /rooms/r1/messages` with `{text:"", reply_to, answers:{items:[
  {question_id:"q1",option_ids:["q1o2"]},{question_id:"q2",option_ids:["q2o1","q2o2"],
  text:"antes do almoço"}]}}`.
- `TestFocusFollowsTheWizard`: after Next, the step heading has focus; after Send, the summary heading
  has focus and the `dl` shows "Quando?" / "Na sexta" and "O que entra?" / "API, Web (antes do
  almoço)".
- `TestATextOnlySingleAnswerIsValid`: no option and "depois das 10h" enables Send and posts
  `{option_ids: [], text:"depois das 10h"}`.
- `TestAFailedSendComesBack`: a 500 returns the editable step and the error toast.
- `TestAnAnsweredWizardIsReadOnly`: rendering with the viewer's answer in the timeline shows the
  summary, no Next and no Send.
- `TestEightQuestionsWithLongLabels`: an 8×8 wizard renders without a crash and every label is text.
- `TestKeyboardOnly`: Tab reaches the first radio, Space selects it, Tab reaches Next, Enter moves on.
- the existing `a11y.test.tsx` route audit still passes with a wizard on the page.
**Done when**:
- [x] native inputs (radio/checkbox) with grouped names and an accessible name per group
- [x] no control is below 44 px and the summary is readable at 320 px
- [x] `bun run lint`, `bun run typecheck` and `cd web && bun run test` are clean
- [x] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): answer a question wizard in the bubble`

### Phase 6: End to end and documentation

#### T9: Cover the questions end to end and on a phone [web]

**What**: `web/e2e/quick-replies.spec.ts` against the real binary (desktop 1280 and mobile 360, the
existing `startInstance`, `loginAs`, `apiCall`, `agentJSON` helpers): seed a group with an AI; the AI
posts a quick-reply question and a wizard through the agent room route; the owner taps "Sim", sees the
reply and the chosen state, reloads and sees it again; the owner completes the wizard with the
keyboard, sees one reply with the expected lines, reloads and sees the summary. Extend
`web/e2e/fit.spec.ts`'s `beforeAll` with a 4-button question (48-character labels) and an 8-question
wizard from the AI, and `checkConversation` to wait for both bubbles at 320×700 and 412×900 with a
125% root font (the existing `enlarge`, `scrollThrough` and `expectFits`).

**Where**: `web/e2e/quick-replies.spec.ts`
**Also**: `web/e2e/fit.spec.ts`
**Depends on**: T4, T7, T8
**Requirement**: QR-24, QR-25
**Tests first**:
- flow, desktop: the quick-reply bubble is visible; after the tap the timeline shows "Sim" as a reply
  and the button is pressed; after `page.reload()` it is still pressed and no request is made.
- flow, wizard: Next is disabled; choose each step; Send posts; the reply text is
  `"Quando?: Na sexta\nO que entra?: API, Web"`; after a reload the summary is shown.
- fit, both phones: the conversation with the two bubbles fits (no sideways scroll, no spilled text),
  and the wizard's Next is visible and enabled after choosing an option.
**Done when**:
- [x] `make e2e` passes for desktop-1280 and mobile-360
- [x] the fit spec still covers every page it covered before (nothing weakened or skipped)
- [x] no test talks to a platform: the questions are posted through the agent room route
- [x] `make check` still passes
**Tests**: e2e
**Gate**: e2e
**Commit**: `test(e2e): cover quick replies and the wizard on a phone`

#### T10: Document the message fields, the tool and the decision [docs]

**What**: a "Questions and answers" section in `docs/api.md` (the three `PostMessageRequest` fields and
their limits, the two response fields, the answer body, the statuses 422/403/409 with the codes, the
`quick_replies` field of the connection send); in `docs/agents.md` the `ask` row in the tools table, the
two frame meta attributes and a short "Asking a question" paragraph (a room gets buttons, a chat gets
numbered text, the answer arrives as a reply); AD-029 in `.specs/STATE.md` as the design's Decision
record proposes. No behaviour changes.

**Where**: `docs/api.md`
**Also**: `docs/agents.md`, `.specs/STATE.md`
**Depends on**: T4, T6, T7, T8
**Requirement**: QR-23
**Tests first**: none in code (documentation; `make check` is the gate). Verify by hand that every field
and error code in `internal/api/dto/quickreplies.go` and `mapError` appears in `docs/api.md`, that the
`ask` schema in `internal/channel/tools.go` matches its row, and that the two meta keys are in
`internal/channel/format.go`.
**Done when**:
- [x] every new field, limit and error code is in `docs/api.md` with its status
- [x] `docs/agents.md` lists `ask`, `interactive` and `answers`
- [x] `.specs/STATE.md` gains AD-029 without changing another decision
- [x] Gate check passes: `make check`
**Tests**: docs (no code; `make check` is the gate)
**Gate**: build
**Commit**: `docs(quick-replies): document the question fields, the ask tool and the decision`
