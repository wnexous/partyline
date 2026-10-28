# shared/ui

Atomic design, app-agnostic. Organisms live in `widgets`, templates (layouts) in `app/layouts`,
pages in `pages`, domain molecules in `entities/*/ui`.

- `primitives/` - may import primitives, `shared/lib/utils` and packages only.
- `atoms/` - may import primitives and `shared/lib`; never molecules.
- `molecules/` - may import atoms, primitives, `shared/lib`, `shared/i18n`.
