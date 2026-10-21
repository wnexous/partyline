# LESSONS - auto-maintained by scripts/lessons.py

> Machine-owned. Do NOT hand-edit. Changes are overwritten on the next `lessons.py` write.
> Canonical state lives in `.specs/lessons.json`. Edit lessons only via the script.
> promote_threshold=2 distinct features · window_days=45 · quarantine_threshold=2

## Confirmed (load these at Specify/Design)

Corroborated across multiple features. Safe to apply as guidance.

### L-017 - Every new sentinel error mapped to an HTTP status needs a test that observes that status
- signal: `surviving_mutant` · recurrence: 2 feature(s) · scope: `api` · harmful: 0
- features: agent-channel, core-platform
- evidence: N12 internal/api/router.go:160 (api) (+1 more)
- last seen: 2026-10-20T07:38:15Z

### L-018 - Negative tests must isolate the check under test so no other guard rejects the input first
- signal: `surviving_mutant` · recurrence: 2 feature(s) · scope: `tests` · harmful: 0
- features: agent-channel, core-platform
- evidence: N17 internal/users/tokens_test.go:235 (tests) (+1 more)
- last seen: 2026-10-20T07:38:14Z

### L-019 - Every behavior written into contract notes needs a test or it is unverified
- signal: `surviving_mutant` · recurrence: 2 feature(s) · scope: `tests` · harmful: 0
- features: core-platform, routing-context
- evidence: N18 internal/live/hub.go:244-245 (tests) (+1 more)
- last seen: 2026-10-20T15:04:35Z

### L-029 - Run the full make check (lint included) before committing even a test-only or orchestrator edit
- signal: `gate_fail` · recurrence: 2 feature(s) · scope: `process` · harmful: 0
- features: agent-channel, connectors
- evidence: internal/live/hub.go:101 (f4147c9), internal/channel/link.go:348 (c9083be) (process) (+1 more)
- last seen: 2026-10-20T10:11:20Z

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

### L-007 - Test permission-gated UI through the page that passes the permission, not only the leaf component
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `web-pages` · harmful: 0
- features: web-app
- evidence: M18 web/src/routes/_app/rooms/$roomId.tsx:120 (web-pages)
- last seen: 2026-10-20T02:50:50Z

### L-008 - Stub matchMedia and assert the resolved result for every behavior that follows a system preference
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `web-lib` · harmful: 0
- features: web-app
- evidence: M17 web/src/lib/theme.ts:36 (UI-23) (web-lib)
- last seen: 2026-10-20T02:50:50Z

### L-009 - Assert layout ACs such as no horizontal scroll with a measurable check in the e2e viewport project, not only by running flows at that width
- signal: `ac_gap` · recurrence: 1 feature(s) · scope: `e2e` · harmful: 0
- features: web-app
- evidence: UI-24 no horizontal-scroll assertion in web/e2e/smoke.spec.ts (e2e)
- last seen: 2026-10-20T02:50:51Z

### L-010 - Test that retry backoff resets after a successful connection, not only that it grows
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `web-lib` · harmful: 0
- features: web-app
- evidence: M19 web/src/lib/live/socket.ts:61 (web-lib)
- last seen: 2026-10-20T02:50:53Z

### L-011 - Assert the rendered value of time and copy outcomes, not only a label prefix or the presence of a control
- signal: `spec_precision_gap` · recurrence: 1 feature(s) · scope: `web-pages` · harmful: 0
- features: web-app
- evidence: UI-15 agents.test.tsx:64; UI-19 tokens.test.tsx:89-98 (web-pages)
- last seen: 2026-10-20T02:50:54Z

### L-012 - Name the exact artifact that must contain each value when an AC lists several outputs
- signal: `spec_precision_gap` · recurrence: 1 feature(s) · scope: `spec` · harmful: 0
- features: web-app
- evidence: UI-14 launch command has no instance URL (spec)
- last seen: 2026-10-20T02:50:56Z

### L-013 - A fix must not add behavior the AC does not allow; reconcile any extra rule with the spec before coding it
- signal: `ac_gap` · recurrence: 1 feature(s) · scope: `auth` · harmful: 0
- features: core-platform
- evidence: AUTH-05 internal/users/users.go:184-188 (probe P2) (auth)
- last seen: 2026-10-20T04:47:07Z

### L-014 - Concurrency limit tests must count the guarded expensive operation, not only the responses
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `auth` · harmful: 0
- features: core-platform
- evidence: N3 internal/users/auth.go:95-98 (auth)
- last seen: 2026-10-20T04:47:07Z

### L-015 - When tests override a configurable default, also assert the production default the spec names
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: core-platform
- evidence: N6 internal/live/hub.go:38; N16 internal/users/users.go:160 (tests)
- last seen: 2026-10-20T04:47:07Z

### L-016 - Test time-window boundaries with values strictly inside and outside the window, never at age zero
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: core-platform
- evidence: N9 internal/rooms/retention_test.go:115 (tests)
- last seen: 2026-10-20T04:47:07Z

### L-020 - Test permission-bit checks with group-only and other-only modes, not only a fully open mode
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `security` · harmful: 0
- features: agent-channel
- evidence: N3 internal/channel/private_unix.go:17 (security)
- last seen: 2026-10-20T07:38:14Z

