# widgets

Organisms: self-contained blocks of a screen that compose features and entities (member panel,
conversation list, conversation header, timeline…). One slice per widget: `ui/`, `model/`, and a
public `index.ts`.

May import: `features`, `entities`, `shared`. Never another widget.
Others import a widget only through `@/widgets/<name>` (its `index.ts`).

Rules: `web/AGENTS.md`.
