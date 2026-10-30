# Tasks: container agents (runner model)

Every task: own branch, tests first, `make check SHARDS=2 GOFLAGS=-p=2` green (and `make e2e` if
web), atomic commits. Order: T1 → (T2, T3, T5 in parallel) → T4 → T6 → T7 → T8.

## T1 — store (CA-01, CA-03, CA-04 data)
Migration 0029 with `runners`, `owner_secrets`, `container_agents` (design "Data"), sqlc queries
(create/get/list/update/delete per table; container_agents by agent/runner; update state,
desired, last_error; runner seen/docker info), `make generate`, migration test. Status: done

## T2 — runner registry, token kind, protocol types, hub + API (CA-01, CA-02, CA-07)
Token kind `runner` (scoped routes, created by the device flow with `kind=runner`), `internal/runners`
(register, list, rename, delete+revoke, live hub with request/response over WS, state updates),
`internal/runners/spec` (op types + validators shared with the CLI), routes + DTOs + tygo, audit,
tests with a fake runner WS client. Status: done

## T3 — owner secrets (CA-03, CA-07, CA-08)
`internal/ownersecrets` + routes (names only on GET, PUT value, DELETE), audit, tests proving the
value never comes back and is sealed in the DB. Status: done

## T4 — `partyline runner` CLI + executor (CA-01, CA-02, CA-05, CA-08, CA-09)
Login/register, WS client with reconnect, executor with `exec.Command("docker", …)` argv only,
env file 0600, `--check`, `install`/`uninstall` (systemd user unit), docker events/poll state.
Tests with a fake `docker` on PATH and a test server. Status: done

## T5 — agent image (CA-04, CA-05)
`deploy/agent/Dockerfile` + entrypoint, per-harness credential setup from env (verify how Claude
Code, pi, OpenCode, omp read credentials), release job pushing `ghcr.io/wnexous/partyline-agent`,
smoke script. Status: done

## T6 — container agents service + API (CA-04, CA-06, CA-07, CA-08)
`internal/containeragents` (create identity + token + `run` via hub, lifecycle, logs, delete with
volume option + token revoke, last_error), routes, audit, tests with the fake runner. Status: done

## T7 — web (CA-01..CA-06, CA-10)
Slices per design "Web"; vitest; one e2e with a fake runner client. Status: done

## T8 — docs + live smoke (all)
`docs/container-agents.md`, README line, AD-036 in STATE.md. Status: done
Live smoke on the dev box after deploy. Status: pending

## T9 — hotfix after the first live test (CA-04, CA-06, CA-11, CA-12)
Found on prod v0.12.0: a second container AI on one runner without a repository collided with the
first (same machine `runner:<id>` and folder `/workspace`, so the `agents_folder` unique index
refused it and the web showed the generic 500); a create without a credential restart-looped with no
way to add one; Docker's `Restarting` was shown as running; and known error codes fell through to
the generic failure. The machine is now `container:<container-name>`; a create requires a variable
the harness reads (422 `credentials_required`); `PATCH /agents/{id}/container` edits the mapping and
recreates the container with the same volume; the protocol reports `restarting` with `last_log`
(redacted) as `last_error`; the web has a sentence per known code, a Credentials dialog and a logs
shortcut next to the last error. Tests: two containers on one runner, credential refusals, the
credential PATCH (success and offline), spec/executor/hub for `restarting` and `last_log`, web unit
tests. Status: done
