# Tasks: container agents

Every task: own branch, tests first, `make check SHARDS=2 GOFLAGS=-p=2` green (and `make e2e` if
web), atomic commits. Order: T1 → (T2, T3, T4, T5 in parallel) → T6 → T7 → T8.

## T1 — store (CA-01, CA-03, CA-04 data)
Migration 0029 with `servers`, `owner_secrets`, `container_agents` (design "Data"), sqlc queries
(create/get/list/update/delete per table; container_agents by agent/server), `make generate`, a
migration test. Status: pending

## T2 — servers service + API (CA-01, CA-02, CA-07, CA-08)
`internal/servers` (key generation, sealed private key, Check with TOFU host key pin, timeouts),
API routes + DTOs + tygo, audit entries, owner-only authz tests, an in-process SSH test server
helper in `internal/testkit` (reused by T4). Status: pending

## T3 — owner secrets service + API (CA-03, CA-07, CA-08)
`internal/ownersecrets` + routes (names only on GET, PUT value, DELETE), audit, tests proving the
value never comes back and is sealed in the DB. Status: pending

## T4 — container runner over SSH (CA-05, CA-08, CA-09)
`internal/containers` Runner + sshRunner + shellQuote + validators; env file via stdin; tests
against the T2 test SSH server asserting exact commands, quoting, stdin env file, no secret in any
command string. Status: pending (needs T2's test server helper; can stub it first)

## T5 — agent image (CA-04, CA-05)
`deploy/agent/Dockerfile` + entrypoint (clone, per-harness credential setup from env, TTY via
`script`, restart loop), release workflow job pushing `ghcr.io/wnexous/partyline-agent:<tag>`,
a smoke test script that builds the image and runs `partyline version` inside. Verify how each of
Claude Code, pi, OpenCode, omp reads credentials from env; document. Status: pending

## T6 — container agents service + API (CA-04, CA-05, CA-06, CA-07, CA-08)
`internal/containeragents` (create identity + token + container, lifecycle, logs, state, delete
with optional volume, token revoke, last_error), routes, audit, tests with a fake Runner.
Status: pending

## T7 — web (CA-01..CA-06, CA-10)
Slices per design "Web"; servers + secrets pages, create wizard, profile container section;
vitest + one e2e with the API mocked or a fake runner flag. Status: pending

## T8 — docs + live smoke (all)
`docs/container-agents.md` (setup a server, secrets, create an AI, troubleshooting), README line,
AD-036 in STATE.md; live smoke on the dev box once deployed. Status: pending
