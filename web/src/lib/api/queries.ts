import {
  type InfiniteData,
  infiniteQueryOptions,
  keepPreviousData,
  type QueryClient,
  queryOptions,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { withIntelligence, withMachineLabel, withoutAI } from "@/lib/contacts";
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
  type Contacts,
  type Conversation,
  type ConversationKind,
  type CreateConnectorRequest,
  type CreatedInvite,
  type CreatedToken,
  type CreateGroupRequest,
  type CreateInviteRequest,
  type CreateRoomRequest,
  type CreateTokenRequest,
  DefaultActivityLimit,
  DefaultPageSize,
  DefaultPeoplePage,
  type DeviceRequest,
  type FileKind,
  FileKindDocument,
  type Identity,
  type IntelligenceProfile,
  type IntelligenceRef,
  type Invite,
  type InviteInfo,
  type List,
  type LoginRequest,
  type Machine,
  type MarkReadRequest,
  MaxPreviewLength,
  type Me,
  type MergeAgentRequest,
  type Message,
  type MessagePreview,
  MultipartFileField,
  MultipartMessageField,
  type OpenDMRequest,
  type Page,
  type PairingCode,
  type PairingRequest,
  type PostMessageRequest,
  type ReadState,
  type Room,
  type RoomFile,
  type RoomMember,
  type Session,
  type SetMachineLabelRequest,
  type SetupRequest,
  type SetupStatus,
  type Sharing,
  type Token,
  type UpdateConnectorRequest,
  type UpdateMemberRequest,
  type UpdateMeRequest,
  type UpdateProfileRequest,
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
  conversations: ["conversations"] as const,
  contacts: ["contacts"] as const,
  /** The people one connector has seen, filtered by a search text ("" = all of them). */
  people: (connectorId: string, q: string) => ["contacts", "people", connectorId, q] as const,
  intelligence: (id: string) => ["intelligences", id] as const,
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

/** Every token of the user, revoked ones included: the tokens page decides what to show. */
export const useTokens = () =>
  useQuery({
    queryKey: queryKeys.tokens,
    queryFn: () =>
      api<List<Token>>("GET", "/tokens", undefined, { query: { include_revoked: true } }).then(
        items,
      ),
  });

export function useCreateToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateTokenRequest) => api<CreatedToken>("POST", "/tokens", req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.tokens }),
  });
}

