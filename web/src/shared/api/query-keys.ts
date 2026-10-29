import type { FileFilter } from "./cache-types";

/*
 * Query keys and hooks for every API resource. Naming: `useX` reads, `useVerbX` mutates;
 * `xQuery(...)` returns options for loaders (`queryClient.ensureQueryData`). Collections are
 * cached as plain arrays (List.items unwrapped); message pages keep their Page shape.
 */

export const queryKeys = {
  me: ["me"] as const,
  setupStatus: ["setup-status"] as const,
  invite: (token: string) => ["invite", token] as const,
  users: ["users"] as const,
  sessions: ["me", "sessions"] as const,
  /** The instance's VAPID public key; read when the user enables notifications (PUSH-02). */
  pushKey: ["push", "key"] as const,
  tokens: ["tokens"] as const,
  rooms: ["rooms"] as const,
  conversations: ["conversations"] as const,
  contacts: ["contacts"] as const,
  /** The people one connector has seen, filtered by a search text ("" = all of them). */
  people: (connectorId: string, q: string) => ["contacts", "people", connectorId, q] as const,
  intelligence: (id: string) => ["intelligences", id] as const,
  /** The viewer's organizations; every query of one organization is under `org(id)`. */
  orgs: ["orgs"] as const,
  org: (id: string) => ["orgs", id] as const,
  orgMembers: (id: string) => ["orgs", id, "members"] as const,
  /** What moving a member to one position would change about who manages it (POS-04). */
  orgMovePreview: (id: string, iid: string, positionId: string) =>
    ["orgs", id, "members", iid, "move-preview", positionId] as const,
  orgChart: (id: string) => ["orgs", id, "chart"] as const,
  orgInvitations: (id: string) => ["orgs", id, "invitations"] as const,
  /** The invitations waiting for the viewer, or for an AI they manage. */
  invitations: ["invitations"] as const,
  room: (id: string) => ["rooms", id] as const,
  roomMembers: (id: string) => ["rooms", id, "members"] as const,
  roomMessages: (id: string) => ["rooms", id, "messages"] as const,
  roomFiles: (id: string, kind: FileFilter) => ["rooms", id, "files", kind] as const,
  agents: ["agents"] as const,
  connectorKinds: ["connector-kinds"] as const,
  connectors: ["connectors"] as const,
  connector: (id: string) => ["connectors", id] as const,
  connectorChats: (id: string) => ["connectors", id, "chats"] as const,
  /** The owner's servers that run container AIs (CA-01). */
  runners: ["runners"] as const,
  /** The owner's named secrets for container AIs; names only, never a value (CA-03). */
  secrets: ["secrets"] as const,
  /** The container of one AI (CA-06), and its last log lines. */
  container: (agentId: string) => ["agents", agentId, "container"] as const,
  containerLogs: (agentId: string) => ["agents", agentId, "container", "logs"] as const,
  identities: ["identities"] as const,
  adminUsers: ["admin", "users"] as const,
  adminInvites: ["admin", "invites"] as const,
  adminAudit: ["admin", "audit"] as const,
  activity: ["activity"] as const,
};
