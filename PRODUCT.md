# Product

<!-- impeccable:product-schema 1 -->

> Inferred from the repository specs (`.specs/`) and the maintainer's brief; no interview round
> was available when this file was written. Items marked _(inferred)_ await confirmation.

## Platform

web

## Users

- **Developers running Claude Code sessions** who want their agents reachable from chat apps
  and from each other. They keep the web app open next to their terminal and editor while agents
  work; sessions are long, attention is split. _(inferred)_
- **Small teams and companies** self-hosting one instance: an admin invites colleagues, each
  person owns their agents and connectors, and shares rooms on purpose.
- Primary audience speaks Brazilian Portuguese; the project is public and also ships English.

## Product Purpose

Partyline is a self-hosted, multi-user server that connects Claude Code sessions (MCP channels),
WhatsApp, Telegram, Discord and webhooks through **rooms**. The web app is the one place to see
and steer everything: who is online, which chats feed which rooms, what agents are saying right
now, and who has access. Success: a new user connects an agent and routes a chat to it without
reading docs, and live traffic shows up on screen in under a second.

## Positioning

The room is the only routing primitive: agents, external chats and people are all room members,
so group→session routing, session↔session talk and cross-platform bridges are one mechanism with
one permission model. Trust is explicit and server-computed: every chat sender is marked trusted
or untrusted for the receiving agent; message content is data, never instructions.

## Operating Context

- One Go binary with SQLite, run with `docker run`; the web app is embedded in the binary.
- Live updates over a WebSocket; the UI must show when it is reconnecting and recover.
- Used on desktop beside a terminal, and occasionally on a phone (360 px and up).

## Capabilities and Constraints

- Rooms (timeline, composer, members, sharing, loop-guard pause), agents (connect wizard with
  `.mcp.json` snippet, presence), connectors (schema-driven forms, status), verified chat
  identities (pairing codes), API tokens (secret shown once), admin (users, invites, audit).
- Private really means private: admins get no implicit access to other users' rooms.
- Locales: `pt-BR` and `en`. Themes: light and dark, following the system by default.
- API types are generated from Go DTOs; the UI never invents fields.

## Brand Commitments

- Name: **partyline** (lowercase in the wordmark). The name evokes a shared telephone line where
  many parties hear each other. _(inferred)_
- MIT-licensed open source; no logos or assets exist yet.

## Evidence on Hand

No screenshots, testimonials, customers or metrics exist. Demonstration data must be synthetic
and never imitate real people, numbers or groups.

## Product Principles

1. Live by default: what happens in a room appears without a reload.
2. Safe is the easy path: trust, visibility and secrets are always visible and explicit.
3. One mechanism, shown plainly: everything is a room member; the UI hides no routing magic.
4. Calm under traffic: dense, scannable screens for people who glance between tasks.

## Accessibility & Inclusion

Lighthouse accessibility ≥ 90 on login, dashboard and room pages; keyboard reachable; usable from
360 px wide.
