# pages

One slice per screen: `pages/<page>/ui/<page>.tsx`, `model/` for state only that page uses, and a
public `index.ts`. A page composes widgets, features and entities; it is rendered by a route file.

May import: `widgets`, `features`, `entities`, `shared`. Never another page.
Others import a page only through `@/pages/<page>` (its `index.ts`).

Rules: `web/AGENTS.md`.
