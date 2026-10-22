import type {
  Agent,
  Chat,
  Connector,
  ConnectorKind,
  Me,
  Message,
  Room,
  RoomMember,
} from "@/lib/api/types.gen";

/** Synthetic fixtures; never real names, numbers or chat ids. */
export function makeMessage(over: Partial<Message> = {}): Message {
  const seq = over.seq ?? 1;
  return {
    id: `m-${over.room_id ?? "r1"}-${seq}`,
    room_id: "r1",
    seq,
    author_kind: "user",
    author_id: "u1",
    author_name: "Ana",
    kind: "text",
    text: `message ${seq}`,
    attachments: [],
    created_at: "2026-10-19T12:00:00Z",
    ...over,
  };
}

export function makeMe(over: Partial<Me> = {}): Me {
  return {
    id: "u1",
    username: "ana",
    display_name: "Ana Souza",
    intelligence: {
      id: "u1",
      kind: "human",
      role: "user",
      display_name: "Ana Souza",
      handle: "ana",
    },
    role: "member",
    locale: "en",
    created_at: "2026-10-01T12:00:00Z",
    ...over,
  };
}

export function makeRoom(over: Partial<Room> = {}): Room {
  return {
    id: "r1",
    name: "general",
    description: "",
    instructions: "",
    owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
    visibility: "private",
    my_level: "manage",
    bridge_chats: false,
    broadcast: true,
    catch_all: false,
    retention_days: 90,
    agents_paused: false,
    last_seq: 0,
    member_count: 0,
    created_at: "2026-10-01T12:00:00Z",
    updated_at: "2026-10-19T12:00:00Z",
    ...over,
  };
}

export function makeMember(over: Partial<RoomMember> = {}): RoomMember {
  return {
    id: "mb1",
    kind: "agent",
    member_id: "a1",
    name: "release-bot",
    outbound: true,
    online: false,
    owner: "ana",
    display_name: "",
    sign_outbound: true,
    delivery: "all",
    keywords: [],
    created_at: "2026-10-01T12:00:00Z",
    ...over,
  };
}

export function makeAgent(over: Partial<Agent> = {}): Agent {
  const name = over.name ?? "release-bot";
  return {
    id: "a1",
    name,
    display_name: name,
    description: "",
    harness: "agent",
    machine: "",
    kind: "fixed",
    archived: false,
    owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
    my_level: "manage",
    online: false,
    created_at: "2026-10-01T12:00:00Z",
    ...over,
  };
}

export function makeChat(over: Partial<Chat> = {}): Chat {
  return {
    id: "c1",
    connector_id: "k1",
    connector_kind: "telegram",
    connector_name: "ops-bot",
    external_id: "-1001",
    title: "Ops on call",
    kind: "group",
    created_at: "2026-10-01T12:00:00Z",
    ...over,
  };
}

export function makeConnector(over: Partial<Connector> = {}): Connector {
  return {
    id: "k1",
    kind: "telegram",
    name: "ops-bot",
    owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
    my_level: "manage",
    enabled: true,
    status: "connected",
    config: {},
    auto_route_scope: "off",
    created_at: "2026-10-01T12:00:00Z",
    updated_at: "2026-10-19T12:00:00Z",
    ...over,
  };
}

/** A synthetic connector kind with one field of every type. */
export function makeConnectorKind(over: Partial<ConnectorKind> = {}): ConnectorKind {
  return {
    name: "telegram",
    label: "Telegram",
    can_list_chats: true,
    fields: [
      { name: "bot_token", label: "Bot token", type: "secret", required: true, secret: true },
      { name: "api_url", label: "API URL", type: "url", required: false, secret: false },
      {
        name: "greeting",
        label: "Greeting",
        type: "string",
        required: false,
        secret: false,
        help: "Sent when the bot joins a group.",
        default: "hello",
      },
      { name: "groups_only", label: "Groups only", type: "bool", required: false, secret: false },
      {
        name: "allowed_chats",
        label: "Allowed chats",
        type: "string_list",
        required: false,
        secret: false,
      },
    ],
    ...over,
  };
}
