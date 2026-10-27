# Quick Replies and Question Wizards Specification

## Problem Statement

The product owner asked for answer buttons inside a message and, in the same breath, for a short
questionnaire inside the bubble (2026-10-27):

> "Buttons here in the chat to answer faster (like WhatsApp message buttons), but with multiple
> questions: a wizard of questions inside the message bubble, and when it finishes, it sends. And
> also just quick question buttons that I click and it goes."

So a message may carry an **interactive part**: a few **quick replies** (at most 4 buttons, e.g.
"Sim" / "Não" / "Depois") that are answered with one tap, or a **wizard** (at most 8 questions, one
at a time inside the bubble, with Back/Next and a final Send) that posts all answers as one reply.
The author of the question is an AI (through its channel tools) or a person (through the API); the
people who answer are the members of the room in the web app.

Every answer is an ordinary reply in the conversation — visible in history, delivered to the AIs
that care about it, notified like any message — and it also carries a **structured copy** the
asking AI can read without parsing prose. On a WhatsApp, Telegram or Discord connection the options
degrade to a numbered plain-text list: no native platform buttons in this version.

The decisions the brief fixes:

- the buttons live in the message bubble, for any member allowed to post in the conversation;
- a quick reply sends its answer at once; a wizard posts one reply when Send is pressed;
- the answer replies to the question, so the conversation keeps its thread;
- platform connections get numbered text, not buttons.

The decisions the design settles (see [design.md](design.md)) are: the JSON column on `messages`
(migration `0026`) and its server-side limits, the canonical ids of buttons and questions, who may
answer and how often, what an edit or a delete does to a question and to an answer, the `ask` tool,
the channel frame attributes, the numbered-text fallback, and the bubble's keyboard and screen
reader behaviour.

## Goals

- [ ] An AI can ask a question with up to 4 buttons or a wizard of up to 8 questions in a room or DM,
      and a person sees it as part of the message bubble in the web app.
- [ ] Tapping a quick reply posts the answer at once; the bubble then shows the chosen label and the
      other buttons stop acting.
- [ ] A wizard is answered one question at a time inside the bubble (single or multiple choice, an
      optional short text), with Back/Next and a final Send that posts **one** reply with all answers.
- [ ] The answer is a normal reply to the question and carries the structured answers, so the asking
      AI reads the choices from its channel frame without guessing.
- [ ] Any member who may post may answer a question once; the asker cannot answer their own question;
      the conversation stores what each member chose.
- [ ] On a WhatsApp, Telegram, Discord or webhook connection the options become a numbered plain-text
      list, and the answer comes back as an ordinary message.
- [ ] Every screen works at 320 px and 412 px wide with a 125% root font, every target is at least
      44 px, and a keyboard and a screen reader can complete both kinds of question.
- [ ] The feature is documented (`docs/api.md`, `docs/agents.md`, `.specs/STATE.md`) and covered by
      unit, integration, route and end-to-end tests, including the phone fit check.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Native platform buttons (WhatsApp interactive messages, Telegram inline keyboards, Discord components) | The brief asks for numbered plain text in this version |
