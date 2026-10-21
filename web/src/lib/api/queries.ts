import {
  type InfiniteData,
  infiniteQueryOptions,
  type QueryClient,
  queryOptions,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "./client";
import {
  type AcceptInviteRequest,
  type ActivityItem,
  type AddMemberRequest,
  type AdminUser,
  type Agent,
  type AuditEntry,
  type ChangePasswordRequest,
  type Chat,
  type Connector,
  type ConnectorKind,
  type CreateConnectorRequest,
  type CreatedInvite,
  type CreatedToken,
  type CreateInviteRequest,
  type CreateRoomRequest,
  type CreateTokenRequest,
  DefaultActivityLimit,
  DefaultPageSize,
  type DeviceRequest,
  type FileKind,
  FileKindDocument,
  type Identity,
  type Invite,
  type InviteInfo,
  type List,
  type LoginRequest,
  type Me,
  type Message,
  MultipartFileField,
  MultipartMessageField,
  type Page,
  type PairingCode,
  type PairingRequest,
  type PostMessageRequest,
  type Room,
  type RoomFile,
  type RoomMember,
  type Session,
  type SetupRequest,
  type SetupStatus,
  type Sharing,
  type Token,
  type UpdateAgentRequest,
  type UpdateConnectorRequest,
  type UpdateMemberRequest,
  type UpdateMeRequest,
  type UpdateRoomRequest,
  type UpdateUserRequest,
  type User,
} from "./types.gen";

/*
 * Query keys and hooks for every API resource. Naming: `useX` reads, `useVerbX` mutates;
 * `xQuery(...)` returns options for loaders (`queryClient.ensureQueryData`). Collections are
 * cached as plain arrays (List.items unwrapped); message pages keep their Page shape.
 */

export type SharingResource = "rooms" | "agents" | "connectors";

export const queryKeys = {
  me: ["me"] as const,
  setupStatus: ["setup-status"] as const,
  invite: (token: string) => ["invite", token] as const,
  users: ["users"] as const,
  sessions: ["me", "sessions"] as const,
  tokens: ["tokens"] as const,
  rooms: ["rooms"] as const,
  room: (id: string) => ["rooms", id] as const,
  roomMembers: (id: string) => ["rooms", id, "members"] as const,
  roomMessages: (id: string) => ["rooms", id, "messages"] as const,
  roomFiles: (id: string, kind: FileFilter) => ["rooms", id, "files", kind] as const,
  sharing: (kind: SharingResource, id: string) => [kind, id, "sharing"] as const,
  agents: ["agents"] as const,
  connectorKinds: ["connector-kinds"] as const,
  connectors: ["connectors"] as const,
  connector: (id: string) => ["connectors", id] as const,
  connectorChats: (id: string) => ["connectors", id, "chats"] as const,
  chats: ["chats"] as const,
  identities: ["identities"] as const,
  adminUsers: ["admin", "users"] as const,
  adminInvites: ["admin", "invites"] as const,
  adminAudit: ["admin", "audit"] as const,
  activity: ["activity"] as const,
};

const items = <T>(list: List<T>) => list.items;

// ---------------------------------------------------------------------------------------------
// Session, setup and invites

export const meQuery = () =>
  queryOptions({
    queryKey: queryKeys.me,
    queryFn: () => api<Me>("GET", "/me", undefined, { skipAuthRedirect: true }),
    staleTime: 60_000,
    retry: false,
  });

export const setupStatusQuery = () =>
  queryOptions({
    queryKey: queryKeys.setupStatus,
    queryFn: () => api<SetupStatus>("GET", "/setup/status"),
    staleTime: 0,
  });

export const inviteQuery = (token: string) =>
  queryOptions({
    queryKey: queryKeys.invite(token),
    queryFn: () => api<InviteInfo>("GET", `/invites/${encodeURIComponent(token)}`),
    retry: false,
  });

export const useMe = () => useQuery(meQuery());
export const useSetupStatus = () => useQuery(setupStatusQuery());
export const useInviteInfo = (token: string) => useQuery(inviteQuery(token));

/** Stores the logged-in user and drops every cached resource of a previous session. */
function startSession(qc: QueryClient, me: Me) {
  qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
  qc.setQueryData(queryKeys.me, me);
}

export function useSetup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: SetupRequest) => api<Me>("POST", "/setup", req, { skipAuthRedirect: true }),
    onSuccess: (me) => {
      qc.setQueryData<SetupStatus>(queryKeys.setupStatus, { needs_setup: false });
      startSession(qc, me);
    },
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: LoginRequest) =>
      api<Me>("POST", "/auth/login", req, { skipAuthRedirect: true }),
    onSuccess: (me) => startSession(qc, me),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("POST", "/auth/logout", undefined, { skipAuthRedirect: true }),
    onSettled: () => qc.clear(),
  });
}

