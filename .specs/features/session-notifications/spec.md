# Session notifications: every session is in the conversation, only some get notified

Status: Specify + Design + Tasks written 2026-10-30. Requirement ids SN-xx. Owner request
(voice note, 2026-10-29): "like a WhatsApp group: everyone is in it; it rings for whoever has
notifications on, or is mentioned, and they then read everything they had not read".

## Problem
Today a message goes to the principal session only; a message addressed with `@handle:label` goes
to that labelled session only (the principal does not see it), and a labelled session never sees
the rest of the conversation. So a parallel session that is asked something lacks the context,
and the principal misses what was said to its siblings.

## Requirements
1. Every live session of an AI SHALL have, per conversation, a read position ("read up to") and
   an unread count; read positions survive reconnects and server restarts for sessions that send
   a harness session id (keyed by it), else by label while the label lives. (SN-01)
2. Every session SHALL have, per conversation, a notification setting `on | mentions`. Defaults:
   the principal `on` (today's behaviour, still subject to the AI's room delivery mode
   all/mentions/trusted), a parallel session `mentions`. A session can change its own setting
   per conversation with a tool; the owner can change it in the web app. (SN-02)
3. A message that a session is notified of (setting `on` and wanted by the delivery mode, or it
   mentions the session — `@handle` for the principal, `@handle:label` for that session) SHALL be
   pushed to that session TOGETHER WITH every message of that conversation it had not read yet
   (oldest first, capped at 50, with an "N earlier messages skipped" note beyond), and the
   session's read position moves to the newest pushed message when it acknowledges. (SN-03)
   Context is filtered by the delivery mode: in `trusted` mode an untrusted message is not context
   (and `mentions` keeps the whole conversation, which is the point of SN-03); agent messages
   written while the loop guard paused the room (DLV-08) are never context; only an acknowledged
   message leaves the batch, so a batch lost with its connection comes back; and a batch larger
   than the free queue keeps the newest frames that fit and counts the rest as skipped. (SN-03a)
4. A message that mentions a parallel session (`@handle:label`) SHALL also reach the principal
   when the principal's setting for that conversation is `on` (it no longer goes to the labelled
   session only). A bare `@handle` in the same text names the principal too. The principal's
   acknowledgement of its copy consumes the addressed row, and its backlog carries the sibling's
   message while that row is unacknowledged and the principal has no acknowledged copy. (SN-04)
5. A session that is not notified SHALL NOT receive the message live; it only counts as unread.
   `list_rooms`/`room_info` SHALL show the session's unread count per conversation, and a new
   tool `read_unread {room}` SHALL return the unread messages of that conversation (same framing
   as pushed messages) and mark them read. (SN-05)
6. Messages written by the AI's own sessions SHALL appear in the other sessions' unread/context
   (framed with `author_session` / `from_own_session`), but SHALL NOT notify a sibling unless they
   mention it explicitly (SESS-38/SESS-49 stay), so sessions do not loop. (SN-06)
7. Platform events (WhatsApp, Telegram…) keep going to the principal only. (SN-07)
8. The web app SHALL show, in the AI's profile Sessions list, per session its notification setting
   per conversation (at least for the conversation being viewed in the member card) and let the
   owner switch it; the member card's session line marks muted sessions. (SN-08)
9. Existing behaviour that is not about sessions (delivery modes, loop guard, trust, edits and
   deletes following the addressed session, backlog on connect for the principal) SHALL keep
   working; edits/deletes reach every session that had the original message pushed. A delivery row
   stores the session key, and updates follow it rather than the label, so a label a later session
   takes over receives no update of a message it never saw; a mentions-only principal follows the
   updates of the messages it has a row for; an own message's update also follows its context
   holders. (SN-09a)

## Amendments

### SN-10 — no live principal: the parallel sessions stand in for it (2026-11-10)
Incident: an AI's only live session was a parallel one (its principal was offline) and a new person
wrote three messages in a DM with the AI. A parallel session in a new conversation reads it on
`mentions`, and a DM notified only the principal, so none of the messages reached the live session:
they waited for a principal that never connected. The same held for a bare `@handle` in a group.

10. WHILE an AI has no live principal session, a message that would notify the principal — a DM
    addressed to the AI, or a message that mentions the AI by its bare `@handle` outside code (the
    rule of SESS-49), in both cases addressing no `@handle:label` — SHALL notify every live parallel
    session of the AI as if its setting were `on` for that message, with the context of SN-03 and a
    copy delivery row per session like any other sibling notification. (SN-10)
    - WHEN a principal is live nothing changes: it is notified, and its siblings follow their own
      setting (SN-02, SN-03, SN-04).
    - A message addressed to a label keeps its own route (SESS-21..SESS-24): a live label owns it, a
      label with no live session is the principal's with the note and waits for it. A message the AI
      wrote itself keeps SESS-37/SESS-38/SESS-49.
    - WHEN a parallel session acknowledges a message it received this way THEN the message SHALL be
      consumed for the principal: the session's row becomes its own tombstone (as for an addressed
      message it acknowledged), so the principal's backlog leaves it out when it connects later and
      no session receives it twice. The principal still counts it as unread in that conversation
      and receives it as context of its next notification there when its read position is below it.
      A message no parallel session acknowledged stays in the principal's queue (nothing is lost
      when the session that was given it ends without answering).
    - WHEN a parallel session attaches while the AI has no live principal THEN its backlog SHALL
      also carry, per conversation, the messages of the principal's queue the first rule names and
      the delivery mode wants (the ones that session already acknowledged, or that its own backlog
      just wrote, left out), as the messages themselves and with the unread conversation before
      them as context, filtered as SN-03a says; they are consumed for the principal on
      acknowledgement the same way. Nothing is given from a room the loop guard holds (DLV-08).
      The cap counts those messages, not the principal's whole queue: the oldest 50 of them are
      given, and WHEN more wait THEN a notice after them SHALL say how many, and those stay in the
      principal's queue (the session reads them with the history tool).
    - A session that is already live when the principal drops SHALL NOT pick up the principal's
      unacknowledged queue: it receives the qualifying messages that arrive from then on, and the
      waiting ones only through the attach-time backlog of a later connection.
    - A stand-in a session did not acknowledge before its connection ended comes back to its next
      connection (same label) through that backlog, and an acknowledgement there consumes it.
    - Accepted race: a principal that connects while a parallel session holds such a message
      unacknowledged also receives it in its backlog, so the message is delivered at most once more
      than the stand-ins, and both may answer it.
    - SESS-23 is amended accordingly: while the principal is offline its queue is still kept for it,
      but the messages this rule names are also delivered to the live parallel sessions.

## Out of scope
Per-session notification for platform chats; notification sounds; per-message read receipts in the
web app.
