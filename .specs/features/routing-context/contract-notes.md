# Contract Notes (from routing-context Phases 1-2: T1-T8)

Binding for Phase 3 (channel and reactions, T9-T12), Phase 4 (web, T13-T16) and the Verifier. Types
live in `internal/api/dto` (AD-003); `web/src/lib/api/types.gen.ts` is regenerated.

## Migrations
- `0006_routing_context.sql` (member settings, `rooms.instructions`, `connectors.auto_route_*`,
  `auto_route_skips`) and `0007_agent_display_names.sql` (recreates `message_views`). The next
  migration is `0008`.

## DTO
- `RoomMember` always carries `display_name` (string), `sign_outbound` (bool), `delivery`
  (`DeliveryMode`: `DeliveryModeAll` "all" | `DeliveryModeMentions` "mentions" |
  `DeliveryModeTrusted` "trusted") and `keywords` (always an array, `[]` when none). Non-agent
  members carry the defaults (`""`, `true`, `"all"`, `[]`).
- `UpdateMemberRequest` adds optional `display_name`, `sign_outbound`, `delivery`, `keywords`
  (`*[]string`: `[]` clears, `""` clears the display name; values are trimmed). Limits
  `MaxMemberDisplayName` 64, `MaxMemberKeywords` 20, `MaxMemberKeyword` 64, counted in code points.
  422 fields/reasons: `display_name` "must have at most 64 characters", `keywords` "each keyword must
  have 1 to 64 characters" / "at most 20 keywords", `delivery` "must be all, mentions or trusted";
  any of the four on a chat or user member → 422 on that field "only for agent members". A rejected
  PATCH applies none of its fields. Viewer 403, no access 404 (manage required).
- `Room.instructions` (string, always present); `CreateRoomRequest.instructions` and
  `UpdateRoomRequest.instructions` (optional); `MaxRoomInstructions` 4000 code points, 422 field
  `instructions` "must have at most 4000 characters".
- `Connector.auto_route_scope` (`AutoRouteScope`: `AutoRouteScopeOff` "off" | `AutoRouteScopeDMs`
  "dms" | `AutoRouteScopeGroups` "groups" | `AutoRouteScopeAll` "all"; always present) and
  `auto_route_room_id` (omitted when unset). `UpdateConnectorRequest` adds both as optional;
  `auto_route_room_id: ""` clears the room. Errors: 422 `auto_route_scope` (outside the enum),
  422 `auto_route_room_id` "unknown room" or "required when the scope is not off"; 403 `forbidden`
  when the caller lacks manage on the room. The room check runs every time a non-empty room id is
  sent (also when unchanged); a grant of manage counts, not only ownership. Changes are audited as
  `connector.update` with `changed` containing `"auto_route"`.
- `ChatRef{id, title, kind, platform}` and `Message.chat *ChatRef` (`chat`, omitempty) are defined
  but **not filled yet**: T9 fills them (platform = connector kind name). `AgentMessage` embeds
  `Message`, so it gets `chat` from there.
- `RoomInfo{room: RoomRef, instructions, members: RoomMember[], me: RoomMember}` and
  `ReactRequest{emoji}` are defined for T10/T11; no route serves them yet.

## Delivery filter (MEM-03, MEM-04)
- `delivery.Wants(MemberPrefs, Delivered)` (pure), `delivery.Prefs(agentName, displayName,
  delivery, keywordsJSON)`, `delivery.LoadFacts(ctx, q, db.MessageView)` and
  `Facts.WantedBy(agentID, ownerID, prefs)`. Mentions = `@<display name>` or `@<agent name>`
  (case-insensitive) and keywords, both with no letter/digit/underscore right before or after;
  every message of a `dm` chat; replies whose target was authored by this agent. `trusted` = only
  `delivery.Trusted` messages. Empty mode = all.
- `AgentDispatcher` skips unwanted pushes; the WS backlog leaves them out. Backlog cursor rule:
  unwanted messages before the first delivered one advance the cursor (before that message is
  written); later unwanted ones wait for the next ack. `ListAgentRoomStates` now returns the
  member's `display_name`, `delivery`, `keywords_json`.
- History (`GET /agent/rooms/{room}/messages`) and the web timeline are unfiltered.
- The skipped-count notice (AGT-09) still counts every pending message, wanted or not.

## Author names (MEM-01, MEM-02)
- `message_views.author_name` for agent authors is the member's `display_name` in the message's
  room when set, else the agent name. This applies everywhere a message is rendered (timeline,
  live, agent pushes, history, post responses).
- `connectors.Sender` sends `Outbound.AuthorName = ""` when the authoring agent member has
  `sign_outbound` off; adapters already omit the header for an empty name. Users unchanged.
- Query `GetRoomMemberByMember(room_id, kind, member_id)`.

## Catch-all routing (ROUTE-01..05)
- In the inbound pipeline, after the chat upsert: a message with text or media from a chat in no
  room is routed when the connector's scope matches the chat kind (`dms` ↔ `dm`; `groups` ↔
  `group`/`channel`; `all`), no unexpired `auto_route_skips` row exists, and
  `rooms.AddMember` succeeds as the connector owner (outbound on; `ErrAlreadyMember` from a
  concurrent first message counts as success). Trust is unchanged (verified owner identities are
  trusted, everyone else is not).
- Unavailable room (deleted → `auto_route_room_id` NULL by FK, or owner without manage): nothing is
  stored, the connector's `last_error` becomes `connectors.AutoRouteUnavailable`
  ("auto-route room unavailable") and a `connector.status` event is published with the current
  status (the status itself is not changed; the next status report from the manager, e.g. a
  reconnect, clears the detail and the next routed message sets it again).
- `rooms.RemoveMember` of a chat from its connector's catch-all room writes a skip until
  `now + rooms.AutoRouteSkip` (24 h). Manual `AddMember` ignores skips.

## Chat titles (CTX-05..08)
- WhatsApp: group messages without a title get the subject from `POST /group/info`
  `{"groupJid": jid}` with the instance token (`data.Name`), cached 10 minutes per chat id; the
  `/group/list` refresh fills the same cache. A failed call leaves the title empty (stored title
  kept) and logs a warning. Supersedes the connectors note "ChatTitle is empty for groups".
- WhatsApp DMs: title = trimmed push name, else the phone number (user part of the first
  `@s.whatsapp.net` JID among Chat, ChatAlt, Sender, SenderAlt; else the chat JID's user part).
- Telegram titles: title, else first + last name, else `@username` (unchanged, now tested).
- `UpsertChat` keeps updating a non-empty changed title (CTX-08).
- `POST /chats/{id}/route` names the room after the trimmed title, else
  `connectors.PlatformLabel(kind) + " chat"` (WhatsApp, Telegram, Discord, Webhook; other kinds
  capitalized), cut to 100 characters. Web T16 should use the same labels for untitled chats.

## Test helpers
- api tests: `agentMember`, `setMember` (PATCH an agent member), `(*agentRoom).roomChat(kind)`,
  `(*agentRoom).postFromChat`, `newSettingsRoom`, `newCatchAllEnv`.
- connectors tests: `newCatchAll` (connected agent "helper" in room "catch-all"), `dmText`,
  `channelText`, `(*outboundEnv).patchAgentMember`, `agentPost`.
- whatsapp tests: `fakeEvolution.setGroup(name, failStatus)`, `count(path)`; `/group/info` answers
  the captured 0.7.2 shape with synthetic values.
- Web fixtures: `makeRoom` has `instructions: ""`, `makeMember` the four settings,
  `makeConnector` `auto_route_scope: "off"`.
