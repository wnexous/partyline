# Secret Requests Specification

## Problem Statement

The product owner, 2026-11-13: an AI needed an API key and the only safe way it could offer was a
shell command to paste into a terminal of its machine, because a key pasted in a room stays in the
history. He asked for a small window that opens when the model asks for a key: paste it there and it
goes straight to a file the AI can use, never into the conversation or the AI's session.

So an AI can post a **secret request**: a message that names the secret (`EXAMPLE_API_KEY`) and says
why it needs it. The web app shows it as a card with a password field. A person who manages the AI
pastes the value and presses Save; the value travels once to the session that asked, which writes it
to a private file on its machine, and the conversation only records that it was saved, by whom and
when. The AI learns the file's path, never the value through a message.

## Goals

- [ ] A key reaches the AI's machine without being stored in any message, log, event or database.
- [ ] Asking for a key is one tool call that returns at once, like `ask`.
- [ ] Only a person the AI already trusts (an identity that manages it) can give it a secret.
- [ ] Works on a 360 px phone with a 125% font, and with password managers.

## Out of Scope

| Feature | Reason |
| --- | --- |
| End-to-end encryption between browser and AI | The value crosses the server in memory over TLS, as every message does; documented. A later version may encrypt to a key the session publishes. |
| Secret requests through WhatsApp/Telegram/Discord connections | A chat cannot render a password field; a value typed there would stay in the platform's history. The tool refuses a connection target. |
| Storing secrets on the server for later | The server keeps nothing; a session that needs the key again asks again. |
| Rotating or deleting a saved file from the web | The file is on the AI's machine; the AI deletes it. |

## Requirements

### SR-P1: Ask for a secret

1. The channel SHALL offer a tool `request_secret` with `room` (a room or DM the AI belongs to),
   `name` (`^[A-Z][A-Z0-9_]{0,63}$`) and `text` (why, 1 to 500 characters). It SHALL post a message
   whose text is `text` and whose interactive part is `{"v":1,"secret":{"name":NAME}}`, and SHALL
   return at once with the message id and the path the value will be written to. (SR-01)
2. The tool SHALL refuse a connection target and a name outside the pattern with a message that says
   why. (SR-02)
3. The server SHALL accept a secret request only from an AI author (a person has nothing to receive
   it with) and SHALL record which live session posted it. (SR-03)

### SR-P2: Give the secret

1. WHEN a user opens a conversation with a secret request THEN the web app SHALL show a card with
   the name, the reason, and, for a user who manages the asking AI, a password field (masked, with a
   show toggle, `autocomplete="off"`) and a Save button; any other member SHALL see the card without
   the field and the line "Waiting for someone who manages @handle". (SR-04)
2. `POST /rooms/{id}/messages/{mid}/secret` with `{"value":…}` (1 to 16384 bytes, any characters,
   newlines included) SHALL be accepted only from a user who manages the asking AI (403 otherwise),
   only for a secret request (404/422 otherwise), and only once (409 `already_answered`). (SR-05)
3. The server SHALL hand the value to the live session that posted the request, in one `secret`
   frame, and wait up to 10 seconds for its acknowledgement. On success it SHALL create the answer —
   a reply to the request with the text "🔑 NAME saved" and the structured answer
   `{"v":1,"secret":{"name":NAME,"saved":true}}` — and answer 201 with it. WHEN that session is not
   live THEN 409 `ai_offline`; WHEN no acknowledgement arrives in time, or the session reports a
   failure, THEN 504 `not_saved` with the session's reason, and nothing is recorded. (SR-06)
4. The value SHALL NOT be written to the database, a log line, an audit entry, an event, a live
   frame to the web app, a push notification or an error message; request bodies of this endpoint
   SHALL be excluded from any request logging. (SR-07)

### SR-P3: Receive the secret

1. The session (the channel, or `partyline listen` for pi and OpenCode) SHALL write the value to
   `<user config dir>/partyline/secrets/<ai id>/<NAME>` with mode 0600 in directories of mode 0700,
   replacing an existing file atomically, and SHALL acknowledge with the path, or with the error.
   (SR-08)
2. WHEN the answer reaches the AI THEN its frame SHALL say that NAME was saved to that path by whom,
   and SHALL say to read the file when needed and never print it or paste it in a room. (SR-09)
3. A client too old to know the `secret` frame SHALL make the server answer 409 `client_outdated`
   before the value is sent (the server knows each session's client version). (SR-10)

### SR-P4: Guidance and docs

1. The channel instructions SHALL tell AIs to use `request_secret` instead of asking for a secret in
   a message, and the paste rule SHALL stay. (SR-11)
2. `docs/api.md` SHALL document the endpoint and errors, `docs/agents.md` the tool, the frame and
   the file location, README the feature and its trust boundary (the value crosses the server in
   memory). (SR-12)
3. The fit end-to-end suite SHALL check the card at 320×700 and 412×900 with a 125% font; an
   end-to-end test SHALL save a secret through the web app into a real session's file and SHALL
   check that the value appears in no API response, no live frame and no server log line. (SR-13)
