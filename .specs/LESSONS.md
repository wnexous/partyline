# LESSONS - auto-maintained by scripts/lessons.py

> Machine-owned. Do NOT hand-edit. Changes are overwritten on the next `lessons.py` write.
> Canonical state lives in `.specs/lessons.json`. Edit lessons only via the script.
> promote_threshold=2 distinct features · window_days=45 · quarantine_threshold=2

## Confirmed (load these at Specify/Design)

Corroborated across multiple features. Safe to apply as guidance.

_none_

## Candidates (under observation - do NOT load as guidance yet)

Seen once or not yet corroborated. Tracked, not trusted.

### L-001 - When a membership or grant is removed, also cancel the queued work it authorized, not only future planning
- signal: `ac_gap` · recurrence: 1 feature(s) · scope: `delivery` · harmful: 0
- features: core-platform
- evidence: ROOM-01 internal/rooms/members.go:166 (delivery)
- last seen: 2026-10-20T02:32:09Z

### L-002 - Rate limiters must reserve an attempt atomically before slow work and be tested with a concurrent burst
- signal: `ac_gap` · recurrence: 1 feature(s) · scope: `auth` · harmful: 0
- features: core-platform
- evidence: AUTH-05 internal/users/auth.go:95 (auth)
- last seen: 2026-10-20T02:32:09Z

### L-003 - Test origin checks with a same-host different-scheme origin, not only a foreign host
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `live` · harmful: 0
- features: core-platform
- evidence: M17 internal/live/hub.go:105 (live)
- last seen: 2026-10-20T02:32:09Z

### L-004 - Every spec edge case must name an operation that exists in scope or move to Out of Scope
- signal: `spec_precision_gap` · recurrence: 1 feature(s) · scope: `specs` · harmful: 0
- features: core-platform
- evidence: edge case last-manager ROOM-02 (specs)
- last seen: 2026-10-20T02:32:09Z

### L-005 - Do not assert background-job effects with a fixed short wait; trigger the job deterministically in tests
- signal: `gate_fail` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: core-platform
- evidence: internal/app/app_test.go:134 (tests)
- last seen: 2026-10-20T02:32:09Z

### L-006 - Split ACs whose enforcement lives in a later feature so each feature's ACs are verifiable end to end
- signal: `spec_precision_gap` · recurrence: 1 feature(s) · scope: `specs` · harmful: 0
- features: core-platform
- evidence: DLV-02 DLV-06 DLV-08 SEC-04 AUD-01 (specs)
- last seen: 2026-10-20T02:32:09Z

## Quarantined (failed when applied - ignore)

A confirmed lesson that recurred alongside failure. Kept for the maintainer to review.

_none_
