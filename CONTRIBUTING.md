# Contributing to partyline

Thanks for looking. Bug reports, fixes, docs and ideas are all welcome, in English or Portuguese.

## Before you start

- **Bugs:** open an issue with the bug template. Small, obvious fixes can go straight to a pull
  request.
- **Features:** open an issue first and describe the problem, not only the solution. Partyline
  stays small on purpose, and a feature that adds a screen, a setting or a concept has to earn its
  place against the principles in [PRODUCT.md](PRODUCT.md). A quick "no, because…" on an issue
  saves you a pull request.
- **Security problems:** never in a public issue. See [SECURITY.md](SECURITY.md).

## Making a change

The contributor guide for humans and coding agents is [AGENTS.md](AGENTS.md): layout, tools,
code generation and the rules every change follows. The short version:

1. `make generate` after changing SQL, migrations or `internal/api/dto`.
2. `make check` must pass. It is the same gate CI runs: generated-code drift, linters, Go and web
   tests, and the build. Web changes also run `make e2e`.
3. Behaviour lives in specs under `.specs/features/`. A change in behaviour updates the spec's
   acceptance criteria, and the tests follow from those criteria.
4. UI changes must work on a phone: 320 px and 412 px wide with a 125% system font
   (`web/e2e/fit.spec.ts` checks it). Add screenshots to the pull request.
5. One logical change per commit, with [Conventional Commits](https://www.conventionalcommits.org/)
   (`fix(web): ...`, `feat(channel): ...`).

Keep fixtures synthetic: no real phone numbers, chat ids, names, hostnames or tokens.

## How decisions are made

The maintainer decides what goes in, using [PRODUCT.md](PRODUCT.md) as the bar: simple to use,
live by default, safe by default, one model shown plainly. Removing something that does not pull
its weight is as welcome as adding something that does.

## License

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
