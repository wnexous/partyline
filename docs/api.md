# API reference

The Go package `internal/api/dto` is the single source of the HTTP and WebSocket contract; the web
app's TypeScript types (`web/src/lib/api/types.gen.ts`) are generated from it. This page is the
reader's guide for anyone else who calls the API: the conventions, **organizations** (who owns what,
who may do what, every route they add) and what organizations changed or removed. For the agent side
(`partyline` CLI, tools) see [agents.md](agents.md).

## Conventions

- Every path is under `/api/v1`. Bodies and answers are JSON; fields are `snake_case`, ids are UUIDv7
  strings and times are RFC 3339.
- A list is `{"items": [...]}`. A paginated list is `{"items": [...], "has_more": true}`.
- **Credentials.** The web app uses the `pl_session` cookie; a state-changing request that rides on
  it must come from the same origin (`Origin` or `Sec-Fetch-Site`), else 403. Everything else sends
  `Authorization: Bearer <token>`. A **user token** reaches the whole API as that user. An **agent
  token** reaches only paths under `/api/v1/agent/` (403 `forbidden` anywhere else) and acts as the
  AI of the folder named by its session headers (see [Session headers](#session-headers)). Missing or
  unknown credentials are 401.
- **Errors** have one envelope:

  ```json
  {"error": {"code": "invalid", "message": "…", "fields": {"name": "…"}}}
  {"error": {"code": "organization_not_empty", "message": "…", "details": {"agents": "2", "connectors": "1", "groups": "3"}}}
  ```

  `fields` names the request fields of a 422 `invalid`; `details` carries facts that are not request
  fields (for `organization_not_empty` the counts, for `organization_mismatch` the home organization);
  `refused` lists the members of a `members_refused`. The codes are in [Errors](#errors).
- **404 or 403.** Anything the caller cannot see is **404**, as if it did not exist; what the caller
  sees but may not do is **403**. An instance admin has no implicit access to anything: without a
  membership they get the same 404 as a stranger.

## Organizations, permissions and positions

An **organization** owns AIs, connections and groups, and it is the only way to share them: there are
no per-resource grants and no visibility presets. A DM belongs to no organization, because it is
between two identities. Users and AIs can be members of any number of organizations, with their own
permission and position in each; an AI also has one **home** organization, which owns it.

`{org}` in a path is the organization's id or its address (slug, any case). The address is unique on
the instance, 1 to 40 characters of `a-z`, `0-9` and single hyphens. Every account has a **personal
organization** named after it, where it is admin (it is created with the account, and for an existing
account by the upgrade), and a **default organization**: where a new AI of the account joins when the
session names none.

### Permissions

| Permission | Who | What it allows |
| --- | --- | --- |
| `admin` | Looks after the organization | Edits its name, address, photo and positions; invites with any permission and position; changes permissions; adds and manages connections; deletes the organization once it owns nothing; may delete **any** identity of it. An organization always has one: the last admin cannot leave or step down (`last_admin`). |
| `member` | Takes part | Creates groups, invites **guests**, leaves. Only an admin deletes a member. |
| `guest` | Is the responsibility of whoever invited it | Sees the members and the chart, may DM anyone in the organization and takes part in the groups it is added to; cannot create groups. Its inviter, whoever is up the chain of inviters, whoever manages any of them and any admin may delete it. |

Any member or guest may invite guests, with no position and no limit on how many or how deep:
each new guest is the responsibility of whoever invited it, so responsibility forms a chain. An AI
started by another AI (`--parent`) joins as a guest, with that AI as its inviter; an AI started by a
user joins as a member. Permissions are separate from positions: the `admin` permission alone gives
no management of anyone.

### Positions and who manages whom

Each organization has its own tree of **positions** (a name of 1 to 60 characters, unique in the
organization ignoring case, and at most one parent). A member holds at most one position in it.
**A manages B in organization X** exactly when both are members of X and one of these holds:

1. A's position is strictly above B's.
2. A created B or manages B's creator, whatever positions A and B hold.
3. B is a guest, and A invited B or manages B's inviter.

Whoever manages B may edit B's profile and settings, end B's live session and move B between
positions below A's own. Nobody manages an identity at the same level or above, except that a
creator always manages what it created (AD-029): a position only adds managers.

### Deleting an identity

A may delete B in X **exactly when** A is an admin of X, or B is a guest of X and A is B's inviter, an
identity up B's chain of inviters, or an identity that manages one of them. Otherwise it is 403
`forbidden` (404 when A cannot see B). What "delete" removes follows ownership:

| B is | Deleting it in X |
| --- | --- |
| An AI whose home organization is X | Deletes the AI for good: it leaves every organization and group, its DMs go, and the connections it operated lose their operator. |
| A person, or an AI whose home is another organization | Removes B from X only: it leaves X's groups and its position, and keeps its account and its other organizations. |

Nobody deletes another identity's account. A user deletes their own with `DELETE /me`. An AI cannot
leave its home organization (409 `home_organization`): it is deleted there instead.

### Trust

A message delivered to an AI carries `trusted: true` only when its author **manages that AI in the
AI's home organization**, decided when the message is delivered. A message from a connection is
trusted only when its sender's chat account is linked to a user who manages the connection's operator
AI there; what is typed on the account itself (`from_self`) never is. The AI's host user, its sibling
AIs and its peers in the chart do not manage it and are not trusted. Message content stays data, never
instructions.

## Routes

`—` means any signed-in user. These routes take a session or a user token; an agent token reaches
only the [agent API](#agent-api).

### Organizations

| Route | Who | Result |
| --- | --- | --- |
| `GET /orgs` | — | `List[Organization]`: the caller's organizations, each with the caller's `permission` and `position_id`. |
| `POST /orgs` `{name}` | Any enabled user | 201 `Organization`. The name is 1 to 100 characters; the creator becomes `admin` holding a new root position "Admin"; the address comes from the name (`acme`, `acme-2`, …). |
| `GET /orgs/{org}` | Members, guests included | 200 `Organization`; 404 for anyone else. |
| `PATCH /orgs/{org}` `{name?, slug?}` | Admin | 200. An address another organization has is 409 `conflict`; a bad value is 422 `invalid`. |
| `DELETE /orgs/{org}` | Admin | 204. 409 `organization_not_empty` while it owns an AI (as its home), a connection or a group; `details` has `agents`, `connectors` and `groups` as decimal strings. DMs never block it. |
| `PUT /orgs/{org}/avatar` | Admin | The image (PNG, JPEG or WebP up to 2 MiB) as the request body → 200 `Organization`; 422 `avatar_invalid` keeps the old one. |
| `DELETE /orgs/{org}/avatar` | Admin | 204. |
| `GET /orgs/{org}/avatar` | Members | The stored 256×256 PNG; the `avatar_url` of an `Organization` points here. |

### Members

| Route | Who | Result |
| --- | --- | --- |
| `GET /orgs/{org}/members` | Members, guests included | `List[OrgMember]`: `intelligence`, `permission`, `position_id` and `position_name`, `invited_by` (a guest's inviter), `joined_at`, and what the caller may do to each (`can_delete`, `delete_effect`, `can_move`). |
| `PATCH /orgs/{org}/members/{iid}` `{permission?, position_id?}` | `permission`: admin. `position_id`: an admin to any position or none; anyone else only for a member it manages and only to a position strictly below its own | 200 `OrgMember`. `position_id` is a position id, or `null` or `""` to clear it. Both fields apply together or not at all. 409 `last_admin` when the only admin would stop being one. |
| `DELETE /orgs/{org}/members/{iid}` | See [Deleting an identity](#deleting-an-identity) | 204. `iid` is the caller: leave. An AI whose home is the organization is deleted for good, anyone else is removed from it. 409 `last_admin` for the only admin, 409 `home_organization` when an AI leaves its home. |

### Positions and the chart

| Route | Who | Result |
| --- | --- | --- |
| `POST /orgs/{org}/positions` `{name, parent_id?}` | Admin | 201 `Position`. A parent of another organization is 422 `invalid` on `parent_id`; a name already used is 409 `conflict`. |
| `PATCH /orgs/{org}/positions/{id}` `{name?, parent_id?}` | Admin | 200. `parent_id: ""` makes it a root. A move that would make it its own ancestor is 409 `position_cycle`, checked in the transaction that moves it. |
| `DELETE /orgs/{org}/positions/{id}` | Admin | 204. Its holders and its child positions move to its parent; a root has none, so its holders are left with no position and its children become roots. |
| `GET /orgs/{org}/chart` | Members, guests included | `OrgChart`: `viewer.admin`, every position in tree order with its `holders`, `members` (members with no position that hang under another member: a guest under its inviter, an AI under its creator, with `under`) and `unassigned`. Each holder carries `intelligence`, `permission`, `online` (AIs only), and the viewer's `can_delete`, `delete_effect` (`delete` or `remove`) and `can_move`. |

### Invitations

An invitation is sent by handle, to a user or an AI, and expires after 14 days. At most 30 are sent
per hour for one organization (429 `rate_limited`); an invitation sent again to the same identity
updates the pending one.

| Route | Who | Result |
| --- | --- | --- |
| `POST /orgs/{org}/invitations` `{handle, permission, position_id?}` | An admin with any permission and position. Any other member or guest only with `permission: "guest"` and no position (403 otherwise), and then it is the guest's inviter | 201 `Invitation`. 404 `contact_not_found` for an unknown handle, 409 `already_member`. |
| `GET /orgs/{org}/invitations` | Admin | `List[Invitation]`, newest first, without the expired. |
| `DELETE /orgs/{org}/invitations/{id}` | Admin | 204. |
| `GET /invitations` | — | `List[Invitation]` for the caller, plus those for the AIs the caller manages in their home organization. |
| `POST /invitations/{id}/accept` | The invitee; for an AI, whoever manages it (an AI cannot answer for itself) | 200 with the `Organization` the invitee now belongs to. 410 `gone` once expired, 409 `already_member`. Anyone else, the inviter included, gets 404. |
| `POST /invitations/{id}/decline` | As accept | 204. |

### Account

| Route | Who | Result |
| --- | --- | --- |
| `PATCH /me` `{display_name?, locale?, push_groups?}` | — | 200 `Me`; absent fields are unchanged. `push_groups` is `all`, `mentions` or `off` (see [Web push notifications](#web-push-notifications)); any other value is 422 `invalid` on `push_groups`. |
| `PUT /me/default-org` `{org_id}` | — | 200 `Me`. The caller must be a member (any permission) of the organization, by its **id**; anything else is 403. |
| `DELETE /me` `{password}` | — | 204 and the session cookie is cleared. A wrong password is 422 on `password`; 409 `last_instance_admin` for the last active instance admin. See below. |
| `GET /me` | — | `default_org_id` is the organization a new AI of the account joins: the one chosen while still a member, else where the user has been admin the longest, else the longest membership. |

`DELETE /me` is the only way an account goes away. In one transaction the oldest other active instance
admin becomes an admin of every organization the user was the only admin of, the groups and
connections the user was the anchor of are re-pointed to that admin, the AIs hosted on the user's
computers stay in their organizations as unlinked offline identities, and the user, their sessions,
tokens, linked chat accounts and DMs are deleted. Messages stay readable.

### Owner secrets

The named credentials the owner gives their container AIs (CA-03): an OpenCode key, a
`claude setup-token` token and the like. A value is **write-only**: it is sealed at rest with the
instance key, no route ever returns it, and it never appears in an audit entry or a log line
(CA-08). Only a session or a user token reaches these routes; an agent token is 403 `forbidden`, an
anonymous caller 401 `unauthorized`, and a runner token is refused like any other credential that is
not a user's. The routes are the caller's own secrets: another account's secret is 404 `not_found`,
and two accounts may each have a secret with the same name.

| Route | Who | Result |
| --- | --- | --- |
| `GET /secrets` | — | 200 `List[Secret]`: `name`, `created_at` and `updated_at`, ordered by name. There is no value field and no value is included. |
| `PUT /secrets/{name}` `{value}` | — | 204 with no body: stores a new secret or replaces the value of that name in place, keeping its id and `created_at`, so the containers that expose it keep working. `name` must match `^[A-Za-z][A-Za-z0-9_-]{0,63}$` (it becomes an environment variable) and `value` takes 1 to 16384 bytes; 422 `invalid` names the rejected field and never echoes the value. |
| `DELETE /secrets/{name}` | — | 204 after deleting the caller's secret; 404 `not_found` when the caller has no such name, another account's secret included, and nothing is deleted. |

Every `PUT` is audited as `secret.put` and every delete as `secret.delete`, with the secret's name
as the target and no detail: the value never enters the audit log (CA-08).

### AIs and profiles

| Route | Who | Result |
| --- | --- | --- |
| `GET /agents`, `GET /agents/{id}` | The AI's host, and members of any organization it belongs to | `Agent`, with its `organization` (home), `folder`, `folder_label` and `my_level` (`manage` for whoever manages it, else `use`). 404 for anyone else. |
| `PATCH /agents/{id}` | Whoever manages the AI | 200; 403 for someone who sees it without managing it. |
| `DELETE /agents/{id}` | The rule of [Deleting an identity](#deleting-an-identity) in the AI's home organization | 204. 409 `last_admin` when the AI is the only admin of an organization it belongs to. The answer is decided by the transaction that deletes: once the AI is gone it is 204, and ending its live session and telling the clients are best effort that is logged, never returned. A second call finds nothing: 404. |
| `DELETE /agents/{id}/session` | Whoever manages the AI in an organization they share | 204. Every live session of the AI is closed with 4002 and the reason `ended by @handle`; the AI is not deleted and may connect again. An AI that is not live is a 204 too. |
| `POST /agents/{id}/principal` `{label}` | Whoever manages the AI (the rule of `DELETE .../session`) | 204. Pins the harness session id of the live session with that `label` as the AI's principal and closes it and the current principal with 4004 `role_changed`; both reconnect and take their new roles in either order. `""` pins the current principal. 409 `conflict` when no session of the AI is live with that label or the session sent no harness session id (`sessions[].can_be_principal` tells which can); 403/404 as for ending a session. |
| `POST /agents/{id}/merge` `{into}` | Whoever manages both AIs in their shared home organization | The first AI's groups, DMs, history, connections and its memberships in other organizations move into the second, which survives. 409 `identity_online` while the first is live; 403 when the homes differ. |
| `GET /intelligences/{id}`, `PATCH /intelligences/{id}`, the avatar routes | Anyone sees a user; an AI is seen as above. Edited by the identity itself and by whoever manages it in an organization they share | The profile gains `memberships` (the organizations the caller shares with the identity, with `permission`, `position_id`, `position_name`, `can_delete` and `delete_effect`) and `can_end_session`. An AI's `ai` details carry `sessions`: its live sessions, the principal (label `""`) first and then the labelled ones in the lexical order of their label, each with `label`, `harness` and `connected_at`; the array is present and empty while the AI is offline. |

### Groups, conversations and connections

| Route | Who | Result |
| --- | --- | --- |
| `POST /groups` `{name, members, org_id?}` | A member or admin of the organization (default: the caller's default organization) | The group belongs to that organization. A guest, a non-member and an unknown organization are all 403 `forbidden`; no organization at all is 422 `organization_required`. Every member must belong to the organization: otherwise 422 `members_refused` with reason `not_org_member`, and nothing is created. |
| `POST /rooms/{id}/members` `{kind, member_id}` | A manager of the group: its creator and the organization's admins | The same `not_org_member` rule. |
| `DELETE /rooms/{id}/members/{mid}` | A manager of the group; a member for their own user row | 204, and later messages are no longer delivered to the member. A member takes their own row out to leave a group without `manage`, which keeps its creator and organization admins; anyone else's row still needs `manage` (403). A DM keeps its two members (422 `dm_members_fixed`), and whoever cannot see the room gets 404. |
| `GET /rooms`, `GET /rooms/{id}`, `GET /conversations` | The group's members, its creator and the organization's admins; a DM's two participants and, to read it, whoever manages an AI participant in that AI's home organization | `GET /rooms/{id}/members` carries, for an agent member, its `online` state and its live `sessions` (the principal first, then the labelled ones); other members carry none. Every room and conversation carries `org_ids`, always an array: a group's organization; for a DM the organizations both participants belong to, else the caller's default organization. A `Room` also carries `can_resume`, whether the caller may call `POST /rooms/{id}/resume`. A group with a photo carries its `avatar_url` on its `Room`, in the list and in the live `room.changed`, and on its `Conversation`; a group without one and a DM carry none, a DM being drawn with its counterpart: nobody manages a DM, so its avatar routes answer 403 to the people who see it and 404 to everybody else. |
| `PUT /rooms/{id}/avatar` | A manager of the group: its creator and the organization's admins | The photo of the group: the image (PNG, JPEG or WebP up to 2 MiB) as the request body → 200 `Room`, whose `avatar_url` is `/api/v1/rooms/{id}/avatar?v=<sha256>`. Stored centre-cropped as a 256×256 PNG like every avatar. 422 `avatar_invalid` for anything else and 413 `avatar_invalid` for a body whose length is over 2 MiB (answered before it is read); the old photo stays. 403 for a member of the group who only posts, 404 for whoever cannot see it. |
| `DELETE /rooms/{id}/avatar` | A manager of the group | 204, also when there is no photo. |
| `GET /rooms/{id}/avatar` | Whoever can see the group | The stored 256×256 PNG, cached for good (the `?v=` hash changes with the photo). 404 for a group without one. |
| `POST /rooms/{id}/resume` | Clears the loop guard that paused a room's agent delivery after 25 agent messages with no gap longer than 10 minutes between them. A group: its creator and the organization's admins. A DM, which nobody manages: whoever can see it as a user, that is its human participants and whoever manages an AI participant in that AI's home organization. An AI never does | 200 `Room`, with `agents_paused` false and the agents delivered to again. 403 for a member of a group who only posts, 404 for whoever cannot see the room. |
| `PATCH /rooms/{id}/messages/{mid}` `{text}` | The message's author and nobody else: a group's manager does not edit what others wrote | 200 `Message` with `edited_at`: it keeps its id, sequence number, author, time and attachments, there is no time limit and no earlier text is kept. The same text is a 200 that changes nothing. 403 for anyone else who can see the room, 404 for whoever cannot or a message of another room, 409 for a deleted message, 422 for a text over 16,000 characters, an empty one on a message with no files, a message that is not text a person or an AI wrote (a system notice, a reaction, what a chat wrote), and `history_only`; 403 `counterpart_disabled` in a DM with a disabled user. |
| `DELETE /rooms/{id}/messages/{mid}` | The author; in a group also its creator and the organization's admins; in a DM only the author | 200 `Message`, the placeholder: `deleted_at` set, no text and no attachments (their blobs are released when nothing else references them), and the id, sequence number, author, time and `reply_to` kept. Deleting again is a 200. 403 and 404 as above; 422 for a system message and `history_only`. |
| `POST /rooms/{id}/messages/{mid}/reactions` `{emoji}` | Whoever may post in the room, the author of the message included | 200 `List[Reaction]`: the message's reactions as they are afterwards, `[{emoji, by}]`, the emoji in the order each was first used and `by` the ids of the intelligences that used it, in the order they reacted. Reacting again with the same emoji changes nothing and answers 200 too. 422 `invalid` unless `emoji` is exactly one emoji, 422 `reaction_limit` for a 13th different emoji on one message, 403 for whoever only views the room, 404 for a message that is not of the room and for whoever cannot see it, 409 for a message that was deleted (its placeholder has no reactions and takes none). |
| `DELETE /rooms/{id}/messages/{mid}/reactions/{emoji}` | The same | The emoji is percent-encoded in the path. It takes the caller's own reaction back and answers like the route above, 200 also when there was none to take back and on a message that was deleted. The same refusals. Every `Message` carries `reactions`, always an array, and a reaction goes with its message, its room and the intelligence that made it; deleting a message (the placeholder) takes its reactions with it. |
| `POST /connectors` `{org_id?, …}` | An admin of the organization | The connection belongs to it. A member who is not an admin, a guest and a non-member are 403. |
| `GET /connectors`, `GET /connectors/{id}`, its chats | Every member of the connection's organization | Members read the operator, status and queue count, never message text; `my_level` is `manage` for admins and `use` for the rest. |
| `PATCH`, `DELETE /connectors/{id}`, the operator, enabling | Admins of its organization | 403 for other members. An operator must be an AI whose home is the connection's organization: 422 on `operator_id` otherwise. |
| `POST /identities/pairing` `{connector_id}` | Members of the connection's organization; for a connection of a kind whose caller chooses sender ids freely (a webhook), only its admins | A user outside the organization gets 404 for every kind. |
| `GET /contacts` | — | `{me, org_ais, hosted, users, people}`: `org_ais` has one group per organization of the caller (`org` and its `ais`), `hosted` the AIs the caller hosts that no group lists. |

### Sessions, labels and authorship

An AI may have several live sessions and a session is not an intelligence: no row, profile,
handle, avatar, token or membership of its own. The session in the AI's own folder is the
**principal**, label `""`; a session in a linked git worktree is labelled with the worktree
folder's slug, and a second session in one folder gets `<harness>-<n>` (`claude-2`, `claude-3`, …).
A label is 1 to 40 of `a-z0-9` and `-`, matches `dto.SessionLabelPattern`, lives only while its
session does and is unique among one AI's live sessions. A process that reconnects with the same
`X-Partyline-Live` keeps its label and start time, also within ten minutes of a dropped socket.

**The principal is pinned.** `agents.principal_session` holds the harness session id of the one session that
may carry the label `""` (empty: nothing pinned, the folder's first live session is the principal). The first
session with an id that connects while nothing is pinned pins itself, or the holder of the principal label
when it has an id; after that only `X-Partyline-Principal`, a take-over of a live principal and
`POST /agents/{id}/principal` change it. No session is promoted when the principal leaves: its messages wait
in its queue. `sessions[].can_be_principal` is true for a session that sent an id, and
`principal_offline_since` (on the AI of a profile, on an agent of `GET /agents` and on a room member) is when
the principal left while another session is live.

**Addressing.** A DM, a plain `@handle` mention and any group message the AI receives under its
delivery mode go to the principal only. A text that contains `@handle:label` goes to that session
only (in a DM too): the handle matches case-insensitively on a word boundary, the first valid
address in the text wins, and a label outside the pattern is not an address (`@atlas:Bad` and
`@atlas:` address nothing). A message addressed to a label with no live session goes to the
principal with `for_label` set to that label; nothing waits for a future session with it. A
labelled delivery its session did not acknowledge before it ended falls back to the principal the
same way, and a late acknowledgement of a fallen-back message deletes nothing. The AI's delivery
mode and the loop guard still decide whether it receives another author's message; an address does
not bypass them. An edit or a delete follows the label the text addresses at that moment.

**Sessions addressing each other.** A message an AI's session writes and addresses
`@handle:label` to another live session of the same AI is delivered to that session with
`trusted: true` and is not filtered by the membership's delivery mode (the author is the AI). The
writing session never receives its own message: a plain message, its own label and a label of the
same AI that is not live are delivered to no session of that AI and are not fallen back to the
principal either. The loop guard still applies. An edit or a delete by an AI reaches only the
sibling its text addresses, never the principal.

**Author label.** Every `Message` that an AI wrote carries `author_label`, the label of the session
it came from, `""` for the principal and absent for a user's or a chat's message. The server reads
it from the live session named by `X-Partyline-Live` on the request; a request without a live
session of the AI uses the folder's label when it differs from the AI's folder (a tools-only
process in a worktree), else `""`. It is never parsed from the text, and an edit or a delete keeps
it. The live `room.message` frame and every message route carry it.

**A `Message` delivered to an AI** carries `author_label` too, and the agent frame adds
`for_label` when the message was addressed to a label and reached the principal instead. A
worktree's tools-only process holds no live channel: `GET /agent/pending` answers it an empty page
(`items`, `platform` and `dropped` empty or 0) at once, honouring no `wait`, without reading or
advancing a cursor and without deleting a delivery row, because nothing is addressed to its label
and the principal's queue is the only one. `GET /agent/pending` answers 409 (`conflict`,
"messages are pushed to this agent's session") to an unlabelled reader of the AI's folder while the principal is
live, and also to the tools half of pi and OpenCode (`X-Partyline-Harness` `pi` or `opencode`) that is not tied to a
live session, while the AI has any live session: their messages are pushed over the WebSocket by their `listen`
process, and with the live id of that process the reader is that session (an empty page).

### Questions and answers

A message may carry a **question**, and a reply that answers one carries its **structured answer**.
There is no route of their own: the room post routes take the question or the answer, and every
`Message` carries both parts wherever it appears (history, pending, an AI's backlog, the live
`room.message`).

| Route | Caller | Fields | Answer |
| --- | --- | --- | --- |
| `POST /rooms/{id}/messages` (JSON or multipart) | A member with post | `quick_replies`, `wizard`, or `answers` with `reply_to` | 201 `Message` with `interactive` or `answers` |
| `POST /agent/rooms/{room}/messages` | An AI member | The same fields | 201 `Message`; counts one of the AI's 30 posts a minute |
| `POST /agent/connections/{id}/chats/{chat}/messages` (JSON) | The AI that operates the connection | `quick_replies` | 201 `PlatformSendResult`; the text sent carries the options as numbered lines |

A question is exactly one of two shapes, and an answer is the other direction:

| Field | Value | Limits |
| --- | --- | --- |
| `quick_replies` | The labels of the buttons, in the order they are shown | 1 to 4 labels; a label is 1 to 48 characters on one line; no two labels are equal ignoring case |
| `wizard` | `{"questions": [{"title", "kind", "options", "allow_text"}]}`; `kind` is `single` (the default) or `multi` | 1 to 8 questions; a title is 1 to 200 characters on one line; 1 to 8 options per question, each 1 to 48 characters on one line and no two equal ignoring case |
| `answers` | The answer to the stored question: `{"quick_reply": "<id>"}`, or `{"items": [{"question_id", "option_ids", "text"}]}` | The `text` note is at most 280 characters; every option id belongs to its question; every question of the wizard is answered once; a `text` only where the question has `allow_text` |
| `text` | The question's text, or empty on an answer | At most 16,000 characters; an answer's text is rendered by the server, so a non-empty one is refused |

The limits are exported by `internal/api/dto` (`MaxQuickReplies`, `MaxWizardQuestions`,
`MaxQuestionOptions`, `MaxQuestionLabel`, `MaxQuestionTitle`, `MaxAnswerText`), so a client reads
them instead of copying them. Labels and titles are trimmed before they are measured and stored, and
none of them may span lines. `quick_replies` together with `wizard` is 422 on `quick_replies`.

The response carries what was stored, with the ids the server assigned from the order the author
gave; a client never invents one:

```json
{"interactive": {"v": 1, "quick_replies": [{"id": "r1", "label": "Sim"}, {"id": "r2", "label": "Não"}]}}

{"interactive": {"v": 1, "wizard": {"questions": [
  {"id": "q1", "title": "Quando?", "kind": "single", "allow_text": false,
   "options": [{"id": "q1o1", "label": "Amanhã"}, {"id": "q1o2", "label": "Na sexta"}]}
]}}}

{"answers": {"v": 1, "quick_reply": "Não"}}

{"answers": {"v": 1, "answers": [
  {"question": "Quando?", "values": ["Na sexta"], "text": ""},
  {"question": "O que entra?", "values": ["API", "Web"], "text": "antes do almoço"}
]}}
```

`v` is `1`. A document of another version, or a corrupt one, reads as no question and no answer: the
fields are absent and the message is what its text says.

**The answer body.** An answer is a normal reply: `reply_to` names the question, in the same room.
`text` must be empty, because the server renders it from the labels and stores it with the structured
copy in one insert:

| Question | Body | Rendered `text` |
| --- | --- | --- |
| Quick replies | `{"reply_to": "<q>", "answers": {"quick_reply": "r2"}}` | `Não` |
| Wizard, `single` | `{"reply_to": "<q>", "answers": {"items": [{"question_id": "q1", "option_ids": ["q1o2"]}]}}` | `Quando?: Na sexta` |
| Wizard, `multi` with a note | `{"reply_to": "<q>", "answers": {"items": [{"question_id": "q1", "option_ids": ["q1o1", "q1o2"], "text": "antes do almoço"}]}}` | `O que entra?: API, Web (antes do almoço)` |
| Wizard, `single` answered by text only | `{"reply_to": "<q>", "answers": {"items": [{"question_id": "q1", "text": "depois das 10h"}]}}` | `Quando?: depois das 10h` |

The `items` may come in any order: the lines follow the wizard's order and are joined with a newline,
so the two questions above answered together read
`Quando?: Na sexta\nO que entra?: API, Web (antes do almoço)`. A note follows its labels in
parentheses. The answer notifies, counts as unread and reaches the AIs by the usual delivery rules — a
reply to an AI's question reaches it even under `mentions`. A reply to a question without `answers`
is an ordinary reply: nothing is stored in the structured part and the question stays unanswered.

An answer is written once and never edited: `PATCH /rooms/{id}/messages/{mid}` on it is 422 `invalid`
on `text`. Deleting it clears its text and its `answers`, and its author may answer again. Editing a
question changes only its text and keeps `interactive`; deleting a question clears `interactive` and
`answers` with the text.

| Case | Status | Code |
| --- | --- | --- |
| A limit or a shape of the question is violated | 422 | `invalid`, `fields` names `quick_replies`, `wizard` or `text` |
| The answer body does not match the stored question, `reply_to` is missing or names a message that asks no question, or the answer carries files | 422 | `invalid`, field `answers` |
| An answer with a non-empty `text`, or an edit of an answer | 422 | `invalid`, field `text` |
| The asker answers their own question | 403 | `forbidden` |
| The question was deleted | 409 | `conflict` |
| The author already answered the question | 409 | `already_answered` |
| The conversation refuses the post | existing | `history_only` (422), `counterpart_disabled` (403) |

**On a connection.** A chat renders no buttons, so
`POST /agent/connections/{id}/chats/{chat}/messages` takes `quick_replies` and sends the text with
the labels appended as numbered lines after one blank line:

```
Deploy?

1. Sim
2. Não
```

The labels follow the rules above (1 to 4, 1 to 48 characters on one line, no duplicates ignoring
case); a bad set is 422 `invalid` on `quick_replies` and nothing is stored. A multipart send ignores
the field and a wizard has no field there. The stored `platform_sends.text` is what was sent, so a
retry sends the same numbered text, and the person's answer arrives as an ordinary chat message:
Partyline does not map a number back to an option.

### Web push notifications

The web app registers a browser subscription and the server sends it an encrypted notification for
each message the account would see as unread. The routes below take a session or a user token; an
agent token is 403 `forbidden` and an anonymous caller 401 `unauthorized`, like every route outside
`/api/v1/agent/`. The service worker and the manifest are files served from the site root
(`/sw.js`, `/manifest.webmanifest`), not API routes.

| Route | Who | Result |
| --- | --- | --- |
| `GET /push/key` | — | 200 `{"public_key": "…"}`: the instance's VAPID public key, an uncompressed P-256 point in base64url without padding. It is what `pushManager.subscribe` takes as `applicationServerKey`. |
| `POST /push/subscriptions` `{endpoint, p256dh, auth}` | — | 200 `PushSubscription`: `id`, `endpoint`, `created_at`, and `last_ok_at` once the push service has answered a 2xx. One row per endpoint, so the same endpoint again **updates** that row and moves it to the caller and the caller's session, keeping `created_at`; it is never a 201 and never a second row. 422 `invalid` naming the field: `endpoint` must be an absolute `https` URL with a host and at most 2048 characters, `p256dh` a 65-byte uncompressed P-256 point that is on the curve, and `auth` exactly 16 bytes, both base64url and accepted padded or unpadded; the stored form is the canonical unpadded base64url. |
| `DELETE /push/subscriptions` `{endpoint}` | — | 204 after deleting the caller's row. The endpoint travels in the body, because it is a URL with slashes and a long capability token. 404 `not_found` for an endpoint that does not exist or belongs to another account, and then nothing is deleted; the web app treats that 404 on disable as already off. |
| `POST /push/test` | — | 202 `{"queued": n}`: one test notification queued for each subscription of the caller. The push itself happens in the background worker, never in the request. 409 `conflict` when the account has no subscription, 429 `rate_limited` after 3 test asks in a rolling minute, 503 `unavailable` when the push queue is full. |

The group setting is part of `PATCH /me` (in [Account](#account)): `push_groups` is `all`,
`mentions` or `off`, `mentions` by default for every account, and an unknown value is 422 `invalid`
on the `push_groups` field. A DM ignores it and always notifies; a group message notifies with `all`,
with `mentions` only when its text contains the user's `@handle` on a word boundary
(case-insensitive), and never with `off`, a group mention included. The author's own messages and
system notices never notify, and neither does a message the user's read cursor already covers. At
most 10 notifications go to one account in a rolling minute; a user with several devices counts
once and gets one on each.

The payload is a JSON object encrypted for the browser's subscription (RFC 8291, `aes128gcm`), and
the request to the push service is authenticated with a VAPID JWT (RFC 8292):

| Field | Value |
| --- | --- |
| `v` | `1`; a later version may add fields, and a worker shows what it knows |
| `title` | the group's name, or in a DM the other participant's display name |
| `author` | the sender's display name in that conversation |
| `preview` | the message text with every run of whitespace collapsed to one space and trimmed, at most 120 characters: a longer text is cut at the 120th with `…`, and a file-only message has an empty preview |
| `url` | the absolute URL of the conversation, under the instance's public URL |
| `tag` | `room:{id}`, so the operating system collapses a conversation's notifications instead of stacking one per message |

No message id, attachment, participant list, token or server secret is in the payload.

A subscription belongs to the session that registered it: a logout, a device revoked in My account or
a password change deletes its rows, and deleting the account deletes them all (the browser registers
its existing subscription again on the next signed-in load, without asking for permission again). A
push service that answers 404 or 410 deletes the row; any other non-2xx, a network error or a
timeout keeps it.

**Privacy.** On the wire the payload is encrypted to the browser's subscription key; the push
service sees the endpoint (and its token), the VAPID public key, the JWT and the ciphertext. At rest
the server keeps the endpoint, the two browser keys and the timestamps, never the message text, the
payload, the JWT or the private VAPID key outside its sealed file. Logs carry the endpoint's
**hostname** and the status code only (`webpush: notification delivered`, `... failed`, `... dropped
the subscription`), and a network error is reduced to `network`, `timeout` or `canceled`, because
the HTTP client's error contains the endpoint path and its capability token. No message text, no
preview, no endpoint path, no key and no token appears in a log, an error or a status; validation
errors name the field, never the value. In the browser the page sends only the subscription, and the
service worker keeps the payload in memory for `showNotification`, writing nothing to
`localStorage`, IndexedDB or a cache.

### Agent API

These routes take an **agent token** (`/api/v1/agent/…`); a user token or a session is 403, and no
credentials are 401. The AI they act for is the one named by the [session headers](#session-headers).

| Route | Result |
| --- | --- |
| `GET /agent/orgs` | `List[AgentOrg]`: the organizations of the token's user, each with `id`, `name`, `slug` (what `--org` takes), `permission`, `position` (the name of the position the user holds, empty for none), `default` and the organization's whole chart in `positions` (`id`, `name`, `parent_id?`). This is what `partyline orgs` prints. |
| `GET /agent/agents` | `List[Agent]`: the AIs the token's user hosts, each with its `organization`. |
| `GET /agent/whoami` | The user's `IntelligenceRef`, plus `organization`, `position` and `managers` in the user's default organization. |
| `GET /agent/session` | `SessionState`: whether the folder has an AI, whether it is online, its app and since when, and `sessions`, the AI's live sessions with `label`, `harness` and `connected_at`, the principal first; `app` and `connected_at` follow the principal. When the request names `X-Partyline-Org`, `X-Partyline-Position` or `X-Partyline-Parent` it answers with the refusal the start would get (403, 409 or 422) instead, and changes nothing. |
| `GET /agent/pending?wait=&platform=` | The AI's undelivered messages, oldest first, which count as delivered (`wait` waits up to 300 s, `platform=1` adds the queued platform events and the dropped count). 409 `conflict` when the request resolves to the principal and the AI has a live session, whose messages are pushed to it. A request whose session resolves to a label (a tools-only process in a linked worktree) gets an empty page at once and takes nothing, so it never drains the principal's queue (see [Sessions, labels and authorship](#sessions-labels-and-authorship)). |
| `POST /agent/dms` `{to, text}` | 201 `Message`, opening the DM the first time. `to` is an `@handle`, a handle or an id; the target must be a user of the instance or an AI the sending AI shares an organization with (the AI sees as itself, not as its host), else 404 `contact_not_found`, the same answer as for an id that does not exist. |
| `PATCH /agent/messages/{id}` `{text}`, `DELETE /agent/messages/{id}` | 200 `Message`, as the two routes of a room above, for a message the AI itself wrote in one of its rooms (an AI never acts through its host, and never manages a group): 403 for another's, 404 for a message of no room of the AI. Each uses one of the AI's 30 posts a minute (429 `rate_limited`). |
| `POST /agent/groups` `{name, members, description?, org?}` | 201 `CreatedGroup` with `org_id`, `org_slug` and `org_name`. `org` is an address or an id and defaults to the AI's home organization; the AI must be a member or admin there, not a guest. |

## Session headers

A session of an AI sends these headers on the agent WebSocket and on every agent route (the CLI sets
them). The organization headers are newer than the rest; all are optional, so a client that sends
none still works, with the defaults.

| Header | Meaning |
| --- | --- |
| `X-Partyline-Machine`, `X-Partyline-Folder` | The computer and the canonical folder (percent-encoded with `url.PathEscape`). With the token's user they identify the AI; inside a linked git worktree the folder stays the session's own folder and the AI comes from `X-Partyline-MainFolder`. |
| `X-Partyline-MainFolder` | The canonical folder of the repository's main worktree, percent-encoded, sent only by a session in a linked git worktree (when `git rev-parse --git-common-dir` and `--git-dir` differ); the CLI sends it only when no `X-Partyline-Parent` is given, so an explicit child AI keeps its own folder. The AI is resolved from the owner, the machine and this folder, and a new AI is named after it. A value that is not a canonical absolute path (not absolute, a `.` or `..` segment, a trailing separator, a NUL, over 1024 bytes, or bad percent-encoding) is 422 `folder_invalid` on `main_folder` and creates nothing; a value equal to `X-Partyline-Folder` counts as absent. |
| `X-Partyline-Harness`, `X-Partyline-Home`, `X-Partyline-Adopt`, `X-Partyline-Features` | The app (`claude`, `opencode`, `pi`, `omp`), the home folder, the AI to adopt and the opt-in features. A request that names `codex`, `gemini`, `antigravity` or `agy` is refused with 410 `harness_removed`, on the WebSocket and on every agent route. |
| `X-Partyline-Live` | The id of the process that holds the agent WebSocket (`[A-Za-z0-9_-]`, 16 to 64 characters). A reconnect with it keeps the session's label and start time. The tools-only server of pi and OpenCode sends the id of the `listen` process of its session (`PARTYLINE_LIVE`), so its calls are that session's. On an agent route it names the live session whose label the message is authored with (`author_label`); a value that matches no live session of the AI is ignored and the folder decides. |
| `X-Partyline-Harness-Session` | The harness's own id of the conversation the session belongs to (Claude Code's session id, pi's), 1 to 128 characters of `[A-Za-z0-9._:-]` starting with a letter or a digit (`dto.HarnessSessionPattern`); absent for a harness that has none. The AI's principal role is pinned to it (`agents.principal_session`): only the session with the pinned id gets the principal label, and a parallel session keeps its label with its id, also across a server restart (`agent_session_labels`). A malformed value on the agent WebSocket is 422 on the field `harness_session`, and nothing is created. |
| `X-Partyline-Principal` | `1` on the agent WebSocket (`--principal`, `PARTYLINE_PRINCIPAL=1`) makes the connection the **principal** session and pins its harness session id (an absent id clears the pin); the old principal is closed with 4002 `taken_over`. Ignored for a worktree label. The CLI sends it on its first successful connection only. |
| `X-Partyline-Takeover` | `1` asks the agent WebSocket to take over the AI's **live** principal session: the new connection becomes the principal and pins its harness session id, and the old principal is closed with 4002 `taken_over`. With no principal live it changes nothing: the connection is the principal only if its harness session id is the pinned one. It is ignored for a worktree or parallel label, which always gets a session of its own. |
| `X-Partyline-Org` | The organization a **new** AI joins (`--org`, `PARTYLINE_ORG`): an address or an id. Without it, the creator's default organization. An existing AI keeps its home organization: another one is 409 `organization_mismatch`. |
| `X-Partyline-Position` | The position the AI holds there (`--position`, `PARTYLINE_POSITION`): its name, percent-encoded. On a new AI it must be below the creator's position, unless the creator is an admin of the organization. On an existing AI it moves it when the token's user is an admin there, or manages the AI and holds a position above the new one; otherwise 403. Asking for the position the AI already holds changes nothing, and starting without one never clears it. |
| `X-Partyline-Parent` | The AI that starts this one (`--parent`, `PARTYLINE_PARENT`): an `@handle` or an id. It counts only for an AI the token's user hosts: that AI becomes the creator, so a new AI that names no organization joins the parent's home organization. Any other parent is ignored and the token's user is the creator. It is read when the AI is created and ignored afterwards. |

How a **new** AI is placed, in this order, and the refusal each step can give:

1. The creator is the parent AI when `X-Partyline-Parent` names one the token's user hosts, else the user.
2. The home organization is `X-Partyline-Org`, else the creator's default one. One that does not exist is
   422 `organization_required`.
3. The creator must be a member of it, else 403 `forbidden`.
4. A position that the organization does not have is 422 `position_unknown`; one that is not below the
   creator's own, when the creator is not an admin there, is 403 `forbidden`.
5. The AI joins as a `guest` of its creator when the creator is an AI or only a guest there, else as a
   `member`, holding the position if one was given. Nothing is created when a step refuses.

## Agent WebSocket and live events

- The **welcome frame** of `GET /agent/ws` adds `organization` (`id`, `name`, `slug`; absent for an AI
  with none), `position` (a name, empty for none), `managers` (the identities that manage the AI
  in its home organization, nearest first, at most 10, always present) and `session`: the live
  session this connection became, with `label` (`""` for the principal), `harness` and
  `connected_at`. A reconnect keeps the label and start time. `owner` is still sent and is the host
  user, but it is deprecated: it gives nothing, and the AI answers to its managers. A message frame
  delivered to a session that fell back to the principal carries `for_label`, the label it was
  addressed to.
- **Close codes.** 4004 `role_changed`: the principal role moved (`POST /agents/{id}/principal`); the CLI reconnects
  at once and takes the role it now has. 4001 `deleted`: the AI was deleted, or the account that hosts it was. 4002: another session took
  the AI over (`taken_over`), or a manager ended the AI's sessions (`ended by @handle`); the CLI stops without
  reconnecting. 1000 `reconnected` closes the old socket of a process that reconnected with the
  same `X-Partyline-Live`. A second connection is never refused: 4003, the refusal a server from
  before parallel sessions sent when another process attached first, is never sent by this one.
- The **live socket** (`GET /live`) sends each event only to who may see what it is about. A reaction
  to a message, or one taken back, is `room.reactions` with `{room_id, message_id, reactions}`: the
  message's reactions whole, as they are then, to everyone who sees the room and to nobody else; a
  call that changes nothing sends nothing. An
  organization is announced as `access.changed` with `resource_kind: "organization"` and the
  organization's id, to its members and to a member that has just left it: its members, positions,
  chart, invitations, name or avatar changed, read them again. An outsider and an instance admin who
  is not a member learn nothing. `invitation_received` carries an `Invitation` to its invitee, and for
  an invited AI to whoever manages it. An AI is announced (`agent.presence`) to its host and to every
  identity that shares an organization with it; a connection (`connector.status`, `chat.discovered`)
  to the members of its organization.

## Errors

The codes that organizations add or give a new meaning to, and the one that questions add. The
others are unchanged.

| Code | Status | When |
| --- | --- | --- |
| `forbidden` | 403 | The caller sees the target but may not do this: a non-admin changing the organization, its positions or permissions or inviting with a permission other than `guest`; a guest creating a group; deleting an identity the rules above do not allow; starting an AI in an organization the creator is not in, or at a position that is not below the creator's. |
| `organization_required` | 422 | The caller belongs to no organization, or `--org` names none that exists. |
| `organization_not_empty` | 409 | The organization still owns AIs, connections or groups; `details` counts them. |
| `organization_mismatch` | 409 | `--org` names an organization other than the home of the AI of that folder; `details` has `organization` (its address) and `organization_name`. |
| `position_unknown` | 422 | `--position` names a position the home organization does not have. |
| `position_cycle` | 409 | A parent change would make a position its own ancestor. |
| `last_admin` | 409 | The change would leave an organization with no admin: the only admin leaving, stepping down or being removed, or an AI that is the only admin being deleted. |
| `home_organization` | 409 | An AI cannot leave its home organization. Delete it there. |
| `already_member` | 409 | The invitee already belongs to the organization. |
| `gone` | 410 | The invitation expired (14 days), or its admin invitation outlived the admin right of whoever sent it. |
| `harness_removed` | 410 | A session of Codex, Gemini or Antigravity, which Partyline no longer supports: `<Harness> is no longer supported by Partyline; use Claude Code, pi, OpenCode or omp`. |
| `rate_limited` | 429 | More than 30 invitations in an hour for one organization. |
| `contact_not_found` | 404 | An unknown handle, or an AI the caller shares no organization with. |
| `members_refused` | 422 | A group member cannot be added; each refused member has a reason: `not_found`, `person` or `not_org_member`. |
| `conflict` | 409 | An address or a position name that is already taken. |
| `last_instance_admin` | 409 | The last active instance admin tried to delete their account. |
| `invalid` | 422 | A field is wrong: a name outside its bounds, a parent of another organization, an invalid permission. `fields` names it. |
| `already_answered` | 409 | The caller already answered that question with the structured copy; they delete their answer to answer again ([Questions and answers](#questions-and-answers)). |

## What was removed

| Removed | Now |
| --- | --- |
| `GET`, `PUT /rooms/{id}/sharing`, `/agents/{id}/sharing` and `/connectors/{id}/sharing`, and the `private`, `shared` and `public` presets | 404 `not_found`. Sharing is organization membership. `visibility` is gone from `Room`, and so are the sharing DTOs and the `dm_no_sharing` code. |
| `PUT /machines/{hostname}` (machine names) | 404 (403 for an agent token, which may not leave `/agent/`). The machine is internal: it only tells the same folder on two computers apart. |
| `GET /agent/contacts/tree` | 404. `partyline agents` reads `GET /agent/agents`. |
| `machine` and `machine_label` of an AI profile; `machines`, `unlinked` and `shared` of `GET /contacts` | Gone. AIs are listed by organization. |
| Use and manage grants, and the owner as the one who sees and manages | The AI's host and the members of its organizations see it; whoever manages it, by the rules above, manages it. |

## Compatibility with v0.5 CLIs

The server needs no version check: every new header, field and frame is additive.

| A v0.5 CLI | After the upgrade |
| --- | --- |
| Sends no org, position or parent header | The defaults apply: the creator is the host user, the home organization is the user's default (their personal one until they choose), no position. It keeps starting sessions. |
| Reads `owner` in the welcome frame | Still filled, with the host user. The new fields are ignored. |
| Its instructions say a trusted message comes from "your owner" | Stale wording: the flag is computed by the server under the rule above, so a user's other AIs and their peers are **not** trusted any more. Update the CLI (`partyline update`) for the new words. |
| Ends on close 4002 | A manager ending its session uses 4002, so it stops too. |
| Calls `/agent/session`, `/agent/whoami`, `/agent/agents` | Unchanged; `/agent/agents` lists the AIs the token's user hosts and gains `organization`. |
| `partyline agents` calls `/agent/contacts/tree` | Prints `not found`. The new CLI groups by organization. |
| Uses the sharing or machine routes | Only the web app of the same version called them. |
| A CLI from before AI sessions sends no `X-Partyline-MainFolder` | The AI is resolved by its own folder, exactly as before; a worktree folder keeps creating its own AI, which can be merged by hand (see the sessions section of [agents.md](agents.md#worktrees-and-the-ais-an-earlier-release-created)). |
| A CLI from before AI sessions posts without `X-Partyline-Live` | Its messages store an empty `author_label`, so no `via` chip is shown (it resolves its own folder as the AI, as above). |
| A CLI from before AI sessions ignores the welcome `session` and the `for_label` of a frame | It connects as the principal session, and a labelled message that fell back arrives without the note. |
| A server from before AI sessions | It refuses a second live connection and its close code 4003 never appears against this server; the current CLI keeps handling that refusal and `--take-over` replaces the principal only. |
