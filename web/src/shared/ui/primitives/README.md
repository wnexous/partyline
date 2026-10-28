# shared/ui/primitives

shadcn/radix primitives (button, dialog, sheet…). Import only other primitives, `shared/lib/utils`
and packages: no i18n, no hooks, no app code. A label a primitive needs (e.g. "Close") is a prop
with an English default that the caller overrides.