export function useAcceptInvite(token: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: AcceptInviteRequest) =>
      api<Me>("POST", `/invites/${encodeURIComponent(token)}/accept`, req, {
        skipAuthRedirect: true,
      }),
    onSuccess: (me) => startSession(qc, me),
  });
}

export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateMeRequest) => api<Me>("PATCH", "/me", req),
    onSuccess: (me) => qc.setQueryData(queryKeys.me, me),
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (req: ChangePasswordRequest) => api<void>("POST", "/me/password", req),
  });
}

export const useSessions = () =>
  useQuery({
    queryKey: queryKeys.sessions,
    queryFn: () => api<List<Session>>("GET", "/me/sessions").then(items),
  });

export function useRevokeSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/me/sessions/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.sessions }),
  });
}

// ---------------------------------------------------------------------------------------------
// Directory and tokens

export const useUsers = () =>
  useQuery({
    queryKey: queryKeys.users,
    queryFn: () => api<List<User>>("GET", "/users").then(items),
  });

export const useTokens = () =>
  useQuery({
    queryKey: queryKeys.tokens,
    queryFn: () => api<List<Token>>("GET", "/tokens").then(items),
  });

export function useCreateToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateTokenRequest) => api<CreatedToken>("POST", "/tokens", req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.tokens }),
  });
}

export function useRevokeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/tokens/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.tokens }),
  });
}

// ---------------------------------------------------------------------------------------------
// Sharing (rooms, agents, connectors)

export const useSharing = (kind: SharingResource, id: string) =>
  useQuery({
    queryKey: queryKeys.sharing(kind, id),
    queryFn: () => api<Sharing>("GET", `/${kind}/${id}/sharing`),
  });

export function useUpdateSharing(kind: SharingResource, id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: Sharing) => api<Sharing>("PUT", `/${kind}/${id}/sharing`, req),
    onSuccess: (sharing) => {
      qc.setQueryData(queryKeys.sharing(kind, id), sharing);
      // Visibility shows on the list and the resource itself; timelines and members are unaffected.
      qc.invalidateQueries({ queryKey: [kind], exact: true });
      qc.invalidateQueries({ queryKey: [kind, id], exact: true });
    },
  });
}

// ---------------------------------------------------------------------------------------------
// Rooms, members and messages

export const roomsQuery = () =>
  queryOptions({
    queryKey: queryKeys.rooms,
    queryFn: () => api<List<Room>>("GET", "/rooms").then(items),
  });

export const roomQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.room(id),
    queryFn: () => api<Room>("GET", `/rooms/${id}`),
  });

export const useRooms = () => useQuery(roomsQuery());
export const useRoom = (id: string) => useQuery(roomQuery(id));

export function useCreateRoom() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateRoomRequest) => api<Room>("POST", "/rooms", req),
    onSuccess: (room) => {
      qc.setQueryData(queryKeys.room(room.id), room);
      qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
    },
  });
}

export function useUpdateRoom(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateRoomRequest) => api<Room>("PATCH", `/rooms/${id}`, req),
    onSuccess: (room) => {
      qc.setQueryData(queryKeys.room(id), room);
      qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
    },
  });
}