| A composer in the web app for a person to create a question | The brief says a person sends one through the API; the AI tools are the other author |
| Changing the interactive part after the message is posted | An edit changes the text only (EDT-01, QR-04); delete and ask again instead |
| Changing an answer after it is sent | The answer is a message: a submitted wizard is read-only (the brief); deleting the answer and answering again is the way |
| Tallies ("3 people chose Sim") and a list of who answered what | Not asked; each answer is a reply in the thread and the bubble shows the viewer's own choice |
| Deadlines, expiry or manually closing a question | A question lives as long as its message; deleting it closes it |
| An AI submitting structured answers through the MCP tools | v1 is people answering in the web app; an AI answers a question with `reply` (text) |
| Keeping a half-finished wizard across a reload or another device | The brief asks for the wizard inside the bubble; a reload starts at step 1 |
| Buttons in push notifications, the conversation list or the activity feed | Those read the message text; the notification preview already carries it |
| Attachments in an answer | An answer is a text reply; a question may carry files, an answer does not |
| A question authored by a chat sender (WhatsApp, Telegram, Discord, webhook) | A platform message is data, never UI (AD-006); a chat cannot carry a question part |
| Localising the labels the author writes | Labels are content, like the message text: the author writes them in the reader's language |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| What one message may carry | Either 1–4 quick replies or one wizard of 1–8 questions, never both | Two interactive widgets in one bubble would need a rule for which comes first; the brief names them as two kinds of message | y |
| Where it lives | One JSON document per message, `messages.interactive_json`, plus `answers_json` for a reply (migration `0026`); empty for every other message | One column, one DTO field, validated once on the way in; no second table for something that is always read with its message | y |
| Who assigns button and question ids | The server (`r1..r4`, `q1..q8`, `q1o1..q1o8`), from the labels the author sends | The author writes labels; the answer must reference something stable, and duplicate ids from a model would be a validation error nobody needs | y |
| Limits | 4 quick replies; 8 questions; 8 options per question; a button/option label 1–48 characters, a single line; a question title 1–200 characters, a single line; a free-text answer 280 characters | Keeps the bubble inside 320 px and the answer text under MSG-02's 16,000 characters; the numbers come from the brief | y |
| What the answer is | A normal reply (`reply_to` the question) whose text is human-readable ("Sim", or one "Question: answer" line per wizard question) and whose `answers_json` is the structured copy | The brief asks for both; a reply keeps the thread, the delivery rules, the notifications and the history unchanged | y |
| Who renders the answer text | The server, from the structured answer, on the post | One source of truth: the AI, the timeline and the notification all read the same text, and a client cannot post a label that was never offered | y |
| Who may answer | A member with the post level who is not the question's author; in a DM that is the other side | "Anyone who may post" keeps groups useful; the asker does not need to answer their own question, and an AI cannot answer itself in a loop | y |
| How many times one member may answer | Once per question; a second attempt is 409 `already_answered` | The brief asks for a chosen state and a read-only wizard after Send; deleting the answer clears it and allows a new one | n (open question 1) |
| What an edit does | A question's edit changes its text only, the buttons stay; an answer cannot be edited (422 on `text`) | The ids of an answer must keep matching the question, and an edited answer would disagree with its structured copy | y |
| What a delete does | A deleted question loses its interactive part (the placeholder remains); a deleted answer loses its text and structured copy, so the member may answer again | Follows DLT-01: a deleted message keeps its place and nothing of what it said | y |
| What a platform gets | `ask` on a connection appends the labels as `1. label` lines and sends plain text; the reply is an ordinary chat message; a wizard is refused on a connection | The brief asks for numbered text; mapping "1" back into a structured answer would need inbound interpretation and is not asked | n (open question 2) |
| Where the "answered" state comes from | The web derives it from the reply the viewer posted (`reply_to` the question with `answers`); no server-side aggregation and no live frame for it | The answer is already in the timeline; a second source of truth would need its own event and its own race rules | y |
| Free text in a wizard | Per question, `allow_text`: a single-choice question may be answered by the text alone, and on any question a text is a note stored with the chosen labels | The brief says "optionally a short free text"; one rule for both kinds keeps the form and the stored answer testable | y |
| Editing the web after answering | The bubble becomes read-only and shows the chosen values; the other buttons stop acting | The brief asks for the chosen state and a read-only wizard | y |

**Open questions:** none blocking; the two rows marked `n` are adopted defaults awaiting the product
owner's confirmation (see "Open questions for the product owner" at the end).

---

## User Stories

### P1: A message carries a question ⭐ MVP

**User Story**: As a member, I want a message to offer me buttons or a short wizard, so that I answer
in one or two taps instead of writing a paragraph.

**Acceptance Criteria**:

