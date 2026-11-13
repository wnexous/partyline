# Secret Requests Tasks

**Spec**: [spec.md](spec.md) · **Design**: [design.md](design.md)

Each task is one commit; `make check` passes before each.

| # | Task | Requirements | Tests |
| --- | --- | --- | --- |
| T1 | DTOs (interactive/answer shapes, request body, frames, error codes, limits), `CountAnswers` query, `make generate` | SR-01, SR-05, SR-06 | generated drift |
| T2 | Server: `rooms` secret request + `GiveSecret`; `agents.Gateway.DeliverSecret` with the pending map and version gate; API route and error mapping; testkit helpers | SR-01, SR-03, SR-05..SR-07, SR-10 | `internal/api/secret_requests_test.go`, `internal/agents/secret_test.go` |
| T3 | Client: `secret` frame in channel and listen, `writeSecret`, SR-09 note, `request_secret` tool, CLI secrets dir, instructions | SR-01, SR-02, SR-08, SR-09, SR-11 | `internal/channel/secret_test.go`, `secret_unix_test.go`, `tools_test.go`, `instructions_test.go` |
| T4 | Web card, mutation, `managesAI`, i18n en/pt-BR | SR-04 | `web/src/routes/_app/rooms/secret-request.test.tsx` |
| T5 | e2e: save through the web app into a session; fit of the card | SR-13 | `web/e2e/secret-requests.spec.ts`, `web/e2e/fit.spec.ts` |
| T6 | Docs: api.md, agents.md, README, README.pt-BR | SR-12 | — |
