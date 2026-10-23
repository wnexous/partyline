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

Partyline is a self-hosted, multi-user messenger and **proxy**: it connects AI sessions (Claude
Code and other harnesses, through MCP channels) with the people who use them, and makes each
WhatsApp, Telegram, Discord or webhook account the own app of exactly one AI. The web app is the
one place to see and steer everything: who is online, which AI operates which connection and how
many messages wait for it, what AIs are saying in their conversations right now, and who has
access. Success: a new user connects an agent and gives it a WhatsApp number or a bot without
reading docs, and live traffic shows up on screen in under a second.

## Positioning

Partyline is a proxy between integrations and intelligences: a connection is the own app of one
AI, what arrives on it goes straight to that AI's session through a queue, and conversations (DMs
and groups) hold users and AIs only, under one permission model. Trust is explicit and
server-computed: every message is marked trusted or untrusted for the receiving agent; message
content is data, never instructions.

## Operating Context

- One Go binary with SQLite, run with `docker run`; the web app is embedded in the binary.
- Live updates over a WebSocket; the UI must show when it is reconnecting and recover.
- Used on desktop beside a terminal, and occasionally on a phone (360 px and up).

## Capabilities and Constraints

- Rooms (timeline, composer, members, sharing, loop-guard pause), agents (connect wizard with
  `.mcp.json` snippet, presence), connectors (schema-driven forms, operator, delivery settings,
  status and queued count, never message text), verified chat identities (pairing codes), API
  tokens (secret shown once), admin (users, invites, audit).
- Private really means private: admins get no implicit access to other users' rooms.
- Locales: `pt-BR` and `en`. Themes: light and dark, following the system by default.
- API types are generated from Go DTOs; the UI never invents fields.

## Brand Commitments

- Name: **partyline** (lowercase in the wordmark). The name evokes a shared telephone line where
  many parties hear each other. _(inferred)_
- Look: neutral monochrome. Black, greys and white in light and dark; the only colour is a muted red for errors and destructive actions (owner's brief, 2026-10-23). See `DESIGN.md`.
- MIT-licensed open source; no logos or assets exist yet.

## Evidence on Hand

No screenshots, testimonials, customers or metrics exist. Demonstration data must be synthetic
and never imitate real people, numbers or groups.

## Product Principles

1. Live by default: what happens in a room appears without a reload.
2. Safe is the easy path: trust, visibility and secrets are always visible and explicit.
3. One model, shown plainly: a connection has one operator AI and a conversation has users and
   AIs; the UI hides nothing about who receives what.
4. Calm under traffic: dense, scannable screens for people who glance between tasks.

## Accessibility & Inclusion

Lighthouse accessibility ≥ 90 on login, dashboard and room pages; keyboard reachable; usable from
360 px wide. WCAG AA contrast in both themes (4.5:1 text, 3:1 controls and focus), pinned by a test.