1. WHEN a user or an AI posts a message with 1–4 `quick_replies` labels THEN the system SHALL store one
   canonical question document in `messages.interactive_json` (migration `0026`), with the ids `r1`,
   `r2`, … in the order given, and SHALL return it as `interactive` on every read of that message
   (history, pending messages, an AI's backlog and the live `room.message` frame). (QR-01)
2. WHEN a user or an AI posts a message with a `wizard` of 1–8 questions THEN the system SHALL store one
   canonical wizard document with ids `q1`…`q8` and `q1o1`…`q1o8`, each question `single` by default
   or `multi`, with `allow_text` false by default, and SHALL return it as `interactive` on every read
   of the message. (QR-02)
3. IF a post carries both `quick_replies` and a `wizard`, or more than 4 quick replies, more than 8
   questions, more than 8 options in a question, a label that is empty, longer than 48 characters or
   not on one line, a question title that is empty or longer than 200 characters, or two equal labels
   in one button set or one question THEN the system SHALL refuse with 422 `invalid` naming the field
   (`quick_replies` or `wizard`) and SHALL store nothing. (QR-03)
4. The system SHALL keep the message text required as it is today (MSG-02) and SHALL keep the
   interactive part immutable: WHEN the author edits the message THEN only its text SHALL change, and
   WHEN the message is deleted THEN the text, the attachments, the reactions and the interactive part
   SHALL be cleared from the placeholder. (QR-04)
5. WHEN a caller posts a reply whose body carries `answers` THEN the system SHALL require that
   `reply_to` names a message of the same room that carries a question, that the caller is not that
   question's author, that the question is not deleted and that the caller has not answered it yet,
   and SHALL refuse with 422 `invalid` (not a question), 403 `forbidden` (the caller's own question),
   409 `conflict` (a deleted question) or 409 `already_answered` (a second answer) otherwise. (QR-05)
6. WHEN the caller answers a quick reply THEN the system SHALL store
   `{"v":1,"quick_reply":"<label>"}` as the structured answer and SHALL set the posted message's text
   to that label. (QR-06)
7. WHEN the caller answers a wizard THEN the system SHALL require exactly one answer per question of
   the wizard (matched by question id, in any order), a `single` question answered by exactly one of
   its options — with an optional text of at most 280 characters when it allows text — or, when it
   allows text and no option was chosen, by a non-empty text, a `multi` question answered by one or
   more of its options plus an optional text of at most 280 characters, and SHALL store the question
   titles and the chosen labels as the structured answer and set the text to one `Title: answer` line
   per question. (QR-07)
8. The system SHALL allow at most one structured answer per author per question (a partial unique
   index on `messages (reply_to, author_iid)` where `answers_json` is not empty), SHALL refuse an
   edit of a message that carries one (422 `invalid` on `text`), and SHALL clear the text and the
   structured answer when that message is deleted, so the member can answer again. (QR-08)
9. IF the conversation refuses a post (a DM with an external person, a DM whose counterpart is
   disabled) THEN the system SHALL refuse an answer exactly as it refuses a post, and SHALL refuse an
   answer to a question whose message was deleted with 409 `conflict`. (QR-09)

**Independent Test**: Alice posts
`{"text":"Deploy hoje?","quick_replies":["Sim","Não","Depois"]}`; `GET /rooms/{id}/messages` returns
`interactive.quick_replies` with labels and ids `r1`, `r2`, `r3`; a post with
`{"reply_to":"<id>","answers":{"quick_reply":"r1"}}` answers 201 with text "Sim" and
`answers.quick_reply` "Sim"; the same body again answers 409 `already_answered`; a body with 5 quick
replies is 422 on `quick_replies`.

---

### P1: Answering with one tap ⭐ MVP

**User Story**: As a member reading a conversation, I want the question's buttons in the bubble, so
that answering takes one tap and I see what I chose.

**Acceptance Criteria**:

1. WHEN a message in the timeline carries quick replies THEN the web app SHALL show up to 4 buttons
   under the message text, one per label, each at least 44 px high and wrapping inside the bubble at
   320 px with a 125% root font, and a tap SHALL post the answer as a reply to that message at once.
   (QR-10)
2. WHILE the answer request is in flight the buttons SHALL not act and SHALL show a pending state;
   WHEN it succeeds THEN the chosen button SHALL be marked as pressed and the buttons SHALL stop
   acting, with a status message for assistive technology; IF it fails THEN the buttons SHALL act
   again and a toast SHALL explain, and a 409 `already_answered` SHALL refresh the timeline so the
   stored answer shows. (QR-11)
3. WHEN the timeline is loaded or reloaded THEN a message with quick replies SHALL show the viewer's
   own chosen label when the viewer posted an answer to it (a reply with `answers.quick_reply`), and
   SHALL NOT treat an ordinary text reply as an answer. (QR-12)
4. WHEN the viewer is the question's author, or may not post in the conversation, or the conversation
   is frozen as history THEN the web app SHALL show the quick replies without acting, and SHALL say in
   words why (your question, no permission to post, this conversation is history). (QR-13)

**Independent Test**: In the web app, Bob opens the room, taps "Sim", and the timeline shows his reply
"Sim" and the button marked as chosen; a reload keeps it; the question's author sees the labels with
"Your question" and no button acts; a room the viewer can only read shows the same.

---

### P1: Answering a wizard ⭐ MVP

**User Story**: As a member, I want to answer a short questionnaire inside the bubble, one question at
a time, so that I can review my answers before they are sent.

**Acceptance Criteria**:

1. WHEN a message carries a wizard THEN the web app SHALL show one question at a time inside the
   bubble, with the step ("Step 1 of 3"), Back and Next, and a Send on the last step; a `single`
   question SHALL be a group of radio inputs, a `multi` question a group of checkboxes, and a question
   with `allow_text` SHALL offer a text field of at most 280 characters. (QR-14)
2. The web app SHALL keep Next and Send disabled until the step is valid — a `single` question with
   one option or, when it allows text and no option is chosen, a non-empty text; a `multi` question
   with at least one option — and SHALL keep every control inside the bubble at 320 px and 412 px with
   a 125% root font, each target at least 44 px. (QR-15)
3. WHEN the viewer moves to another step THEN the web app SHALL move focus to the new question's
   heading; WHEN Send succeeds THEN the bubble SHALL become read-only, SHALL show the chosen values as
   the answers it sent and SHALL move focus to that summary, and IF Send fails THEN the editable steps
   SHALL return with a toast. (QR-16)
4. The web app SHALL expose the wizard with native inputs and grouped names (a labelled radio group
   and a labelled checkbox group), SHALL announce the step and the result, and SHALL have every string
   in Portuguese (pt-BR) and English, so a keyboard and a screen reader can complete it without a
   mouse. (QR-17)
5. WHEN someone else answers a question the viewer is looking at THEN the answer SHALL arrive through
   the live socket as an ordinary message and SHALL appear in the timeline without a reload, while
   the viewer's own bubble SHALL keep its own state. (QR-18)

**Independent Test**: A wizard with a single-choice, a multiple-choice and a text question is answered
with the keyboard: Next is disabled on each empty step, a step change focuses the next question, Send
posts one reply whose text lists all answers, and the bubble then shows the summary; another member's
answer shows up live as a message.

---

### P1: The AI asks and reads the answer ⭐ MVP

**User Story**: As an AI, I want to ask a multiple-choice question and receive the choice in a form I
can act on, so that I do not have to parse a sentence.

**Acceptance Criteria**:

1. WHEN an AI calls the `ask` tool with `room`, `text` and `quick_replies` or `wizard` THEN the tool
   SHALL post the question through the agent room route and SHALL answer `sent (message_id …)`, and a
   refusal by the server SHALL come back as a tool error naming the field. (QR-19)
2. WHEN an AI calls `ask` with `connection` and `chat` THEN the tool SHALL send the text with the
   options appended as numbered plain-text lines and SHALL neither store a question nor post a
   message in a room, and IF the request names a wizard ELSE SHALL refuse with a tool error; `ask`
   SHALL accept exactly one of `room` or `connection` with `chat` and no `to`. (QR-20)
3. WHEN an AI receives a message that carries a question or an answer THEN its channel frame SHALL
   carry the question as an `interactive` meta attribute (the JSON of the stored document) and the
   answer as an `answers` meta attribute (the JSON of the stored answer), with the human-readable
   text as the content and the question's id in `reply_to`. (QR-21)
4. The system SHALL deliver an answer to the AI that asked the question without changing the delivery
   rules: an answer replies to the question's message, and `delivery.Wants` already delivers a reply
   to the AI's own message under the mentions-only default. (QR-22)

**Independent Test**: An AI asks "Deploy hoje?" with two buttons in a DM; the person taps "Sim"; the
AI's next frame carries `content="Sim"`, `reply_to` the question and
`answers={"v":1,"quick_reply":"Sim"}`. On a Telegram connection, the same call sends
"Deploy hoje?\n\n1. Sim\n2. Não" and the answer arrives as a normal chat message.

---

### P2: Documentation and the phone check

**User Story**: As an operator, I want the new message fields and the new tool written down and the
bubble checked on a phone, so that I can run the feature and trust its size.

**Acceptance Criteria**:

1. The repository SHALL document the two message fields, their limits, the answer body and its error
   codes in `docs/api.md`, the `ask` tool and the two frame attributes in `docs/agents.md`, and the
   decision in `.specs/STATE.md`. (QR-23)
2. The fit end-to-end suite SHALL seed a question with four long labels and a wizard with long labels
   and SHALL check the conversation at 320×700 and 412×900 with a 125% root font, so neither the
   quick replies nor the wizard scrolls sideways or spills out of the bubble. (QR-24)
3. The end-to-end suite SHALL drive a quick reply and a wizard in the real web app, SHALL see the
   answer as one reply with the expected text, and SHALL see the chosen state after a reload. (QR-25)

**Independent Test**: `make e2e` passes for both phone projects with the seeded questions;
`docs/api.md` lists `quick_replies`, `wizard` and `answers` with their limits and 422/403/409.

---

## Edge Cases

- WHEN the question message is deleted, its answers stay as replies to a deleted message and the
  bubble disappears; a new answer is 409 `conflict` (QR-04, QR-09).
- WHEN a wizard's question allows text and the viewer types without choosing an option, a `single`
  question accepts the text alone and a `multi` question still requires one option (QR-07).
- WHEN a member deletes their own answer, the bubble returns to its answerable state and the same
  member may answer again, because the partial unique index no longer sees the cleared row (QR-08).
- WHEN two answers from one member race, the second insert fails the unique index and the API answers
  409 `already_answered`, never 500 (QR-05, QR-08).
- WHEN the author edits a question after somebody answered, the labels and ids stay and the answer
  keeps matching (QR-04).
- WHEN the web app cannot reach the server, the buttons return and a toast explains; no answer is
  held locally (QR-11).
- WHEN the viewer answers on another device and comes back, the chosen state comes from the timeline
  the page loads (QR-12).
- WHEN a wizard has 8 questions with 8 options each and 125% text, the bubble wraps and grows in
  height, never in width (QR-15).
- WHEN an AI asks on a connection, nothing is stored in Partyline and the platform reply is handled by
  the existing inbound pipeline as an ordinary message (QR-20).
- WHEN an AI answers a question with a plain `reply` (it has no tool for a structured answer in v1),
  the reply is in the thread but the bubble stays unanswered: there is no structured answer to show or
  to close it, and the asker reads the text (QR-21).

---

## Implicit-requirement sweep

| Dimension | Covered by |
| --- | --- |
| Input validation & bounds | QR-03 (all limits, field names), QR-07 (answer shape, 280 characters), QR-20 (a wizard is refused on a connection) |
| Failure / partial-failure states | QR-05, QR-09 (refused answer); QR-11, QR-16 (a failed submit returns the controls); no partial wizard is ever stored |
| Idempotency / duplicates | QR-05, QR-08 (one answer per author, 409, unique index); a second tap while pending does nothing (QR-11) |
| Auth boundaries & permissions | QR-05 (the asker cannot answer), QR-13 (a viewer who may not post sees no action), QR-09 (history-only DMs), the existing 30-posts-a-minute agent limit covers `ask` (QR-19) |
| Concurrency / ordering | QR-08 (partial unique index turns a race into 409); QR-18 (live `room.message` for everyone); the answer is a reply, so the timeline order does not change |
| Data lifecycle | QR-04 (delete clears both parts), QR-08 (deleting an answer frees the slot), migration `0026` adds columns only (AD-012) |
| Observability | No log gains message text, labels or answers; the existing notification and delivery logs are unchanged (AGENTS.md privacy rule) |
| External-dependency failure | The platform path is the existing send queue with its retries (QR-20); a platform reply is an ordinary inbound message |
| State-transition integrity | unanswered → pending → answered → deleted → answerable; the server is the authority and the web derives the state from the timeline (QR-08, QR-12) |
| Accessibility & internationalisation | QR-14, QR-15, QR-16, QR-17 (groups, focus, touch targets, both locales), QR-24 (the phone fit) |
| Documentation | QR-23 |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| QR-01 | P1: A message carries a question | Execute | Pending |
| QR-02 | P1: A message carries a question | Execute | Pending |
| QR-03 | P1: A message carries a question | Execute | Pending |
| QR-04 | P1: A message carries a question | Execute | Pending |
| QR-05 | P1: A message carries a question | Execute | Pending |
| QR-06 | P1: A message carries a question | Execute | Pending |
| QR-07 | P1: A message carries a question | Execute | Pending |
| QR-08 | P1: A message carries a question | Execute | Pending |
| QR-09 | P1: A message carries a question | Execute | Pending |
| QR-10 | P1: Answering with one tap | Execute | Pending |
| QR-11 | P1: Answering with one tap | Execute | Pending |
| QR-12 | P1: Answering with one tap | Execute | Pending |
| QR-13 | P1: Answering with one tap | Execute | Pending |
| QR-14 | P1: Answering a wizard | Execute | Pending |
| QR-15 | P1: Answering a wizard | Execute | Pending |
| QR-16 | P1: Answering a wizard | Execute | Pending |
| QR-17 | P1: Answering a wizard | Execute | Pending |
| QR-18 | P1: Answering a wizard | Execute | Pending |
| QR-19 | P1: The AI asks and reads the answer | Execute | Pending |
| QR-20 | P1: The AI asks and reads the answer | Execute | Pending |
| QR-21 | P1: The AI asks and reads the answer | Execute | Pending |
| QR-22 | P1: The AI asks and reads the answer | Execute | Pending |
| QR-23 | P2: Documentation and the phone check | Execute | Pending |
| QR-24 | P2: Documentation and the phone check | Execute | Pending |
| QR-25 | P2: Documentation and the phone check | Execute | Pending |

**Coverage:** 25 total, 25 mapped to tasks (`tasks.md` T1–T10), status Pending (the plan is not
executed yet).

---

## Success Criteria

- [ ] An AI asks "Deploy hoje?" with three buttons; a person taps "Sim" once and the conversation
      shows "Sim" as a reply; the AI's frame carries the structured answer.
- [ ] A wizard of three questions is completed with the keyboard on a 320 px phone at 125% text, one
      reply is posted with three "Question: answer" lines, and the bubble then shows the summary.
- [ ] The same server refuses a fifth button, a ninth question, a 49-character label and a second
      answer from one member, each with the documented status and code.
- [ ] An answer to an AI's question reaches the AI under the default mentions-only setting.
- [ ] On a Telegram connection, the question arrives as numbered plain text and the person's reply
      arrives as an ordinary message.
- [ ] After a reload, the viewer's chosen quick reply and the submitted wizard are read-only and show
      the values that were sent.
- [ ] `make e2e` covers the flow and the 320/412 px fit, and every new string exists in pt-BR and en.

---

## Open questions for the product owner

1. **One answer per member.** A question accepts one structured answer from each member; a second
   attempt answers 409 `already_answered`, and changing an answer means deleting it and answering
   again. In a group, everyone may answer once (the bubble shows the viewer's own choice, and every
   answer is a reply in the thread). Confirm, or say that only the first answer in a group should
   count (and everyone else should see the question as closed).
2. **The platform reply is text.** On a WhatsApp, Telegram or Discord connection the options go out as
   `1. …` lines and the person's answer arrives as an ordinary message; Partyline does not turn "1"
   into a structured answer. That keeps the platform path to text, and the AI that asked reads the
   number itself. Confirm, or say that "1" must be mapped back to the option automatically.
