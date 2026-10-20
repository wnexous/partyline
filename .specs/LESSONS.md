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
- evidence: internal/app/app_test.go:134 (tests) (+1 more)
- last seen: 2026-10-20T04:47:07Z

### L-006 - Split ACs whose enforcement lives in a later feature so each feature's ACs are verifiable end to end
- signal: `spec_precision_gap` · recurrence: 1 feature(s) · scope: `specs` · harmful: 0
- features: core-platform
- evidence: DLV-02 DLV-06 DLV-08 SEC-04 AUD-01 (specs)
- last seen: 2026-10-20T02:32:09Z

### L-007 - A fix must not add behavior the AC does not allow; reconcile any extra rule with the spec before coding it
- signal: `ac_gap` · recurrence: 1 feature(s) · scope: `auth` · harmful: 0
- features: core-platform
- evidence: AUTH-05 internal/users/users.go:184-188 (probe P2) (auth)
- last seen: 2026-10-20T04:47:07Z

### L-008 - Concurrency limit tests must count the guarded expensive operation, not only the responses
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `auth` · harmful: 0
- features: core-platform
- evidence: N3 internal/users/auth.go:95-98 (auth)
- last seen: 2026-10-20T04:47:07Z

### L-009 - When tests override a configurable default, also assert the production default the spec names
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: core-platform
- evidence: N6 internal/live/hub.go:38; N16 internal/users/users.go:160 (tests)
- last seen: 2026-10-20T04:47:07Z

### L-010 - Test time-window boundaries with values strictly inside and outside the window, never at age zero
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: core-platform
- evidence: N9 internal/rooms/retention_test.go:115 (tests)
- last seen: 2026-10-20T04:47:07Z

### L-011 - Every new sentinel error mapped to an HTTP status needs a test that observes that status
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `api` · harmful: 0
- features: core-platform
- evidence: N12 internal/api/router.go:160 (api)
- last seen: 2026-10-20T04:47:07Z

### L-012 - Negative tests must isolate the check under test so no other guard rejects the input first
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: core-platform
- evidence: N17 internal/users/tokens_test.go:235 (tests)
- last seen: 2026-10-20T04:47:07Z

### L-013 - Every behavior written into contract notes needs a test or it is unverified
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: core-platform
- evidence: N18 internal/live/hub.go:244-245 (tests)
- last seen: 2026-10-20T04:47:07Z

## Quarantined (failed when applied - ignore)

A confirmed lesson that recurred alongside failure. Kept for the maintainer to review.

_none_
