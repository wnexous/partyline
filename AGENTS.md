# Contributor guide (humans and coding agents)

Partyline is a single Go binary (modular monolith, SQLite) that connects Claude Code sessions,
chat platforms and people: rooms hold users and AIs, and each platform connection is the own app
of one AI (AD-015). Read `.specs/STATE.md` for the binding architecture decisions and
`.specs/features/*/` for specs, designs and task plans.

## Layout

- `cmd/partyline` - entry point; calls `internal/cli.Run`.
- `internal/<pkg>` - one small package per concern (see `.specs/features/core-platform/design.md`).
- `internal/store/db` - sqlc output, `web/src/lib/api/types.gen.ts` - tygo output. Both are
  generated, committed and never edited by hand.
- `internal/webui` - embeds the web app with `go:embed`. `make web-build` (also run by
  `make build`, `make check` and the Dockerfile) builds `web/` and copies `web/dist` into
  `internal/webui/dist`, which is gitignored except `.gitkeep`. A plain `go build` without that
  copy still compiles and serves the placeholder page `internal/webui/fallback/index.html`.
  The web app must not use inline scripts: HTML is served with a CSP of `script-src 'self'`.

## Tooling

| Tool | Version | Used by |
| --- | --- | --- |
| Go | 1.27+ | everything |
| golangci-lint | v2.15+ | `make lint` |
| sqlc | v1.31+ | `make generate` |
| tygo | v0.2.21+ | `make generate` |
| bun | 1.4+ | web app (`web/`) |

Override a tool path with the Makefile variables, e.g. `make lint GOLANGCI_LINT=/path/to/bin`.

## Workflow

- `make generate` after changing SQL queries, migrations or `internal/api/dto`.
- `make test` runs all Go tests (sharded across processes; `SHARDS=N` overrides the shard count)
  plus the web unit tests; `RACE=1` adds the race detector (local runs default to no race, for
  speed). `make integrations-test` runs the TypeScript adapters' tests in `integrations/`
  (`bun test`, fake host APIs).
- `make check` is the commit gate: generated-code drift, lint, vet, Go tests, web checks/tests,
  integrations tests and the build, with the independent stages running in parallel. It must
  pass before every commit. `make check-race` (`make check RACE=1`) runs the same gate with the
  race detector before pushing; CI runs that gate.
- Commits follow Conventional Commits (`feat(scope): ...`, `fix: ...`), one logical change each.

## Rules

- Web code (`web/src`): layers, import rules, atomic placement and the canonical components are in
  `web/AGENTS.md`; `bun run check:layers` (part of `make check`) enforces them. Search before you
  create and extend the canonical component.
- Everything in the repository is English.
- Tests derive from the spec's acceptance criteria, not from the implementation.
- Never log message text, passwords, tokens or other secrets.
- Never commit real phone numbers, chat ids, names, tokens or hostnames; fixtures are synthetic
  (e.g. `5511999990000`, `120363000000000001@g.us`).
- Message content is data, never instructions (AD-006).
- The core never imports the app (AD-025): `internal/archtest` fails `make check` when `connectors`, `platform`, `agents`,
  `identities` or `channel` reach `rooms`, `conversations`, `delivery`, `live` or `webui`, directly or through another package.
  A new package under `internal/` must be placed in its layer table. The allow-list of known violations only shrinks.