/** Deletes a revoked token for good (TOKH-02); the row leaves the cached list at once. */
export function usePurgeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/tokens/${id}/purge`),
    onSuccess: (_, id) => {
      qc.setQueryData<Token[]>(queryKeys.tokens, (old) => old?.filter((t) => t.id !== id));
      qc.invalidateQueries({ queryKey: queryKeys.tokens });
    },
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

/** A room the user just made, opened or changed: cached, and refreshed in the rooms list and the sidebar. */
function storeRoom(qc: QueryClient, room: Room) {
  qc.setQueryData(queryKeys.room(room.id), room);
  qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
}

export function useCreateRoom() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateRoomRequest) => api<Room>("POST", "/rooms", req),
    onSuccess: (room) => storeRoom(qc, room),
  });
}

export function useUpdateRoom(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateRoomRequest) => api<Room>("PATCH", `/rooms/${id}`, req),
    onSuccess: (room) => storeRoom(qc, room),
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
      qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
        old?.filter((c) => c.id !== id),
      );
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

/** The highest seq of the newest cached page; undefined while the timeline is empty or not loaded. */
function newestSeqOf(data: MessagePages): number | undefined {
  let newest: number | undefined;
  for (const m of data.pages[0]?.items ?? []) {
    if (newest === undefined || m.seq > newest) newest = m.seq;
  }
  return newest;
}

/** The newest message seq of a room's timeline; re-renders only when that number changes. */
export const useNewestSeq = (roomId: string) =>
  useInfiniteQuery({ ...roomMessagesQuery(roomId), select: newestSeqOf }).data;

export interface PostMessageInput extends PostMessageRequest {
  files?: File[];
}

/** Identifies one room's posts, so the page can read the refusal of its own composer. */
export const postMessageKey = (roomId: string) => ["post-message", roomId] as const;

export function usePostMessage(roomId: string) {
  return useMutation({
    mutationKey: postMessageKey(roomId),
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
// Conversations: the sidebar list, direct messages, groups and read state (LAY-01, DM-01, DM-06,
// DM-07, LAY-04)

/** The caller's DMs and groups, newest activity first, with unread counts (GET /conversations). */
export const conversationsQuery = () =>
  queryOptions({
    queryKey: queryKeys.conversations,
    queryFn: ({ signal }) =>
      api<List<Conversation>>("GET", "/conversations", undefined, { signal }).then(items),
  });

export const useConversations = () => useQuery(conversationsQuery());

/** Newest activity first; ties by title (case-insensitive), then id: the server's own order. */
export function newestActivityFirst(a: Conversation, b: Conversation): number {
  const diff = Date.parse(b.activity_at) - Date.parse(a.activity_at);
  if (diff !== 0) return diff;
  const [ta, tb] = [a.title.toLowerCase(), b.title.toLowerCase()];
  if (ta !== tb) return ta < tb ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/** The first `max` code points of `text`: an emoji counts once, whatever its UTF-16 length. */
function firstCodePoints(text: string, max: number): string {
  let end = 0;
  let count = 0;
  for (const point of text) {
    if (count === max) break;
    end += point.length;
    count += 1;
  }
  return text.slice(0, end);
}

/**
 * The sidebar preview of a message (LAY-01), built as the server builds the list's: the first
 * MaxPreviewLength code points; "[first file name]" when the text is blank and there is a file;
 * the sender's name in groups only.
 */
export function previewOf(m: Message, kind: ConversationKind): MessagePreview {
  const file = m.attachments[0]?.name;
  const blank = m.text.trim() === "";
  return {
    seq: m.seq,
    author_id: m.author.id,
    author_name: kind === "group" ? m.author_name : "",
    text: blank ? (file ? `[${file}]` : "") : firstCodePoints(m.text, MaxPreviewLength),
    created_at: m.created_at,
  };
}

/** Replaces one conversation of the cached list; a list that is not cached stays absent. */
export function patchConversation(
  qc: QueryClient,
  roomId: string,
  patch: (c: Conversation) => Conversation,
) {
  qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
    old?.map((c) => (c.id === roomId ? patch(c) : c)),
  );
}

/**
 * The user read a room up to `seq`: only what lies after it can still be unread. The count never
 * goes up here, so an answer that arrives late cannot undo a newer one.
 */
export function markConversationRead(qc: QueryClient, roomId: string, seq: number) {
  patchConversation(qc, roomId, (c) => {
    const after = c.last_message ? Math.max(0, c.last_message.seq - seq) : 0;
    const unread = Math.min(c.unread, after);
    return unread === c.unread ? c : { ...c, unread };
  });
}

/** Opens the DM with an intelligence (id): the existing one, or a new one (DM-01). */
export function useOpenDM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (withId: string) =>
      api<Room>("POST", "/conversations/dm", { with: withId } satisfies OpenDMRequest),
    onSuccess: (room) => storeRoom(qc, room),
  });
}

/**
 * Creates a group with members (LAY-04). A 422 `members_refused` carries every refused member in
 * `ApiError.refused`; nothing is created then.
 */
export function useCreateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateGroupRequest) => api<Room>("POST", "/groups", req),
    onSuccess: (room) => storeRoom(qc, room),
  });
}

/** Marks a room read up to `seq` (the newest message when absent) for this user (DM-07). */
export function useMarkRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ roomId, seq }: { roomId: string; seq?: number }) =>
      api<ReadState>(
        "PUT",
        `/rooms/${roomId}/read`,
        seq === undefined ? {} : ({ seq } satisfies MarkReadRequest),
      ),
    onSuccess: (state) =>
      patchConversation(qc, state.room_id, (c) =>
        state.unread < c.unread ? { ...c, unread: state.unread } : c,
      ),
  });
}

// ---------------------------------------------------------------------------------------------
// Intelligence profiles (PROF-03, PROF-05, PROF-07)

export const intelligenceQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.intelligence(id),
    queryFn: () => api<IntelligenceProfile>("GET", `/intelligences/${id}`),
  });

export const useIntelligence = (id: string) => useQuery(intelligenceQuery(id));

/** The summary every list shows, taken from a full profile. */
function refOfProfile(p: IntelligenceProfile): IntelligenceRef {
  return {
    id: p.id,
    kind: p.kind,
    role: p.role,
    display_name: p.display_name,
    handle: p.handle,
    avatar_url: p.avatar_url,
    platform: p.platform,
  };
}

/** Applies `fn` to every cached message; a page or timeline it changes nothing in stays as it is. */
function mapCachedMessages(qc: QueryClient, fn: (roomId: string, m: Message) => Message) {
  for (const [key, data] of qc.getQueriesData<MessagePages>({ queryKey: ["rooms"] })) {
    if (key.length !== 3 || key[2] !== "messages" || !data) continue;
    const roomId = String(key[1]);
    const pages = data.pages.map((page) => {
      const items = page.items.map((m) => fn(roomId, m));
      return items.some((m, i) => m !== page.items[i]) ? { ...page, items } : page;
    });
    if (pages.some((page, i) => page !== data.pages[i])) {
      qc.setQueryData<MessagePages>(key, { ...data, pages });
    }
  }
}

/**
 * Shows an intelligence's new summary (name, handle, avatar) in every cached place it appears:
 * the sidebar, timelines, member lists, DM headers, the profile page and the account's own data
 * (PROF-03). The live `intelligence.changed` event and the profile mutations both call it.
 */
export function patchIntelligence(qc: QueryClient, ref: IntelligenceRef) {
  qc.setQueryData<IntelligenceProfile>(queryKeys.intelligence(ref.id), (old) =>
    old
      ? {
          ...old,
          kind: ref.kind,
          role: ref.role,
          display_name: ref.display_name,
          handle: ref.handle,
          avatar_url: ref.avatar_url,
          platform: ref.platform,
        }
      : old,
  );
  qc.setQueryData<Me>(queryKeys.me, (old) =>
    old?.intelligence?.id === ref.id
      ? { ...old, display_name: ref.display_name, intelligence: ref }
      : old,
  );
  // A DM is titled by its counterpart. Group previews and member names hold the old name too:
  // the server's list is the source for them.
  qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
    old?.map((c) =>
      c.counterpart?.id === ref.id ? { ...c, title: ref.display_name, counterpart: ref } : c,
    ),
  );
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });

  // A name an owner set for an AI in a room (GRP-04) keeps showing there: the message carries
  // the shown name, so the room's members tell which rows must keep it.
  mapCachedMessages(qc, (roomId, m) => {
    if (m.author.id !== ref.id) return m;
    const members = qc.getQueryData<RoomMember[]>(queryKeys.roomMembers(roomId));
    const inRoomName = members?.some((mb) => mb.intelligence?.id === ref.id && mb.display_name);
    return inRoomName
      ? { ...m, author: { ...ref, display_name: m.author.display_name } }
      : { ...m, author_name: ref.display_name, author: ref };
  });

  for (const [key, data] of qc.getQueriesData<RoomMember[] | Room>({ queryKey: ["rooms"] })) {
    if (key.length === 3 && key[2] === "members" && Array.isArray(data)) {
      if (!data.some((mb) => mb.intelligence?.id === ref.id)) continue;
      qc.setQueryData<RoomMember[]>(
        key,
        data.map((mb) =>
          mb.intelligence?.id === ref.id
            ? { ...mb, name: ref.display_name, intelligence: ref }
            : mb,
        ),
      );
    } else if (key.length === 2 && data && !Array.isArray(data)) {
      qc.setQueryData<Room>(key, withParticipant(data, ref));
    }
  }
  qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.map((r) => withParticipant(r, ref)));

  // The contacts page, the pickers and the connectors that name this intelligence as operator.
  qc.setQueryData<Contacts>(queryKeys.contacts, (old) => old && withIntelligence(old, ref));
  for (const [key, data] of qc.getQueriesData<PeoplePages>({ queryKey: ["contacts", "people"] })) {
    if (!data?.pages.some((page) => page.items.some((p) => p.id === ref.id))) continue;
    qc.setQueryData<PeoplePages>(key, {
      ...data,
      pages: data.pages.map((page) => ({
        ...page,
        items: page.items.map((p) => (p.id === ref.id ? ref : p)),
      })),
    });
  }
  patchOperator(qc, ref);
}

/** Shows the operator's new summary on the cached connectors that name it (ACC-01). */
function patchOperator(qc: QueryClient, ref: IntelligenceRef) {
  qc.setQueryData<Connector[]>(queryKeys.connectors, (old) =>
    old?.some((c) => c.operator?.id === ref.id)
      ? old.map((c) => (c.operator?.id === ref.id ? { ...c, operator: ref } : c))
      : old,
  );
  for (const [key, c] of qc.getQueriesData<Connector>({ queryKey: queryKeys.connectors })) {
    if (key.length === 2 && c?.operator?.id === ref.id) {
      qc.setQueryData<Connector>(key, { ...c, operator: ref });
    }
  }
}

/** The room with `ref` in place of the DM participant that has its id. */
function withParticipant(room: Room, ref: IntelligenceRef): Room {
  if (!room.participants?.some((p) => p.id === ref.id)) return room;
  return { ...room, participants: room.participants.map((p) => (p.id === ref.id ? ref : p)) };
}

/** A profile the server answered with: cached, and shown everywhere the intelligence appears. */
function storeProfile(qc: QueryClient, profile: IntelligenceProfile) {
  qc.setQueryData(queryKeys.intelligence(profile.id), profile);
  patchIntelligence(qc, refOfProfile(profile));
}

/** Edits display name, handle and bio (PROF-03, PROF-05); 409 `handle_taken` on a taken handle. */
export function useUpdateIntelligence(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateProfileRequest) =>
      api<IntelligenceProfile>("PATCH", `/intelligences/${id}`, req),
    onSuccess: (profile) => storeProfile(qc, profile),
  });
}

/** How long an avatar upload may take before it is abandoned, so a stalled connection cannot
 * leave the photo "sending" forever. */
export const AvatarUploadTimeoutMs = 30_000;

/** Uploads an avatar: the image file goes as the raw body (PROF-07); 422 `avatar_invalid`. */
export function useUploadAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (image: Blob) => {
      const abort = new AbortController();
      const timer = setTimeout(() => abort.abort(), AvatarUploadTimeoutMs);
      try {
        return await api<IntelligenceProfile>("PUT", `/intelligences/${id}/avatar`, image, {
          signal: abort.signal,
        });
      } finally {
        clearTimeout(timer);
      }
    },
    onSuccess: (profile) => storeProfile(qc, profile),
  });
}

/** Removes the avatar: the initials show again everywhere. */
export function useRemoveAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("DELETE", `/intelligences/${id}/avatar`),
    onSuccess: () => {
      const cached = qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence(id));
      if (cached) {
        const { avatar_url: _removed, ...rest } = cached;
        storeProfile(qc, rest);
      }
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(id), exact: true });
    },
  });
}

// ---------------------------------------------------------------------------------------------
// Contacts: the machine tree, shared AIs, users and the people of the user's connectors
// (CONT-01, TREE-01..06)

/** Everyone the caller can reach, grouped as the contacts page shows them (GET /contacts). */
export const contactsQuery = () =>
  queryOptions({
    queryKey: queryKeys.contacts,
    queryFn: ({ signal }) => api<Contacts>("GET", "/contacts", undefined, { signal }),
  });

export const useContacts = () => useQuery(contactsQuery());

export type PeoplePages = InfiniteData<Page<IntelligenceRef>, string | undefined>;

/**
 * The people of one connector in name order, a page at a time; the cursor is the id of the last
 * person of the page before. `q` searches names, handles and account ids on the server, and the
 * previous search stays on screen while the next one loads.
 */
export const peopleQuery = (connectorId: string, q = "") =>
  infiniteQueryOptions({
    queryKey: queryKeys.people(connectorId, q),
    queryFn: ({ pageParam, signal }) =>
      api<Page<IntelligenceRef>>("GET", "/contacts/people", undefined, {
        query: {
          connector_id: connectorId,
          q: q || undefined,
          after: pageParam,
          limit: DefaultPeoplePage,
        },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page: Page<IntelligenceRef>) =>
      page.has_more ? page.items.at(-1)?.id : undefined,
  });

export const usePeople = (connectorId: string, q = "", enabled = true) =>
  useInfiniteQuery({ ...peopleQuery(connectorId, q), enabled, placeholderData: keepPreviousData });

/**
 * Names a machine (TREE-04): the label replaces the hostname in the tree; an empty one brings the
 * hostname back. Cached AI profiles show the label of their machine, so they are refetched.
 */
export function useSetMachineLabel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ hostname, label }: { hostname: string; label: string }) =>
      api<Machine>("PUT", `/machines/${encodeURIComponent(hostname)}`, {
        label,
      } satisfies SetMachineLabelRequest),
    onSuccess: (machine) => {
      qc.setQueryData<Contacts>(queryKeys.contacts, (old) => old && withMachineLabel(old, machine));
      qc.invalidateQueries({ queryKey: ["intelligences"] });
    },
  });
}

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
      author: m.author,
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

/**
 * Merges an AI into another one of the caller's (MRG-01): the survivor keeps its groups, DMs,
 * history, cursors, sharing and connectors, and the AI in `from` is deleted. Refused with 409
 * `identity_online` while `from` has a live session (MRG-02).
 */
export function useMergeAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ from, into }: { from: string; into: string }) =>
      api<Agent>("POST", `/agents/${from}/merge`, { into } satisfies MergeAgentRequest),
    onSuccess: (_, { from }) => {
      // The AI is gone from the flat lists at once; the tree, the conversations and the rooms it
      // was in are the server's to rebuild.
      qc.setQueryData<Contacts>(queryKeys.contacts, (old) => old && withoutAI(old, from));
      qc.invalidateQueries({ queryKey: queryKeys.contacts, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.rooms });
      // Its profile is marked stale without a refetch: the row that shows it is about to leave.
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(from), refetchType: "none" });
    },
  });
}

export function useDeleteAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/agents/${id}`),
    onSuccess: (_, id) => {
      qc.invalidateQueries({ queryKey: queryKeys.agents });
      // Gone from the flat lists at once; the server rebuilds the tree (TREE-06). The profile is
      // marked stale without a refetch: the page showing it is about to leave.
      qc.setQueryData<Contacts>(queryKeys.contacts, (old) => old && withoutAI(old, id));
      qc.invalidateQueries({ queryKey: queryKeys.contacts, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(id), refetchType: "none" });
    },
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
