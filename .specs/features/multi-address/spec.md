# Several session addresses in one message

Status: Specify + Design written 2026-11-05 (heartbeat, not built). Requirement ids MA-xx. Not
asked for yet: built when someone needs to ask two parallel sessions of one AI at once.

## Problem
A message addresses one session of an AI with `@handle:label` (SESS-21). When its text names two
sessions of the same AI, `@atlas:api and @atlas:web, please compare`, only the first one is
addressed: `delivery.SessionLabel` returns the first valid address and the dispatcher pushes to
that label. The second session only gets the message if it follows the room (SN-03), and not as
the session the message is for. The writer has to send the same message twice.

## What is already in place
- Since migration 0032, `agent_session_deliveries` is keyed by `(message_id, agent_id, label)`:
  one message can already hold one delivery row per label. **No migration is needed.**
- `dispatchUpdate` already passes a list of targets to `Gateway.PushUpdate`, deduplicated.
- `selfTargets` already loops over the old and new text and dedupes labels with `seen`.

## Requirements
1. WHEN a message's text addresses several labels of the receiving AI with `@handle:label` THEN
   the system SHALL treat every distinct valid address outside code as addressed, in the order
   they appear, with the same parsing rules as SESS-21 and SESS-48; an address repeated in the
   text SHALL count once. (MA-01, amends SESS-21's "the first such address wins")
2. WHEN several addressed labels have a live session THEN each of those sessions SHALL receive the
   message once as addressed (its own delivery row, its own `for_label`-free frame), with the
   context it had not read (SN-01). (MA-02)
3. WHEN one or more addressed labels have no live session THEN the principal SHALL receive the
   message once, not once per missing label, with the note naming the first missing label
   (`for_label`); every missing label SHALL still get its own fallback row so an
   acknowledgement clears them all. (MA-03, extends SESS-22)
4. WHEN the principal receives a message because a sibling is addressed (SN-04) and some other
   addressed label fell back to it THEN it SHALL receive one frame, the fallback one (with the
   note). (MA-04)
5. WHEN a labelled session that was addressed ends before acknowledging THEN its row SHALL fall
   back to the principal as today (SESS-24), and the principal SHALL NOT receive the message a
   second time when it already received it through another addressed label's fallback or as a
   copy of a sibling. (MA-05)
6. WHEN a session of an AI writes a message addressing several live siblings of the same AI THEN
   each sibling SHALL receive it (SESS-37); the writer's own label SHALL be skipped; a label with
   no live session SHALL NOT fall back to the principal (SESS-38). (MA-06)
7. WHEN a message with several addresses is edited or deleted THEN the update SHALL reach every
   session addressed before or after the change, once each (SESS-39, SN-09). (MA-07)
8. WHEN the text addresses labels of several different AIs THEN each AI SHALL be handled on its
   own, as today (each AI reads only the addresses with its own handle). (MA-08)
9. The number of addressed labels per AI per message SHALL be capped at 10; further addresses
   SHALL be read as plain text (no routing). (MA-09)

## Design
- `delivery.SessionLabels(text, handle) []string`: the same scan as `SessionLabel`, returning every
  distinct valid label in order, capped at 10. `SessionLabel` becomes `first(SessionLabels(...))`
  so the parsing tests keep covering both.
- `Gateway.Push(agentID, targets []string, m)`; `planPush` takes the list:
  - `addressed[label]` replaces `target == it.live.Label`;
  - `plan.notes` = addressed labels with no live session; the principal is routed once, through the
    first note, and every other note gets a fallback row with `fell_back_at` set and the same
    `owner_live` as the first, so the principal's ack/backlog sees one message;
  - `copyOfSibling` holds when any addressed label is live.
- Principal dedupe: the backlog and the pending list must return the message once even when it
  holds two fallback rows. Group by `message_id` in the fallback query (sqlc), and the queue loop
  already drops a principal frame with `Seq <= sent[room]`.
- Ack: acknowledging a message as the principal marks every fallback row of that message and AI.
- `dispatch` and `selfTargets` call `SessionLabels`; `dispatchUpdate` appends every label of the old
  and new text before `dedupeTargets`.
- The channel and plugins need no change: frames are per session, and `for_label` stays one label.

## Risks (review focus)
- A double copy to the principal through two fallback rows (backlog, pending, reconnect sweep).
- An ack that clears one fallback row and leaves the other pending forever.
- The interplay with the attach window fixed in v0.14.1/v0.14.2 (`c.rooms`, backlog bounds).
This touches the delivery core: it needs an adversarial review before merge, as SN did.

## Tests to write first
- Parser: two addresses, a repeat, one in code, an invalid one, eleven addresses.
- Two live siblings addressed: each gets one addressed frame; the principal (notify on) gets one copy.
- One live + one missing: the live one gets it; the principal gets one frame with the note.
- Two missing: the principal gets one frame; one ack clears both rows; a reconnect brings nothing.
- An addressed sibling ends before ack while another label already fell back: the principal still
  gets it once.
- Self-authored: a session addressing two siblings and itself.
- Edit that adds a second address; delete of a message with two addresses.

## Out of scope
- Addressing several AIs' principals in one message: already works (plain mentions).
- Changing `for_label` into a list.
