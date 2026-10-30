# Tasks: session notifications

## T1 — data + session keys (SN-01, SN-02)
Migration 0031 `session_room_state`, sqlc queries (get/upsert read_seq, get/set notify, list per
session, unread counts per room for a session), session_key resolution helpers in internal/agents,
join-point initialisation, principal read_seq mirroring agent_cursors. Tests. Status: done

## T2 — routing + catch-up (SN-03, SN-04, SN-06, SN-07, SN-09)
Gateway Push targets per the design, catch-up batches with `context="true"` frames (channel
Format/Frame attribute), acks moving read_seq, edits/deletes following delivery rows, SESS-38/49
kept, platform events principal-only. Server tests for every rule incl. the incident: owner
mentions `@atlas:pi-2` in a group → pi-2 gets the unread context + the message, principal also gets
it; plain group message → principal only, pi-2 unread grows; pi-2 with notify on → gets it.
Review fixes (SN-03a, SN-09a): membership clamp and removal cleanup, acked-copy fallback, replay of
unacked context, own-message updates for context holders, trusted/loop-guard context filters,
offline principal backlog, session-key routing, principal row and queue hygiene. Status: done

## T3 — tools (SN-05, SN-02)
`list_rooms`/`room_info` unread + notify per calling session; `read_unread`; `set_notifications`;
docs/agents.md; channel tests. Status: done

## T4 — API + web (SN-08)
Per-session notification routes (manage level), web Sessions "Notificações" sheet + member card
muted marker, i18n, vitest + e2e. Status: done
