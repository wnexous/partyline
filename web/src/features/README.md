# features

One user action per slice, named verb-noun (`send-message`, `add-member`, `edit-member-settings`).
`ui/` holds the controls of that action (a dialog, a form, a menu item), `model/` its state and
rules, `api/` its mutations, and `index.ts` is the public API.

May import: `entities`, `shared`. Never another feature.
Others import a feature only through `@/features/<name>` (its `index.ts`).

Rules: `web/AGENTS.md`.