export function useDeleteRoom() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/rooms/${id}`),
    onSuccess: (_, id) => {
      // Same cache effect as the live room.deleted event (ROOMDEL-02): gone from every list now.
      qc.removeQueries({ queryKey: queryKeys.room(id) });
      qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.filter((r) => r.id !== id));
      qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
        old?.filter((a) => a.room.id !== id),
      );
    },
  });
}

export function useResumeRoom(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<Room>("POST", `/rooms/${id}/resume`),
    onSuccess: (room) => qc.setQueryData(queryKeys.room(id), room),
  });
}

export const useRoomMembers = (id: string) =>
  useQuery({
    queryKey: queryKeys.roomMembers(id),
    queryFn: () => api<List<RoomMember>>("GET", `/rooms/${id}/members`).then(items),
  });

export function useAddMember(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: AddMemberRequest) => api<RoomMember>("POST", `/rooms/${roomId}/members`, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.roomMembers(roomId) }),
  });
}

export function useUpdateMember(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ memberId, ...req }: UpdateMemberRequest & { memberId: string }) =>
      api<RoomMember>("PATCH", `/rooms/${roomId}/members/${memberId}`, req),
    onSuccess: (member) =>
      qc.setQueryData<RoomMember[]>(queryKeys.roomMembers(roomId), (old) =>
        old?.map((m) => (m.id === member.id ? member : m)),
      ),
  });
}

export function useRemoveMember(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (memberId: string) => api<void>("DELETE", `/rooms/${roomId}/members/${memberId}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.roomMembers(roomId) }),
  });
}

export type MessagePages = InfiniteData<Page<Message>, number | undefined>;

/** Sorts a page oldest first, whatever order the server used. */
export function normalizePage(page: Page<Message>): Page<Message> {
  return { ...page, items: [...page.items].sort((a, b) => a.seq - b.seq) };
}

/** The `before_seq` cursor of the next (older) page, or undefined when there is none. */
export function olderPageCursor(page: Page<Message>): number | undefined {
  if (!page.has_more || page.items.length === 0) return undefined;
  return Math.min(...page.items.map((m) => m.seq));
}

export function fetchMessages(
  roomId: string,
  cursor: { before_seq?: number; after_seq?: number },
  signal?: AbortSignal,
) {
  return api<Page<Message>>("GET", `/rooms/${roomId}/messages`, undefined, {
    query: { ...cursor, limit: DefaultPageSize },
    signal,
  }).then(normalizePage);
}

/**
 * Room timeline: pages[0] is the newest page; fetchNextPage loads older messages. Items inside
 * each page are oldest first. Live events append to pages[0] (lib/live/handlers.ts).
 */
export const roomMessagesQuery = (roomId: string) =>
  infiniteQueryOptions({
    queryKey: queryKeys.roomMessages(roomId),
    queryFn: ({ pageParam, signal }) => fetchMessages(roomId, { before_seq: pageParam }, signal),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: olderPageCursor,
    staleTime: Number.POSITIVE_INFINITY,
  });

export const useRoomMessages = (roomId: string) => useInfiniteQuery(roomMessagesQuery(roomId));

export interface PostMessageInput extends PostMessageRequest {
  files?: File[];
}

export function usePostMessage(roomId: string) {
  return useMutation({
    mutationFn: ({ files, ...req }: PostMessageInput) => {
      if (!files?.length) return api<Message>("POST", `/rooms/${roomId}/messages`, req);
      const form = new FormData();
      form.append(MultipartMessageField, JSON.stringify(req));
      for (const file of files) form.append(MultipartFileField, file, file.name);
      return api<Message>("POST", `/rooms/${roomId}/messages`, form);
    },
    // The stored message comes back through the live room.message event; no cache write here,
    // so the timeline has a single source of appends.
  });
}

export const attachmentUrl = (id: string) => `/api/v1/attachments/${id}`;

// ---------------------------------------------------------------------------------------------
// Room files (FILES-01, FILES-04)

/** A files list filter: one kind, or every file. */
export type FileFilter = FileKind | "all";

/** The kind the server gives a file: its MIME family, document for everything else. */
export function fileKindOf(mime: string): FileKind {
  const family = mime.toLowerCase().split("/")[0];
  return family === "image" || family === "audio" || family === "video" ? family : FileKindDocument;
}

/** A message's attachments as room files, newest first like the files list. */
export function messageFiles(m: Message): RoomFile[] {
  return m.attachments
    .map((a) => ({
      ...a,
      kind: fileKindOf(a.mime),
      message_id: m.id,
      message_seq: m.seq,
      author_kind: m.author_kind,
      author_name: m.author_name,
      created_at: m.created_at,
    }))
    .reverse();
}

export type FilePages = InfiniteData<Page<RoomFile>, string | undefined>;

