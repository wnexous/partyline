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
4. A message that mentions a parallel session (`@handle:label`) SHALL also reach the principal
   when the principal's setting for that conversation is `on` (it no longer goes to the labelled
   session only). (SN-04)
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
   working; edits/deletes reach every session that had the original message pushed. (SN-09)

## Out of scope
Per-session notification for platform chats; notification sounds; per-message read receipts in the
web app.
