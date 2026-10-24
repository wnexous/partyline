import type {
  Agent,
  AIContact,
  Chat,
  Connector,
  ConnectorKind,
  Contacts,
  Conversation,
  IntelligenceProfile,
  IntelligenceRef,
  Me,
  Message,
  OrgAIs,
  Room,
  RoomMember,
} from "@/lib/api/types.gen";

/** Synthetic fixtures; never real names, numbers or chat ids. */

/** An intelligence summary; a user ("Ana Souza") unless overridden (`kind: "ai"`, `role: "ai"`…). */
export function makeIntelligence(over: Partial<IntelligenceRef> = {}): IntelligenceRef {
  return {
    id: "u1",
    kind: "human",
    role: "user",
    display_name: "Ana Souza",
    handle: "ana",
    ...over,
  };
}

/** An AI's summary: "Atlas" (@atlas) unless overridden. */
export function makeAI(over: Partial<IntelligenceRef> = {}): IntelligenceRef {
  return makeIntelligence({
    id: "a1",
    kind: "ai",
    role: "ai",
    display_name: "Atlas",
    handle: "atlas",
    ...over,
  });
}

/** An external person: "Lia Costa" (@lia) on WhatsApp unless overridden. */
export function makePerson(over: Partial<IntelligenceRef> = {}): IntelligenceRef {
  return makeIntelligence({
    id: "p1",
    role: "person",
    platform: "whatsapp",
    display_name: "Lia Costa",
    handle: "lia",
    ...over,
  });
}

/** A conversation list row: a quiet group with no messages unless overridden. */
export function makeConversation(over: Partial<Conversation> = {}): Conversation {
  return {
    id: "r1",
    kind: "group",
    title: "general",
    my_level: "manage",
    unread: 0,
    activity_at: "2026-10-19T12:00:00Z",
    member_names: [],
    ...over,
  };
}

export function makeMessage(over: Partial<Message> = {}): Message {
  const seq = over.seq ?? 1;
  return {
    id: `m-${over.room_id ?? "r1"}-${seq}`,
    room_id: "r1",
    seq,
    author_kind: "user",
    author_id: "u1",
    author_name: "Ana",
    author: { id: "u1", kind: "human", role: "user", display_name: "Ana", handle: "ana" },
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
    kind: "group",
    participants: [],
    name: "general",
    description: "",
    instructions: "",
    owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
    visibility: "private",
    my_level: "manage",
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
    online: false,
    owner: "ana",
    display_name: "",
    delivery: "all",
    keywords: [],
    created_at: "2026-10-01T12:00:00Z",
    ...over,
  };
}

/**
 * An AI of the agents list. `name` is shorthand for its profile: the display name and the @handle
 * (an `intelligence` override wins).
 */
export function makeAgent(over: Partial<Agent> & { name?: string } = {}): Agent {
  const { name = "release-bot", ...agent } = over;
  const id = agent.id ?? "a1";
  return {
    id,
    intelligence: makeAI({ id, display_name: name, handle: name }),
    description: "",
    harness: "agent",
    machine: "",
    owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
    my_level: "manage",
    online: false,
    created_at: "2026-10-01T12:00:00Z",
    ...agent,
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
    org_id: "o1",
    owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
    my_level: "manage",
    enabled: true,
    status: "connected",
    deliver: "all",
    groups_mentions_only: true,
    queued: 0,
    config: {},
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

/** An AI as the contacts page lists it: Atlas, offline, in ~/atlas, home organization o1, unless overridden. */
export function makeAIContact(over: Partial<AIContact> = {}): AIContact {
  return {
    intelligence: makeAI(),
    org_id: "o1",
    folder: "/home/dev/atlas",
    folder_label: "~/atlas",
    app: "claude",
    online: false,
    ...over,
  };
}

/** The AIs of one organization of the contacts: Acme (o1), without AIs unless overridden. */
export function makeOrgAIs(over: Partial<OrgAIs> = {}): OrgAIs {
  return { org: { id: "o1", name: "Acme", slug: "acme" }, ais: [], ...over };
}

/** GET /contacts for the default user: nothing to show until a test adds it. */
export function makeContacts(over: Partial<Contacts> = {}): Contacts {
  return {
    me: makeMe().intelligence,
    org_ais: [],
    hosted: [],
    users: [],
    people: [],
    ...over,
  };
}

/** GET /intelligences/{id} of a user, read-only; spread an AI or a person summary over it. */
export function makeProfile(over: Partial<IntelligenceProfile> = {}): IntelligenceProfile {
  return {
    ...makeIntelligence(),
    bio: "",
    can_edit: false,
    apps: [],
    conversations: [],
    memberships: [],
    can_end_session: false,
    ...over,
  };
}