/** A room's files, newest first; fetchNextPage loads older ones (cursor = last id). */
export const roomFilesQuery = (roomId: string, kind: FileFilter) =>
  infiniteQueryOptions({
    queryKey: queryKeys.roomFiles(roomId, kind),
    queryFn: ({ pageParam, signal }) =>
      api<Page<RoomFile>>("GET", `/rooms/${roomId}/attachments`, undefined, {
        query: {
          kind: kind === "all" ? undefined : kind,
          before: pageParam,
          limit: DefaultPageSize,
        },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page: Page<RoomFile>) =>
      page.has_more ? page.items[page.items.length - 1]?.id : undefined,
  });

export const useRoomFiles = (roomId: string, kind: FileFilter) =>
  useInfiniteQuery(roomFilesQuery(roomId, kind));

// ---------------------------------------------------------------------------------------------
// Dashboard activity

/** Feed entries requested, then kept in the dashboard feed in total and at most per room. */
export const ACTIVITY_FETCH = DefaultActivityLimit;
export const ACTIVITY_SIZE = 8;
export const ACTIVITY_PER_ROOM = 3;

/** Newest message first; seq breaks ties inside a room. */
export function newestFirst(a: ActivityItem, b: ActivityItem): number {
  const diff = Date.parse(b.message.created_at) - Date.parse(a.message.created_at);
  return diff !== 0 ? diff : b.message.seq - a.message.seq;
}

/**
 * Shapes the dashboard feed: newest first, no duplicate ids, at most ACTIVITY_PER_ROOM messages
 * per room (one busy room must not hide the others), at most ACTIVITY_SIZE in total.
 */
export function trimActivity(entries: ActivityItem[]): ActivityItem[] {
  const seen = new Set<string>();
  const perRoom = new Map<string, number>();
  const out: ActivityItem[] = [];
  for (const entry of [...entries].sort(newestFirst)) {
    const m = entry.message;
    if (seen.has(m.id)) continue;
    const n = perRoom.get(m.room_id) ?? 0;
    if (n >= ACTIVITY_PER_ROOM) continue;
    seen.add(m.id);
    perRoom.set(m.room_id, n + 1);
    out.push(entry);
    if (out.length === ACTIVITY_SIZE) break;
  }
  return out;
}

/**
 * The dashboard feed: latest messages across the rooms the user can see, newest first, from
 * one `GET /activity` request. Live room.message events keep it current (lib/live/handlers.ts).
 */
export const activityQuery = () =>
  queryOptions({
    queryKey: queryKeys.activity,
    queryFn: ({ signal }) =>
      api<List<ActivityItem>>("GET", "/activity", undefined, {
        query: { limit: ACTIVITY_FETCH },
        signal,
      }).then((list) => trimActivity(list.items)),
  });

export const useActivity = () => useQuery(activityQuery());

// ---------------------------------------------------------------------------------------------
// Agents

export const agentsQuery = () =>
  queryOptions({
    queryKey: queryKeys.agents,
    queryFn: () => api<List<Agent>>("GET", "/agents").then(items),
  });

export const useAgents = () => useQuery(agentsQuery());

export function useUpdateAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...req }: UpdateAgentRequest & { id: string }) =>
      api<Agent>("PATCH", `/agents/${id}`, req),
    onSuccess: (agent) =>
      qc.setQueryData<Agent[]>(queryKeys.agents, (old) =>
        old?.map((a) => (a.id === agent.id ? agent : a)),
      ),
  });
}

export function useDeleteAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/agents/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.agents }),
  });
}

// ---------------------------------------------------------------------------------------------
// Connectors and chats

export const useConnectorKinds = () =>
  useQuery({
    queryKey: queryKeys.connectorKinds,
    queryFn: () => api<List<ConnectorKind>>("GET", "/connector-kinds").then(items),
    staleTime: Number.POSITIVE_INFINITY,
  });

export const connectorsQuery = () =>
  queryOptions({
    queryKey: queryKeys.connectors,
    queryFn: () => api<List<Connector>>("GET", "/connectors").then(items),
  });

export const connectorQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.connector(id),
    queryFn: () => api<Connector>("GET", `/connectors/${id}`),
  });

export const useConnectors = () => useQuery(connectorsQuery());
export const useConnector = (id: string) => useQuery(connectorQuery(id));

