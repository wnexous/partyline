# shared

No business knowledge and no slices; anything above may import it, deep paths included
(`@/shared/ui/molecules/confirm-dialog`).

- `ui/primitives` - shadcn/radix primitives. Import only other primitives, `shared/lib/utils` and
  packages: no i18n, no hooks, no app code. Labels come as props.
- `ui/atoms` - the smallest app-agnostic pieces (StatusDot, RelativeTime, Logo). May import
  primitives and `shared/lib`.
- `ui/molecules` - combinations of atoms (EmptyState, ConfirmDialog, FormDialog, ActionMenu…).
  May import atoms, primitives, `shared/lib` and `shared/i18n`.
- `api` - http client, query keys, error text, live socket transport, generated types.
- `lib` - pure helpers (time, device, avatar tone, clipboard, forms, validation, `cn`).
- `i18n` - provider, `useT`, dictionaries.

Rules: `web/AGENTS.md`.