### L-021 - Unit-test ownership checks with an injected file info carrying another uid when tests cannot create files as another user
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `security` · harmful: 0
- features: agent-channel
- evidence: N4 internal/channel/private_unix.go:14 (security)
- last seen: 2026-10-20T07:38:15Z

### L-022 - Bounded caches that implement a requirement need a test at the eviction boundary
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: agent-channel
- evidence: N11 internal/channel/link.go:30 (tests)
- last seen: 2026-10-20T07:38:15Z

### L-023 - When a limit is specified in Unicode code points, test a multi-byte input whose byte length exceeds the limit but whose code-point count does not.
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `text-limits` · harmful: 0
- features: connectors
- evidence: internal/connectors/inbound.go:143 (N22) (text-limits)
- last seen: 2026-10-20T08:16:42Z

### L-024 - When one component makes outbound calls through several clients (REST and WebSocket), test the network guard on each client separately, because the first refused call hides a bypass in the others.
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `ssrf` · harmful: 0
- features: connectors
- evidence: internal/connectors/whatsapp/connector.go:101,113 (N29, N36) (ssrf)
- last seen: 2026-10-20T08:16:42Z

### L-025 - Replay and dedupe state that a spec bounds by time must survive restarts of the component that holds it, or the spec must scope it to one running instance.
- signal: `ac_gap` · recurrence: 1 feature(s) · scope: `webhooks` · harmful: 0
- features: connectors
- evidence: HOOK-05; internal/connectors/webhook/connector.go:110 (replay accepted after restart) (webhooks)
- last seen: 2026-10-20T08:16:42Z

### L-026 - Key replay caches on the canonical value the server computes, and test that a non-canonical copy of the same credential (padding, case) is still caught as a replay.
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `webhooks` · harmful: 0
- features: connectors
- evidence: internal/connectors/webhook/connector.go:188 (N35) (webhooks)
- last seen: 2026-10-20T08:16:42Z

### L-027 - Assert a cache-hygiene rule before any refetch can overwrite the cache; test query clients with staleTime 0 make post-navigation cache checks vacuous
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `web-tests` · harmful: 0
- features: web-app
- evidence: F2 web/src/lib/api/queries.ts:516 (web-tests)
- last seen: 2026-10-20T10:09:33Z

### L-028 - When moving a data source behind a panel, test its failure state too, not only success and empty
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `web-tests` · harmful: 0
- features: web-app
- evidence: F3 web/src/routes/_app/index.tsx:237 (web-tests)
- last seen: 2026-10-20T10:09:33Z

### L-030 - Build platform fixtures from a captured real response (types included), not from a field list
- signal: `ac_gap` · recurrence: 1 feature(s) · scope: `connectors` · harmful: 0
- features: connectors
- evidence: internal/connectors/whatsapp/connector.go:76 (websocketEnable string in evolution-go 0.7.2) (connectors)
- last seen: 2026-10-20T12:21:13Z

### L-031 - Test identity-scoped rules such as reply-to-me with two actors of the same kind so a check on kind alone fails
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: routing-context
- evidence: validation.md G5 internal/delivery/wants.go:129 (tests)
- last seen: 2026-10-20T15:04:35Z

### L-032 - Assert spec-mandated text against literal spec wording, never against the production constant that emits it
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: routing-context
- evidence: validation.md G23 internal/agents/gateway.go:49 (tests)
- last seen: 2026-10-20T15:04:35Z

### L-033 - Turn measurable success criteria such as latency into an AC with a test, or mark them as manual UAT in the spec
- signal: `spec_precision_gap` · recurrence: 1 feature(s) · scope: `specs` · harmful: 0
- features: routing-context
- evidence: spec.md Success Criteria (2 s DM latency); validation.md Spec-Anchored ACs (specs)
- last seen: 2026-10-20T15:04:35Z

### L-034 - When a guard is added to several sibling implementations, give each sibling its own refusal test; a test on one sibling does not cover the others
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `tests` · harmful: 0
- features: harness-onboarding
- evidence: validation.md S04 (internal/harness/clis.go:276) (tests)
- last seen: 2026-10-21T08:27:24Z

### L-035 - Output the spec ties to a flag needs a negative assertion that it is absent when the flag is not given
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `cli` · harmful: 0
- features: harness-onboarding
- evidence: validation.md Y03 (internal/cli/setup.go:188) (cli)
- last seen: 2026-10-21T08:27:24Z

### L-036 - Test every rule of an input allowlist (scheme, port shape, host) with one rejected case each, not only the attacks that motivated it
- signal: `surviving_mutant` · recurrence: 1 feature(s) · scope: `scripts` · harmful: 0
- features: harness-onboarding
- evidence: validation.md I06, I07 (scripts/install.sh:58,67) (scripts)
- last seen: 2026-10-21T08:27:25Z

### L-037 - A URL allowlist AC must say which schemes are accepted, not only which plain-http hosts are allowed
- signal: `spec_precision_gap` · recurrence: 1 feature(s) · scope: `spec` · harmful: 0
- features: harness-onboarding
- evidence: validation.md I07 / ONB-10 (spec)
- last seen: 2026-10-21T08:27:25Z

## Quarantined (failed when applied - ignore)

A confirmed lesson that recurred alongside failure. Kept for the maintainer to review.

_none_
