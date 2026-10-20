# Core Platform Context

**Gathered:** 2026-10-19
**Spec:** `.specs/features/core-platform/spec.md`
**Status:** Ready for design

---

## Feature Boundary

Self-hosted multi-user server where rooms connect Claude Code agents, external chats and people, with sharing, live updates and a hardened container. Connectors, the agent channel and the web app are separate features built on this core.

---

## Implementation Decisions

### Routing model
- Rooms with members (agents, chats, users). One mechanism for chat→agent, agent↔agent and chat↔chat bridges.

### Untrusted content
- Agents receive the full text of every message, flagged `trusted=true|false` (recipient-relative). No content-free "notice only" mode in v1.

### Login
- Local accounts. First admin via a setup token printed in the log; everyone else via single-use invites.

### Agent's Discretion
- User said "você escolhe" for the stack and "o que tiver de sugestão ou melhoria, pode implementar". Agent chose: Go modular monolith + SQLite, Vite/React/shadcn/TanStack/zustand/zod front, project name **partyline**, MIT license, loop guard, audit log, verified identities via pairing codes, signed webhooks, encrypted connector secrets, command palette, pt-BR + en.

### Declined / Undiscussed Gray Areas → Assumptions
- Limits and defaults (loop guard 25, retention 90 d, text 16k, upload 25 MiB, session 30 d) are logged as assumptions in the spec.

---

## Specific References

- Previous private prototype (Python, single user, spool + terminal notices). Its evolution-go findings (broadcast WebSocket) carry over; its code and data do not.
- "front com ws pra ver tudo ao vivo", "separação por usuário", "compartilhar sessões com outros usuários, público ou privado total".

---

## Deferred Ideas

- OIDC/OAuth login; teams/groups; Postgres; per-user trust lists; permission relay; Slack/Matrix native connectors; message edit propagation; metrics endpoint (Prometheus).
