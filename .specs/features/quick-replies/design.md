# Quick Replies and Question Wizards Design

**Feature**: `quick-replies` · **Spec**: [spec.md](spec.md) · **Tasks**: [tasks.md](tasks.md)
**Status**: Draft for approval · **Base**: `main` at `a1f6544`

## Answers to the brief's design questions

| Question | Answer |
| --- | --- |
| How a message carries the interactive part | Two columns on `messages` (migration `0026`): `interactive_json` holds the question (1–4 quick replies, or a wizard of 1–8 questions) and `answers_json` holds the structured answer of a reply. Both are `''` for every other message. The server validates, assigns the ids and stores one canonical, versioned JSON document. → [Data model](#data-model) |
| What the limits are | 4 quick replies; 8 questions; 8 options per question; labels 1–48 characters on one line; question titles 1–200 characters on one line; free-text answers at most 280 characters. → [Validation](#validation) |
| Edits and deletes | An edit changes the text only (the interactive part is immutable, `internal/rooms/message_edit.go:44-103`); a delete clears the interactive part and the answer with the text, so the placeholder keeps nothing (`MarkMessageDeleted`, `internal/store/queries/messages.sql:65-70`). An answer cannot be edited. → [The answer](#the-answer) |
| The answer | A normal reply (`reply_to` the question) whose text the **server renders** ("Sim", or one `Title: answer` line per question) and whose `answers_json` is the structured copy. The author, the room, the delivery rules, the notifications and the live socket are the existing ones. → [The answer](#the-answer) |
| Who may answer, and how often | Any member with the post level (`access.LevelPost`) except the question's author; in a DM that is the other side. One structured answer per author per question, enforced by a partial unique index; a second attempt is 409 `already_answered`. → [Who answers](#who-answers) |
| How the AI asks | A new MCP tool `ask` (room, or connection with chat) with `text`, `quick_replies` or `wizard`, and `reply_to` for a room. → [`ask`](#ask) |
| How the AI receives the answer | As a normal message: the answer replies to the question's message, so `delivery.Wants` gives it to the asking AI under mentions-only (`internal/delivery/wants.go:66-92`, `ReplyToAuthorIsMe`); its channel frame carries `answers` (the structured JSON) in the meta. → [Frames](#frames) |
| Platforms | `ask` on a connection appends the labels as `1. label` lines to the text and sends plain text through the existing send queue; nothing is stored and the reply is an ordinary inbound message. A wizard is refused on a connection. → [Platforms](#platforms) |
| The web bubble | `QuestionBubble` in the message row: quick reply buttons (min 44 px, wrapping at 320 px), or a wizard with one step at a time, Back/Next, a final Send and a read-only summary. The answered state is derived from the viewer's reply in the timeline, so no new server state and no new live frame. → [Web](#web) |
| Every new route | No new path: `POST /api/v1/rooms/{id}/messages` and `POST /api/v1/agent/rooms/{room}/messages` accept `quick_replies`, `wizard` and `answers`; `POST /api/v1/agent/connections/{id}/chats/{chat}/messages` accepts `quick_replies`. → [HTTP API](#http-api) |

## Overview

```
AI: ask(room, text, quick_replies | wizard)        person / API: POST /rooms/{id}/messages
        │ POST /agent/rooms/{room}/messages               with quick_replies | wizard
        ▼
rooms.Post  ──► validate + canonicalise (internal/rooms/interactive.go)
        │                             interactive_json = {"v":1,…}
        ▼
messages (migration 0026)  ──► message_views  ──► dto.Message.interactive
        │                                              │
        │                                          live room.message / history / pending
        ▼                                              ▼
person answers in the bubble  ──► POST /rooms/{id}/messages
   {"reply_to":"<q>","answers":{"quick_reply":"r1"}}
        │
        ▼
rooms.Post: the target carries a question, the caller is not its author, no answer yet
        │   answers_json = {"v":1,"quick_reply":"Sim"}   text = "Sim"
        ▼
a normal reply message ──► delivery.Wants → the asking AI's frame (meta answers…)
        │
        └─► notifications, unread counts, history, the live socket: unchanged
```

The interactive part is read and written with its message; nothing about it is asynchronous. The one
new server rule with a race is "one answer per author", and the partial unique index settles it in the
database, not in application code.

## Data model

### Migration `0026_quick_replies.sql`

```sql
-- Quick replies and question wizards (QR-01..QR-09).
-- interactive_json is the question a message asks, empty for every other message: at most 4 quick
-- replies or a wizard of at most 8 questions, one versioned JSON document whose ids the server
-- assigns. answers_json is the structured answer of a reply, empty for every other message. Both are
-- only written when the message is created, and both are cleared when the message is deleted
-- (DLT-01, QR-04), so a deleted message keeps nothing of what it asked or answered.
ALTER TABLE messages ADD COLUMN interactive_json TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN answers_json TEXT NOT NULL DEFAULT '';

-- message_views lists the messages column by column, so it is recreated to expose the two new
-- columns; it is dropped first, as 0023 did. The author columns are unchanged.
DROP VIEW message_views;
CREATE VIEW message_views AS
SELECT m.id, m.room_id, m.seq, m.author_kind, m.author_id, m.author_user_id, m.author_iid,
       m.sender_id, m.sender_alt_id, m.sender_name, m.kind, m.text, m.reply_to,
       m.recipients_json, m.external_id, m.created_at, m.edited_at, m.deleted_at,
       m.interactive_json, m.answers_json,
       CAST(CASE
            WHEN m.author_kind = 'system' THEN 'Partyline'
            WHEN m.author_kind = 'agent' THEN COALESCE(NULLIF(rm.display_name, ''), i.display_name, '')
            WHEN m.author_kind = 'user' THEN COALESCE(i.display_name, '')
            ELSE COALESCE(i.display_name, NULLIF(m.sender_name, ''), c.title, '')
            END AS TEXT) AS author_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS author_handle,
       CAST(COALESCE(i.kind, '') AS TEXT) AS author_ikind,
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS author_avatar
FROM messages m
LEFT JOIN intelligences i ON i.id = NULLIF(m.author_iid, '')
LEFT JOIN room_members rm ON m.author_kind = 'agent' AND rm.room_id = m.room_id
                         AND rm.kind = 'agent' AND rm.member_id = m.author_id
LEFT JOIN chats c ON m.author_kind = 'chat' AND c.id = m.author_id;

-- One structured answer per author per question (QR-08): a member answers once; a deleted answer
-- clears answers_json, so deleting and answering again works. Plain replies carry an empty
-- answers_json and are not affected.
CREATE UNIQUE INDEX messages_answer_once ON messages (reply_to, author_iid)
    WHERE answers_json <> '';
```

AD-012 is respected: two `ADD COLUMN`s and a view recreate, no table rebuild. The view definition is
`0023_message_edit_delete.sql:16-37` plus the two columns; a later change to the view must copy both.

### The canonical documents

`interactive_json`, exactly one of two shapes (versioned so a later field can be added without breaking
a stored row or an installed client):

```json
{"v":1,"quick_replies":[{"id":"r1","label":"Sim"},{"id":"r2","label":"Não"},{"id":"r3","label":"Depois"}]}
```

```json
{"v":1,"wizard":{"questions":[
  {"id":"q1","title":"Quando?","kind":"single","allow_text":false,
   "options":[{"id":"q1o1","label":"Amanhã"},{"id":"q1o2","label":"Na sexta"}]},
  {"id":"q2","title":"O que entra?","kind":"multi","allow_text":true,
   "options":[{"id":"q2o1","label":"API"},{"id":"q2o2","label":"Web"}]}
]}}
```

`answers_json`, one of two shapes:

```json
{"v":1,"quick_reply":"Sim"}
```

```json
{"v":1,"answers":[
  {"question":"Quando?","values":["Amanhã"],"text":""},
  {"question":"O que entra?","values":["API","Web"],"text":"antes do almoço"}
]}
```

- **Ids** are assigned by the server from the order the author gave (`r1…r4`, `q1…q8`,
  `q1o1…q1o8`). The author sends labels only; a model cannot invent a duplicate id, and the ids never
  depend on the labels' text, which a person cannot change after posting.
- **The stored answer keeps labels, not ids**: the answer is what the AI reads and what the bubble
  matches, so it is self-describing. Duplicate labels are refused in one set (see Validation), which
  makes a label a key of its own question.
- **Unknown versions** parse to "no interactive part" (`dto.ParseMessageInteractive`), so a message
  stored by a future version is still readable as a plain text message by this one.
- The document is stored as written by `encoding/json` over the DTO structs (no field order
  guarantees needed; it is read by the same code).

### Queries (`internal/store/queries/messages.sql`)

- `CreateMessage` gains `interactive_json, answers_json` (both written once, at insert).
- `MarkMessageDeleted` gains `interactive_json = ''`, `answers_json = ''` next to `text = ''`, so a
  deleted question or answer keeps nothing (DLT-01, QR-04).
- `CountAnswersByAuthor(question_id, author_iid)` — `SELECT COUNT(*) … WHERE reply_to = ? AND
  author_iid = ? AND answers_json <> ''` — gives the service a clean 409 before the insert; the unique
  index is the race guard behind it.
- `EditMessageText` is unchanged: it writes the text only, so the interactive part survives an edit
  (QR-04).
- Everything that lists messages (`ListMessagesBefore`, `ListMessagesAfter`,
  `ListLatestMessagesInRooms`, `GetMessageView`) selects `message_views` or `messages`, so the two
  columns arrive in the generated structs after `make generate`; no query text changes for reads.

### Lifecycle

| Step | Effect |
| --- | --- |
| A question is posted | `interactive_json` written in the same insert as the text; one `MessageCreated` event; live `room.message` for members, an AI frame for the members whose delivery wants it |
| The author edits it | `EditMessageText` only; `room.message_updated` carries the same interactive part (QR-04) |
| The author (or a manager) deletes it | `MarkMessageDeleted` clears text, interactive part, answers, attachments and reactions; a new answer is 409 (QR-09) |
| A member answers | A new message with `reply_to` and `answers_json`, text rendered by the server; one `MessageCreated` event; live and delivery as any reply |
| The member deletes their answer | Clears its text and `answers_json`; the partial index no longer sees the row, so the member may answer again (QR-08) |
| The room is deleted | The existing cascade removes the messages; the index goes with them |
| Retention removes a message | The message's rows go; no orphan state exists to clean up |

## Validation

The two shapes are validated in one pure file, `internal/rooms/interactive.go`; `rooms.Post` calls it
before anything is stored (`internal/rooms/messages.go:104-121`) and adds the three presence rules
that need the stored message (`reply_to` set, no `text`, no `files` in `checkAnswer`,
`internal/rooms/interactive_post.go`). The API maps `*validation.Error` to 422 `invalid` with one
reason per field (`internal/api/router.go:202-208`, `internal/validation/validation.go:9-41`).
Labels and titles are trimmed before they are measured and stored; a line break is refused rather
than collapsed because a button is one line.

| Rule | Limit | Field |
| --- | --- | --- |
| Quick replies per message | 1–4 | `quick_replies` |
| A quick reply label | 1–48 characters, one line | `quick_replies` |
| Two equal quick reply labels (case-insensitive) | refused | `quick_replies` |
| `quick_replies` together with `wizard` | refused | `quick_replies` (the first field) |
| Questions per wizard | 1–8 | `wizard` |
| A question title | 1–200 characters, one line | `wizard` |
| Options per question | 1–8 | `wizard` |
| An option label | 1–48 characters, one line | `wizard` |
| Two equal option labels in one question (case-insensitive) | refused | `wizard` |
| A question kind | `single` (default) or `multi` | `wizard` |
| A free-text answer | at most 280 characters (trimmed) | `answers` |
| Answers without `reply_to` | refused | `answers` |
| Answers together with `files` | refused (an answer is a text reply) | `answers` |
| Answers together with `text` | refused (`text` must be empty; the server renders it) | `text` |

Answers are validated against the **stored** question, never against the request: every question id
must exist once, every option id must belong to its question, a `single` question has exactly one
option (with an optional text note when `allow_text`) or, when `allow_text` and no option is chosen, a
non-empty text, a `multi` question has at least one option and an optional text note. A message that is not a question, a deleted question, the asker's own question and a
second answer are separate service errors, not field errors (see [The answer](#the-answer)).

The limits are exported constants on `dto` (`MaxQuickReplies`, `MaxWizardQuestions`,
`MaxQuestionOptions`, `MaxQuestionLabel`, `MaxQuestionTitle`, `MaxAnswerText`), so tygo emits them to
`web/src/lib/api/types.gen.ts` and the bubble's `maxLength` and validity rules cannot drift from the
server (the composer already imports `MaxTextLength` this way, `web/src/components/composer.tsx:41`).

The rendered text is checked with `validateText` (`internal/rooms/messages.go:198-210`, at most 16,000
characters) although the limits bound it well below: 8 questions × (200 title + 8 × 48 labels + 280
text) ≈ 7.1 kB. A rendered answer is never rejected there.

## The answer

### Who answers

`rooms.Post` already authorizes with `access.LevelPost` and checks the room's counterpart
(`checkCounterpart`, `internal/rooms/messages.go:131-168`). Answering adds, in this order:

1. the post level (existing 403/404 rules);
2. the room is not history-only and its DM counterpart is not disabled (existing, QR-09);
3. `reply_to` names a message of the room (existing `checkReplyTo`, `internal/rooms/messages.go:270-283`);
4. that message carries a question (`interactive_json <> ''`) → else 422 `invalid` on `answers`;
5. the caller's intelligence is not the question's author (`author_iid`) → else 403 `forbidden`;
6. the question is not deleted → else 409 `conflict` (`rooms.ErrMessageDeleted`);
7. the caller has no live answer to it (`CountAnswersByAuthor`) → else 409 `already_answered`
   (`rooms.ErrAlreadyAnswered`). The insert is still guarded by the partial unique index; a
   constraint failure maps to the same sentinel, never a 500.

The author is `p.IID()` — the user's id, or the AI's id when the AI posts through its own route. The
rule therefore reads identically for people and AIs. A DM with an AI and its owner's user has two
different intelligences, so the user may answer the AI's question, which is the main flow.

A chat sender never answers with a question part: `PostFromChat` (`internal/rooms/messages.go:169-189`)
has no interactive input, and a platform message is data (AD-006).

An AI may also answer a question in the thread with `reply`, as it answers any message: that is a text
message without `answers_json`, so the bubble keeps its unanswered state and the asker reads the text.
The MCP tools do not expose a structured answer in v1 (Out of Scope), so an AI-to-AI question is a
question the thread answers in words.

### The two shapes

| Question | Request body (with `reply_to` = the question) | Stored `answers_json` | Rendered text |
| --- | --- | --- | --- |
| Quick replies | `{"answers":{"quick_reply":"r2"}}` | `{"v":1,"quick_reply":"Não"}` | `Não` |
| Wizard, single | `{"answers":{"items":[{"question_id":"q1","option_ids":["q1o2"]}]}}` | `{"v":1,"answers":[{"question":"Quando?","values":["Na sexta"]}]}` | `Quando?: Na sexta` |
| Wizard, multi + note | `{"answers":{"items":[{"question_id":"q2","option_ids":["q2o1","q2o2"],"text":"antes do almoço"}]}}` | `{"v":1,"answers":[{"question":"O que entra?","values":["API","Web"],"text":"antes do almoço"}]}` | `O que entra?: API, Web (antes do almoço)` |
| Wizard, single answered by text only | `{"answers":{"items":[{"question_id":"q1","text":"depois das 10h"}]}}` | `{"v":1,"answers":[{"question":"Quando?","values":[],"text":"depois das 10h"}]}` | `Quando?: depois das 10h` |

A text note on a `single` question that also has a chosen option is stored the same way as on a
`multi` question: in `text` and in parentheses after the labels (`Quando?: Na sexta (depois das 10h)`).
A text on a question without `allow_text` is refused (422 on `answers`).

The wizard's lines are joined with `\n` in the wizard's order. The text is what people read in the
timeline, the conversation preview, the history of an AI and a notification preview; the structured
copy is what the asking AI acts on.

### Edit and delete

- **A question is edited** by its author: only the text changes (EDT-01); labels and ids stay, so every
  answer keeps matching. `room.message_updated` carries the message as it is now.
- **A question is deleted** by its author or a group manager (AD-026): the placeholder loses the
  interactive part, so the bubble becomes a deleted message. Answers already posted stay as replies to
  a deleted message; the reply context shows "message deleted". A new answer is 409.
- **An answer is not editable**: `EditMessage` returns 422 `invalid` on `text` ("an answer is deleted and
  posted again, never edited") because the human text and the structured copy must agree.
  Deleting the answer clears both, and the partial index frees the member's slot (QR-08).

## HTTP API

No new path; the existing routes gain fields. They are under the same middleware chain as today
(`internal/api/middleware.go`): an agent token is confined to `/api/v1/agent/`, a state-changing
session request needs a same-origin `Origin`.

| Route | Caller | New request fields | Answer |
| --- | --- | --- | --- |
| `POST /api/v1/rooms/{id}/messages` (JSON or multipart) | a member with post | `quick_replies` (labels), `wizard` (questions), or `answers` with `reply_to` | 201 `Message` with `interactive` or `answers` |
| `POST /api/v1/agent/rooms/{room}/messages` | an AI member | the same fields | 201 `Message`; counts one of the AI's 30 posts a minute (`internal/api/agent_handlers.go:590-600`) |
| `POST /api/v1/agent/connections/{id}/chats/{chat}/messages` | the AI that operates the connection | `quick_replies` (labels) | 201 `PlatformSendResult`; the text sent carries the numbered list |

Request DTOs (`internal/api/dto/quickreplies.go`, `internal/api/dto/rooms.go:225-232`):

```go
type PostMessageRequest struct {
    Text         string                `json:"text"`
    ReplyTo      string                `json:"reply_to,omitempty"`
    Recipients   []string              `json:"recipients,omitempty"`  // ignored, CID-22
    QuickReplies []string              `json:"quick_replies,omitempty"`
    Wizard       *WizardRequest        `json:"wizard,omitempty"`
    Answers      *AnswerSubmission     `json:"answers,omitempty"`
}

type WizardRequest struct { Questions []WizardQuestionRequest `json:"questions"` }
type WizardQuestionRequest struct {
    Title     string               `json:"title"`
    Kind      MessageQuestionKind  `json:"kind,omitempty"`      // single (default) | multi
    Options   []string             `json:"options"`
    AllowText bool                 `json:"allow_text,omitempty"`
}

type AnswerSubmission struct {
    QuickReply string            `json:"quick_reply,omitempty"`
    Items      []AnswerItem      `json:"items,omitempty"`
}
type AnswerItem struct {
    QuestionID string   `json:"question_id"`
    OptionIDs  []string `json:"option_ids,omitempty"`
    Text       string   `json:"text,omitempty"`
}
```

Response DTOs: `dto.Message` gains `Interactive *MessageInteractive` and `Answers *MessageAnswers`,
decoded from the columns in `convert.Message` (`internal/api/convert/convert.go:55-72`). Because every
read path (history, pending, an AI's backlog, the live hub at `internal/live/hub.go:329-352`) converts
through that one function, the two fields appear everywhere a message does, and no new live event type
is needed.

Errors:

| Case | Status | Code |
| --- | --- | --- |
| Any limit or shape rule of the question | 422 | `invalid`, field `quick_replies`, `wizard` or `text` |
| The answer body does not match the stored question | 422 | `invalid`, field `answers` |
| Answers without `reply_to`, or `reply_to` is not a question of the room | 422 | `invalid`, field `answers` |
| The caller's own question | 403 | `forbidden` |
| A deleted question | 409 | `conflict` |
| A second answer by the same author | 409 | `already_answered` (new `dto.ErrorCodeAlreadyAnswered`) |
| The conversation refuses a post (history, disabled counterpart) | existing | `history_only`, `counterpart_disabled` |

`rooms.ErrAlreadyAnswered` is mapped in `internal/api/router.go:202-…` next to the other sentinels;
`already_answered` is a new value of `dto.ErrorCode` (`internal/api/dto/common.go:85-170`), so the web
can tell it from a generic conflict (QR-11).

## Agent channel

### `ask`

A new tool next to `reply` and `send_message` (`internal/channel/tools.go:44-104`). It is separate
rather than three more arguments on `send_message` because its schema is a tree (questions, options,
kinds) and its description is a different instruction to the model; `send_message` keeps its flat
schema and its target rules.

```json
{
  "type": "object",
  "properties": {
    "room":          {"type": "string", "description": "room id, room name, or @handle of a DM with the intelligence to ask"},
    "connection":    {"type": "string", "description": "instead of room: the connection_id of a connection you operate, together with chat"},
    "chat":          {"type": "string", "description": "with connection: the chat_id to ask in, or the address of someone who never wrote to you, as for send_message"},
    "reply_to":      {"type": "string", "description": "with room: optional message_id this question answers"},
    "text":          {"type": "string", "description": "the question, at most 16000 characters"},
    "quick_replies": {"type": "array", "items": {"type": "string"}, "description": "1 to 4 buttons, each 1 to 48 characters on one line"},
    "wizard":        {"type": "object", "properties": {"questions": {"type": "array", "items": {
        "type": "object",
        "properties": {
          "title":      {"type": "string"},
          "kind":       {"type": "string", "enum": ["single", "multi"]},
          "options":    {"type": "array", "items": {"type": "string"}},
          "allow_text": {"type": "boolean"}
        },
        "required": ["title", "options"]}}, "description": "1 to 8 questions"}},
      "description": "instead of quick_replies: a wizard answered step by step in the web app"}
  },
  "required": ["text"]
}
```

Behaviour:

- Exactly one of `room`, or `connection` with `chat`; `to` is not in the schema and is ignored (a DM
  is `room: "@handle"`, as with `send_message`); `reply_to` only with `room`. The refusals are the
  tool's own (`askNoTarget`, `askHalfTarget`, `askSeveralTargets`), worded like the `send_message` ones
  (`internal/channel/tools.go:269-275`) but without `to`.
- At least one of `quick_replies` or `wizard`; otherwise the tool says to use `send_message`.
- `room` → `POST agent/rooms/{room}/messages` with the three fields and returns `sent (message_id …)`.
  A 422 becomes a tool error naming the field, because the server's `validation.Error` message already
  names it (`"invalid quick_replies, wizard"`) and the tool passes `err.Error()` through.
- `connection` + `chat` → `POST agent/connections/{id}/chats/{chat}/messages` with `text` and
  `quick_replies`; a `wizard` there is refused with "a wizard needs a room: on a chat, ask with
  quick_replies and the person answers with a number". It returns `platformSent(...)`
  (`internal/channel/tools.go:189-199`), so the AI learns whether the platform took it or Partyline
  retries.
- It uses the same guarded call as every tool (`guard`, `internal/channel/tools.go:114-130`), so a lost
  link, a busy session or a deleted AI is reported, not thrown.
- The shared instruction texts (`internal/channel/instructions.go`) are not changed in v1: the tool's
  own description is what the model reads before calling it, and `docs/agents.md` documents the tool
  for an operator. A sentence in the shared paragraph would be a second place to keep in sync.

### Frames

`channel.Format` (`internal/channel/format.go:32-81`) builds the notification's meta and content; the
frame escapes it as attributes (`internal/channel/frame.go:29-52`). Two keys are added:

| Meta key | Value | When |
| --- | --- | --- |
| `interactive` | the compact JSON of the question (`{"v":1,…}`) | the message carries a question (QR-21) |
| `answers` | the compact JSON of the structured answer | the message carries an answer |

The content stays the human text, so an adapter that ignores meta still shows something sensible:
`Deploy hoje?` for the question and `Sim` for the answer, plus the existing `reply_to` meta naming the
question. A frame never carries the ids in a second form.

The answer reaches the asking AI through the existing delivery rules: an answer is a reply to the
question's message, and `delivery.Wants` returns true for `ReplyToAuthorIsMe` in `mentions` mode
(`internal/delivery/wants.go:66-92`, `:157-179`). In a group where another member asks, the answer is
delivered by the same rules as any other message; nothing is special-cased (QR-22).

## Platforms

The recorded sends of a connection are not room messages: `rooms` sends to no chat (CID-22) and
`connectors.Sends` writes `platform_sends`, not `messages` (`internal/connectors/sends.go:148-290`).
A question asked on a connection therefore has no room message to carry it, which is exactly the
degradation the brief asks for.

- `ask` with `connection` + `chat` sends `text` plus the numbered labels, appended by the API handler
  before the send is stored:

  ```
  Deploy hoje?

  1. Sim
  2. Não
  3. Depois
  ```

  `rooms.NumberedOptions(text, labels)` is a pure function: one blank line, then `1. label` per line,
  ASCII digits, no prose (localisation belongs to the author's text, which the model writes in the
  reader's language).
- `SendToChatRequest` gains `quick_replies []string`; `agentConnectionSend`
  (`internal/api/agent_handlers.go:776-800`) validates the labels with the same rules (1–4, 1–48,
  one line, no duplicates) and replaces the outgoing text with the numbered one. The stored
  `platform_sends.text` is what was sent, so a retry sends the same numbered text.
- The person's reply arrives through the connection's normal inbound pipeline as a chat message, and
  the AI reads it with `reply`/`listen` as usual. There is no mapping from "1" to an option: the send
  carries no stored question to map against, and parsing a number out of prose would guess (open
  question 2).
- A wizard is refused on a connection; a chat cannot render a step-by-step form, and the brief limits
  the platform path to numbered text.
- A multipart send (a file with a caption) ignores `quick_replies`: a file is sent once and has no
  buttons; `ask` never sends a file.
- A webhook connection sends text like the others; its platform supplementary data carries the
  numbered text, nothing else.

## Web

### Quick replies

`web/src/components/question-bubble.tsx` renders under the message text, between it and the
attachments (`web/src/components/message-item.tsx:401-440`):

- a `role="group"` with `aria-label="Quick replies"` (`qr.group`) and one `<button type="button">` per
  label, `min-h-11` (44 px), `w-full`-ish in a `flex flex-wrap gap-2`, `text-start`,
  `break-words`, so four 48-character labels wrap inside the bubble at 320 px instead of widening it;
- while a request is in flight: `aria-disabled="true"` on every button and a `role="status"` line
  `qr.sending`; a tap guard means a second tap does nothing;
- after success: the chosen button has `aria-pressed="true"`, every button has `aria-disabled="true"`,
  and the `role="status"` line reads `qr.answered` with the label. Focus stays on the pressed button,
  so a keyboard user is not dropped at the top of the page (the alternative, replacing the group with
  text, loses focus);
- after an error: the buttons act again and a `toast.error` says `qr.failed` (or `qr.alreadyAnswered`
  for a 409 `already_answered`, which also invalidates the room's message query so the timeline shows
  the answer that was posted elsewhere, `web/src/lib/api/queries.ts:577-600`);
- the actions are absent (labels shown as plain text) when the viewer may not post, the question is the
  viewer's own, or the conversation is frozen (`canPost(r.my_level)` and `historyOnly`, as the room
  page already computes at `web/src/routes/_app/rooms/$roomId.tsx:95-130`); a `qr.ownQuestion`,
  `qr.noPermission` or `qr.frozen` line says why (QR-13).

The answer is posted with its own `useMutation` (`mutationKey: ["answer-question", roomId]`, the same
`POST /rooms/{id}/messages` body as the composer, `web/src/lib/api/queries.ts:587-599`) so the bubble's
pending state and the composer's are independent and the room page's `postMessageKey` error banner does
not react to an answer: `{ text: "", reply_to: question.id, answers: { quick_reply: option.id } }`. The
stored message arrives through the live `room.message` event, so the timeline has one source of appends.

### The wizard

`QuestionWizard` (in the same file) is a small state machine over `wizard.questions`:

- state: the current step index, one `answers` array (`{optionIds, text}` per question), `submitting`,
  and `sent` (the result). No persistence: a reload starts at step 0 (out of scope).
- header: `qr.step` ("Step {current} of {total}") in a polite `role="status"`; one text line, no
  progress bar (a bar adds nothing at this size and is one more thing that can overflow).
- one question per step: a container with `role="radiogroup"` for `single` (native
  `<input type="radio" name={question.id}>`) or `role="group"` for `multi` (checkboxes), labelled by
  the step heading (`aria-labelledby`); the heading is a real `<h4 tabIndex={-1} ref>` so it can take
  focus when the step changes (QR-16, QR-17). Each option is a `<label>` row of `min-h-11` with the
  input first, the label `break-words`; the whole row is the 44 px target.
- `allow_text`: a `<textarea rows={2} maxLength={280}>` labelled `qr.textLabel`, with
  `aria-describedby` pointing at a hint (`qr.textHint`, "Up to 280 characters"); the hint is small text
  inside the bubble, never a tooltip.
- footer: Back (`qr.back`) and Next (`qr.next`), each `h-11 flex-1`, hidden/disabled as the step
  requires; on the last step Next becomes Send (`qr.send`). Next and Send are `disabled` until the step
  is valid (`singleValid`, `multiValid` pure functions in `web/src/lib/questions.ts`, unit-tested), so
  the rule is not buried in JSX.
- on Send: the submission is built from the answers (`question_id`, `option_ids`, `text`) and posted
  through the same `useMutation` as a quick reply, `{text:"", reply_to, answers:{items}}`; while it is
  in flight the footer shows `qr.sending` and the fields are read-only; on success the component
  renders the summary.
- the summary: `qr.yourAnswer` as a heading (focused on mount, `tabIndex={-1}`) and a `<dl>` whose
  `<dt>` is the question title and `<dd>` the chosen labels and the text (`qr.noOption` for an empty
  `values`), followed by `qr.readOnly` ("Delete your answer to answer again"). It is the same shape
  the stored answer has, so the summary and the reply never disagree.
- after a reload, the question is read-only with the same summary, derived from the viewer's answer in
  the timeline (below).

### The answered state is derived

`MessageTimeline` builds a `Map<questionId, Map<authorId, Message>>` from the messages it already holds
(`useTimelineMessages`, `web/src/components/message-timeline.tsx:58-62`): for every message with
`answers` and `reply_to`, it records the answer by `author_id`. It passes `myAnswer` (the answer by
`meId`) to `MessageItem` and therefore to the bubble. This is why there is no server aggregation and no
new live event: the answer is a message in the timeline, and another member's answer simply appears as
a message (QR-18). It also means the state is correct after a reload on any device, and a timeline with
the answer not loaded (it is always the next rows after the question) shows the question as unanswered
— the safe direction, and the answer is one page-load away.

`isGrouped` (`web/src/components/message-timeline.tsx:77-84`) returns false for a message with
`interactive` or `answers`, so a question always shows its author and time and an answer is never
folded into its question's row.

### Live, preview and notifications

Nothing changes: `room.message` carries the question and the answer because `convert.Message` maps the
columns, and `room.message_updated` carries an edited or deleted question as it is now
(`web/src/lib/live/handlers.ts:190-215`). The conversation preview and a notification preview read the
message text, which for an answer is the rendered lines (possibly several); the existing preview
collapses whitespace and truncates, so no change is needed.

### Fit and i18n

- The bubble is a block inside the message row: `max-w-full`, `min-w-0`, labels and titles
  `break-words`, buttons `flex-wrap`, the text area `w-full`. At 320 px with a 125% root font the
  bubble grows in height only. The fit e2e seeds a 4-button question with 48-character labels and an
  8-question wizard with long titles and options and measures both phones (QR-24).
- Every string is a key in `web/src/lib/i18n/en.ts` and `pt-BR.ts` under `qr.*`; the dictionaries must
  have the same key set (the type of `pt-BR.ts` and `i18n.test.ts` enforce it).

## Tests

The tests derive from the spec's acceptance criteria and are written before the code. Values below are
the expected outcomes.

| Layer | Tests | Expected values |
| --- | --- | --- |
| Migration/store (`internal/store/quick_replies_migration_test.go`) | `0026` on a database before it and on a fresh one; view columns; unique index; delete clears | `interactive_json`/`answers_json` default `''`; two answers by one author on one question fail the index; a second author passes; a plain reply passes; after `MarkMessageDeleted` both are `''` |
| Pure rules (`internal/rooms/interactive_test.go`) | limits, canonical ids, answer shape, rendered text, numbered options | 4 ok / 5 → 422 `quick_replies`; 8 ok / 9 → `wizard`; 48 ok / 49 or a newline → 422; ids `r1..`, `q1..`, `q1o1..`; `{"quick_reply":"r2"}` → "Não"; wizard → "Quando?: Na sexta\nO que entra?: API, Web (antes do almoço)"; numbered "1. Sim\n2. Não" |
| Post/answer service (`internal/rooms/interactive_post_test.go`, package `rooms`) | post a question, answer it, refuse own/deleted/second/not-a-question; edit; delete | stored columns match the canonical JSON; answer text rendered; 403 own; 409 deleted; 409 already; 422 not-a-question; edit keeps `interactive_json`; delete clears both |
| Routes (`internal/api/quick_replies_test.go`) | both room routes, the connection send, the statuses | 201 with `interactive`/`answers`; 422 field names; 403 own; 409 `already_answered`; 409 `conflict` deleted; the connection send's stored text ends with the numbered lines |
| Channel (`internal/channel/tools_test.go`, `format_test.go`) | `ask` room and connection, its refusals; frame meta | room → `sent (message_id …)`; connection → `not sent yet: …`/`sent`; no target/several targets/wizard on a connection → the documented errors; frame has `interactive`/`answers` JSON and `content` "Sim" |
| Web quick replies (`web/src/components/question-bubble.test.tsx`) | offer, tap, pending, chosen, error, own/no-post/frozen, reload state | one POST with `reply_to` and `answers`; `aria-pressed`/`aria-disabled` after; status text; own question has no button; `answers` in the loaded rows makes it read-only |
| Web wizard (`web/src/components/question-bubble.test.tsx`) | steps, validity, focus, submit, summary, failure, keyboard | Next disabled until valid; focus on the new heading; one POST with all `items`; summary values; a 500 restores the steps |
| Fit e2e (`web/e2e/fit.spec.ts`) | the conversation with a seeded 4-button question and an 8-question wizard, 320 and 412 at 125% | no sideways scroll, no spilled text, the wizard's Next is reachable |
| Flow e2e (`web/e2e/quick-replies.spec.ts`) | tap a quick reply, complete a wizard, reload | the reply text "Sim"; one reply with all lines; the chosen/summary state after a reload |
| Docs | none (build gate) | `docs/api.md`, `docs/agents.md` and `.specs/STATE.md` name the fields, limits, tool, meta and decision |

## Code Reuse Analysis

| Existing component | Location | How the feature uses it |
| --- | --- | --- |
| `rooms.Post`, `post`, `insert` | `internal/rooms/messages.go:104-320` | The one write path for a question and an answer: `PostInput` gains the two shapes and `draft`/`insert` write the two columns in the same transaction and the same `MessageCreated` event |
| `checkReplyTo`, `authorize`, `checkCounterpart` | `internal/rooms/messages.go` | The answer's target, permission and history rules are the existing ones, plus three question-specific checks |
| `EditMessage` / `DeleteMessage` | `internal/rooms/message_edit.go` | The edit keeps the interactive part; the delete clears it through the existing `MarkMessageDeleted` statement |
| `validation.Check` and `httpx.Invalid` | `internal/validation`, `internal/api/httpx` | The same 422 envelope with one reason per field; the web already reads `error.fields` |
| `dto.Message` + `convert.Message` | `internal/api/dto/rooms.go:175-213`, `internal/api/convert/convert.go:55-72` | One conversion adds the two fields to history, pending, backlog, the live hub and every route response |
| `MessageTimeline` / `MessageItem` | `web/src/components/message-timeline.tsx`, `message-item.tsx` | The bubble hangs inside the existing row; the answered map reuses `useTimelineMessages`; grouping learns one rule |
| `usePostMessage` | `web/src/lib/api/queries.ts:580-600` | The same `POST` path and body shape; the bubble has its own `useMutation` so its pending state is separate, and the live event is still the only timeline writer |
| Tool and frame plumbing | `internal/channel/tools.go`, `format.go`, `frame.go` | `ask` is one more registered tool with the shared guard, decode and target checks; the two meta keys ride the existing frame |
| `delivery.Wants` and the live hub | `internal/delivery/wants.go`, `internal/live/hub.go` | No change: a reply to the AI's message reaches it, and the live frame already carries the whole message |
| Existing app stores and routes | `internal/store`, `internal/api/router.go` | Migration `0026`, two SQL statements changed, one query added; no new route, no new package, so `internal/archtest` is untouched |

**Integration points**: the message store and its view, the rooms service, the HTTP room and connection
routes, the channel tools and frames, and the web timeline/composer mutations. The live socket, the
notification worker and the delivery dispatcher are deliberately not integration points.

## Error Handling Strategy

| Scenario | Handling | What the user sees |
| --- | --- | --- |
| A limit or shape of a question is violated | 422 `invalid` with the field; nothing stored | The API client names the field; a tool says `invalid quick_replies` |
| The answer body does not match the question | 422 `invalid` on `answers` | The toast says the answer could not be sent (`qr.failed`) |
| The asker answers their own question | 403 `forbidden` | The web never offers the buttons (`qr.ownQuestion`) |
| The question was deleted | 409 `conflict` | `qr.failed`, and the bubble disappears when the delete's `room.message_updated` arrives |
| A second answer by one member | 409 `already_answered`; the web refreshes the timeline | `qr.alreadyAnswered` ("You already answered this question") and the stored answer shows |
| Two answers race | The unique index refuses the second insert; the same 409 | The same sentence, never a 500 |
| The answer post fails (network, 500) | The bubble returns to its editable state; a toast | "Could not send your answer. Try again." |
| The platform refuses a send | The existing pending/retry behaviour of `platform_sends` | The tool says "not sent yet: …", the AI does not resend |
| A corrupt `interactive_json` (tampering, a future version) | `ParseMessageInteractive` returns nil; the message is plain text | The text is still readable, no broken UI |
| A question message with files | Allowed; the bubble sits under the text and above the files | The files are shown as usual |
| The viewer is offline | The post mutation fails; the buttons return | "Can’t reach the server…" (the existing toast) |

## Risks & Concerns

| Concern | Location (file:line) | Impact | Mitigation |
| --- | --- | --- | --- |
| The answered state is derived from loaded timeline rows | `web/src/components/message-timeline.tsx:58-62` | A question whose answer is not in the loaded pages shows as unanswered | The answer is the next rows after its question; posting (or a reload) puts it in the timeline; the server still refuses a second answer |
| The unique index depends on `author_iid` | migration `0026` | An answer by a principal without an intelligence id would not be covered | Every principal that may post (a user, an AI) has an `author_iid`; a chat sender never answers with a structured answer |
| The unique-violation check reads the driver's message text | `internal/rooms/interactive.go` | A driver upgrade could change the text and turn a 409 into a 500 | The same string match already guards handles (`internal/intelligences/service.go:595`); the pre-check in `CountAnswersByAuthor` returns 409 without relying on the text, and the text path is the race fallback only |
| The frame carries JSON in an attribute | `internal/channel/frame.go:29-52` | A quote in a label is escaped as `&quot;`, which an adapter must unescape | That is how the frame already carries every attribute; labels and titles are plain text and the content stays human-readable |
| The wizard state is in memory | `web/src/components/question-bubble.tsx` | A reload loses a half-finished wizard | Out of scope by the brief: a reload starts at step 1; answers are only stored on Send |
| The rendered answer text is server-side and not localised | `internal/rooms/interactive.go` | The "Title: answer" shape is the same in every language | The titles and labels come from the asker, who writes them for the reader; only the `: ` and ` (…)` punctuation is fixed |
| Four 48-character labels on a 320 px phone | `web/src/components/question-bubble.tsx` | A tall bubble, not a wide one | Buttons wrap (`flex-wrap`, `break-words`); the fit e2e measures it with a 125% root font |
| A question to a platform cannot carry its ids back | `internal/api/agent_handlers.go:776-800` | "1" is not turned into a structured answer | Open question 2: the AI reads the number; the design keeps the platform path to text |
| Answers are normal messages | `internal/rooms/messages.go` | Every answer notifies and counts as unread, as any reply does | That is the desired behaviour: the asker must see the answer; the notification preview carries the rendered text |

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| A separate `questions` table with answers as rows | A question is read with its message on every path (history, pending, live, backlog); a join or a second fetch would touch all of them, and the answer is a message the delivery rules already carry |
| Answers in a table, not as messages | The brief asks for a reply in the conversation; a message keeps the order, the read cursors, the notifications and the AI delivery unchanged |
| A new live event `room.question_answered` | The answer is a `room.message` already; a second event would duplicate the state and need its own ordering rules |
| Ids from the author | A model can send duplicates or change them between the question and the answer; the server's ids are stable and cheap |
| Labels as the answer key only | Duplicate labels would make the chosen option ambiguous; refusing duplicates in one set keeps labels usable as keys and readable in the JSON |
| A wizard composer in the web app | The brief says a person sends questions through the API; the only author UI is the bubble that answers |
| Native platform buttons | Out of scope by the brief; templates/keyboards differ per platform and need provider setup |
| Mapping "1" back into a structured answer | The send stores no question (a connection is not a room), and parsing prose would guess; open question 2 |
| A local pending answer in the browser | A lost answer is better than a stale one; the brief asks for a Send that posts, and the timeline is the source of truth |
| A modal or sheet for the wizard | The brief asks for the wizard **inside the message bubble**; a sheet would lose the context of the question |

## Operations

- **Backup**: nothing new. The two columns travel with the database; no key, no file.
- **Upgrade**: migration `0026` adds two columns and recreates the view, as `0023` did; it runs in the
  normal startup path before the server accepts requests.
- **Logs**: no new log line and no message text, label or answer in any log; the post path logs what
  it logged before.
- **Limits**: a question counts as one post against the existing per-AI limit (30 a minute);
  answering in the web app is a normal user post. There is no new rate limit.
- **Removal**: delete the question's message (AD-026 rules) or the whole room; no cleanup job is
  needed.
- **One instance**: nothing new is in memory on the server; the wizard's step state lives in the
  browser tab only.

## Decision record (proposed)

Record one AD in `.specs/STATE.md` (AD-029, the next free number) when the plan is approved: *a message
may carry a question — up to 4 quick replies or a wizard of up to 8 questions — stored as one
versioned JSON document on the message (`interactive_json`), and its answer is a normal reply that
carries the structured answers (`answers_json`); the server validates, assigns the ids and renders the
answer text, a member answers once per question, and a platform connection gets the options as
numbered plain text.* Scope: store (`0026`), rooms, `api/dto` and the room/connection routes, channel,
web-app, docs. No new package, so the architecture table is unchanged.

## Open questions

See [spec.md](spec.md#open-questions-for-the-product-owner): the two `Confirmed? n` rows (one answer per
member, and the platform reply staying plain text) are the only ones awaiting the owner.
