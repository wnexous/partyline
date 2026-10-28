# app

Providers, router wiring, layouts (templates), live-socket wiring, push registration and global
styles. `routes/` (the TanStack Router file routes) is part of this layer: a route file only wires
a URL to a page.

May import: `pages`, `widgets`, `features`, `entities`, `shared`.
Nothing imports `app` except `main.tsx` and the routes.

Rules and the canonical components: `web/AGENTS.md`. Checked by `bun run check:layers`.
