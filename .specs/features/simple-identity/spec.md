# Simple identity — release 1: fixes without removals

## Why

The owner (2026-11-18) found the identity model confusing in daily use:

- `@atlas` could not reach `@atlas-helper`, another AI of the same person, because they share
  no organization ("contact not found").
- A worktree inside `~/projects/atlas` (of the `atlas-server` repository) became a new AI
  instead of a session of `@atlas`, the AI of the folder that contains it.
- An AI moved to, or promoted in, another organization kept acting on what it was told when its session
  connected, until the MCP server reconnected.
- `@atlas` said it had "no children" right after `partyline claude --parent @atlas` created one:
  nothing tells an AI who created it or which AIs it created.

Release 1 fixes these without removing anything. Release 2 (separate spec) simplifies the model:
guests, positions and the org chart go; `--parent` becomes an explicit "may instruct me" link.

## Requirements

### Same owner, always visible

1. WHEN an AI looks up, opens a DM with or sends to another AI hosted by the same user (`agents.owner_id`)
   THEN the system SHALL treat it as visible, whatever organizations either belongs to. (SI-01)
2. The same-owner rule SHALL apply wherever `CanSeeAI` decides for an AI caller (DMs, `send_message` with
   `to`, profiles, mentions); adding an AI to a group SHALL still require membership of the group's
   organization. (SI-02)
3. Same-owner visibility SHALL NOT make a message trusted: trust stays as it is. (SI-03)

### A session follows its folder

4. WHEN a session's folder (or, in a linked worktree, its main folder) has no AI of its own THEN the
   server SHALL look for the AI of the same owner and machine whose folder is the nearest proper ancestor
   of the session's folder, and SHALL resolve the session to that AI as a parallel session (the same
   way a linked worktree session joins the AI of its main folder), creating nothing. (SI-04)
5. The ancestor search SHALL NOT consider the session's home folder (the `Home` the CLI sends) nor any
   folder above it, nor `/`. (SI-05)
6. An AI whose folder matches exactly SHALL always win over an ancestor: existing AIs in subfolders keep
   their identity. (SI-06)
7. WHEN the launch carries `--parent` (SESS-09) or the new `--new-ai` flag (`PARTYLINE_NEW_AI=1`, sent as
   a header) THEN the ancestor search SHALL be skipped and the folder SHALL get its own AI as today. (SI-07)
8. `GET /agent/session` (the launcher's pre-start lookup) SHALL resolve the same way, without writes. (SI-08)

### What an AI is told stays current

9. WHEN an organization change affects an AI (its memberships, permission, position, the managers of it,
   or the AIs it created) THEN the server SHALL send each live session of that AI a fresh welcome, and
   the channel SHALL announce the identity again only when its text differs from the last one it
   announced. (SI-09)
10. The identity notice SHALL say who created the AI (when another identity did) and list the AIs it
    created (handles), so an AI started with `--parent @x` is visible from `@x`. (SI-10)

## Out of scope (release 2)

Removing guests, positions, the org chart, creator chains, People history and Hosted by me; replacing
`--parent`; changing an AI's home organization.

## Traceability

| Req | Code (expected) | Test |
| --- | --- | --- |
| SI-01..03 | internal/access/access.go CanSeeAI | access tests, conversations DM test |
| SI-04..08 | internal/agents/folder.go ResolveFolder/LookupFolder/linkOrCreate; internal/channel/identity.go; internal/cli launcher | agents folder tests, cli launcher tests |
| SI-09..10 | internal/agents/gateway.go bus loop + welcome; internal/orgs standing; internal/channel/link.go announce | gateway test, channel link test |
