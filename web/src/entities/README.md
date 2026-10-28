# entities

Business objects: `intelligence`, `room`, `room-member`, `message`, `organization`, `connector`.
Each slice holds its domain molecules in `ui/` (avatar, row, session list), `model/` (hooks,
pure rules, adapters), `api/` (queries) and `lib/` (pure helpers), behind a public `index.ts`.

May import: `shared`. Never another entity, except through an explicit cross-API:
`entities/a/@x/b.ts` is what `a` exposes to `b`, imported as `@/entities/a/@x/b`.
Others import an entity only through `@/entities/<name>` (its `index.ts`).

Rules: `web/AGENTS.md`.