export function useCreateConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateConnectorRequest) => api<Connector>("POST", "/connectors", req),
    onSuccess: (c) => {
      // Generated secrets are shown once by the caller and never cached (reads mask them).
      const { generated_secrets: _secrets, ...stored } = c;
      qc.setQueryData<Connector>(queryKeys.connector(c.id), stored);
      qc.invalidateQueries({ queryKey: queryKeys.connectors, exact: true });
    },
  });
}

export function useUpdateConnector(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateConnectorRequest) => api<Connector>("PATCH", `/connectors/${id}`, req),
    onSuccess: (c) => {
      qc.setQueryData(queryKeys.connector(id), c);
      qc.invalidateQueries({ queryKey: queryKeys.connectors, exact: true });
    },
  });
}

export function useDeleteConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/connectors/${id}`),
    onSuccess: (_, id) => {
      qc.removeQueries({ queryKey: queryKeys.connector(id) });
      qc.invalidateQueries({ queryKey: queryKeys.connectors, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.chats });
    },
  });
}

export const useConnectorChats = (id: string) =>
  useQuery({
    queryKey: queryKeys.connectorChats(id),
    queryFn: () => api<List<Chat>>("GET", `/connectors/${id}/chats`).then(items),
  });

export function useRefreshChats(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("POST", `/connectors/${id}/chats/refresh`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.connectorChats(id) }),
  });
}

/** Chats the user may add to rooms (owned or shared connectors). */
export const useChats = () =>
  useQuery({
    queryKey: queryKeys.chats,
    queryFn: () => api<List<Chat>>("GET", "/chats").then(items),
  });

/** Creates a private room with the chat and the agent as members (UI-13). */
export function useRouteChat() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ chatId, agentId }: { chatId: string; agentId: string }) =>
      api<Room>("POST", `/chats/${chatId}/route`, { agent_id: agentId }),
    onSuccess: (room) => {
      qc.setQueryData(queryKeys.room(room.id), room);
      qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
    },
  });
}

// ---------------------------------------------------------------------------------------------
// Identities

export const useIdentities = () =>
  useQuery({
    queryKey: queryKeys.identities,
    queryFn: () => api<List<Identity>>("GET", "/identities").then(items),
  });

export function useStartPairing() {
  return useMutation({
    // A code is bound to the connector it is requested for (IDN-01, IDN-02).
    mutationFn: (connectorId: string) =>
      api<PairingCode>("POST", "/identities/pairing", {
        connector_id: connectorId,
      } satisfies PairingRequest),
  });
}

export function useDeleteIdentity() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/identities/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.identities }),
  });
}

// ---------------------------------------------------------------------------------------------
// Admin

export const useAdminUsers = () =>
  useQuery({
    queryKey: queryKeys.adminUsers,
    queryFn: () => api<List<AdminUser>>("GET", "/admin/users").then(items),
  });

export function useUpdateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...req }: UpdateUserRequest & { id: string }) =>
      api<AdminUser>("PATCH", `/admin/users/${id}`, req),
    onSuccess: (user) =>
      qc.setQueryData<AdminUser[]>(queryKeys.adminUsers, (old) =>
        old?.map((u) => (u.id === user.id ? user : u)),
      ),
  });
}

export const useAdminInvites = () =>
  useQuery({
    queryKey: queryKeys.adminInvites,
    queryFn: () => api<List<Invite>>("GET", "/admin/invites").then(items),
  });

export function useCreateInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateInviteRequest) => api<CreatedInvite>("POST", "/admin/invites", req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.adminInvites }),
  });
}

export function useRevokeInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/admin/invites/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.adminInvites }),
  });
}

export const useAudit = () =>
  useInfiniteQuery({
    queryKey: queryKeys.adminAudit,
    queryFn: ({ pageParam, signal }) =>
      api<Page<AuditEntry>>("GET", "/admin/audit", undefined, {
        query: { before: pageParam },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.has_more ? page.items.at(-1)?.id : undefined),
  });

// ---------------------------------------------------------------------------------------------
// Machine approval (WIZ-02, DEV-02, DEV-05): the user types the code the terminal shows; it
// travels in the request body only, never in a URL.

export type DeviceAction = "lookup" | "approve" | "deny";

/** Looks up, approves or denies the request with a typed code; answers the request. */
export function useDeviceAction() {
  return useMutation({
    mutationFn: ({ action, code }: { action: DeviceAction; code: string }) =>
      api<DeviceRequest>("POST", `/devices/${action}`, { code }),
  });
}
