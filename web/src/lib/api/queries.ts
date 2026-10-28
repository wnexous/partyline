import {
  type QueryClient,
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { toMyOrgs } from "@/lib/orgs";
import { putAvatarImage } from "@/shared/api/avatar-upload";
import { api, isApiError } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import {
  type Chat,
  type Connector,
  type ConnectorKind,
  type CreateConnectorRequest,
  type CreateInvitationRequest,
  type CreateOrganizationRequest,
  type CreatePositionRequest,
  ErrorCodeAlreadyMember,
  type Invitation,
  type List,
  type Me,
  type MovePreview,
  type Organization,
  type OrgChart,
  type OrgMember,
  type Position,
  type UpdateConnectorRequest,
  type UpdateOrganizationRequest,
  type UpdateOrgMemberRequest,
  type UpdatePositionRequest,
} from "@/shared/api/types.gen";

export const orgsQuery = () =>
  queryOptions({
    queryKey: queryKeys.orgs,
    queryFn: () => api<List<Organization>>("GET", "/orgs").then(items),
  });

export const useOrgs = () => useQuery(orgsQuery());

/**
 * The organizations the viewer belongs to, with what they are in each (OMEM-03): `GET /orgs`
 * lists every membership, guests and the personal organization included, by name.
 */
export const useMyOrgs = () => useQuery({ ...orgsQuery(), select: toMyOrgs });

export const orgQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.org(id),
    queryFn: () => api<Organization>("GET", `/orgs/${encodeURIComponent(id)}`),
  });

/** One organization as the viewer sees it; 404 for anyone who is not a member (CHART-02). */
export const useOrg = (id: string) => useQuery(orgQuery(id));

export const orgMembersQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.orgMembers(id),
    queryFn: () =>
      api<List<OrgMember>>("GET", `/orgs/${encodeURIComponent(id)}/members`).then(items),
  });

export const useOrgMembers = (id: string, enabled = true) =>
  useQuery({ ...orgMembersQuery(id), enabled });

/**
 * Who would stop managing a member if it moved to `positionId` (POS-04; `""` is no position),
 * asked before the move and shown in the dialog. It changes nothing, and the move's own errors
 * are still the move's. `enabled` keeps the dialog from asking for the position the member
 * already holds.
 */
export const useOrgMovePreview = (orgId: string, iid: string, positionId: string, enabled = true) =>
  useQuery({
    queryKey: queryKeys.orgMovePreview(orgId, iid, positionId),
    queryFn: () =>
      api<MovePreview>(
        "GET",
        `/orgs/${encodeURIComponent(orgId)}/members/${encodeURIComponent(iid)}/move-preview?position_id=${encodeURIComponent(positionId)}`,
      ),
    enabled,
  });

export const orgChartQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.orgChart(id),
    queryFn: () => api<OrgChart>("GET", `/orgs/${encodeURIComponent(id)}/chart`),
  });

/** The positions as a tree with their holders, and what the viewer may do about each (CHART-01, CHART-03). */
export const useOrgChart = (id: string) => useQuery(orgChartQuery(id));

/** The pending invitations of one organization; its admins only (OMEM-04). */
export const useOrgInvitations = (id: string, enabled = true) =>
  useQuery({
    queryKey: queryKeys.orgInvitations(id),
    queryFn: () =>
      api<List<Invitation>>("GET", `/orgs/${encodeURIComponent(id)}/invitations`).then(items),
    enabled,
  });

/**
 * The invitations waiting for the viewer, and for the AIs they manage (OMEM-01). The page that
 * lists them asks again every time it opens: the invitee is not told of a new one by the
 * organization's live frame, which only its members receive.
 */
export const useMyInvitations = () =>
  useQuery({
    queryKey: queryKeys.invitations,
    queryFn: () => api<List<Invitation>>("GET", "/invitations").then(items),
    refetchOnMount: "always",
  });

/**
 * Marks stale everything a change of an organization can have changed (CHART-02): the viewer's
 * organizations and, when one is named, all its pages (members, chart, invitations); the
 * invitations waiting for them; their own profile, which lists their organizations; the contacts,
 * which group the AIs by organization; and the conversations, whose DMs are filed under the
 * organizations both sides share.
 */
export function invalidateOrganizations(qc: QueryClient, orgId?: string) {
  if (orgId) {
    qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
    qc.invalidateQueries({ queryKey: queryKeys.org(orgId) });
  } else {
    qc.invalidateQueries({ queryKey: queryKeys.orgs });
  }
  qc.invalidateQueries({ queryKey: queryKeys.invitations });
  const meId = qc.getQueryData<Me>(queryKeys.me)?.intelligence.id;
  if (meId) qc.invalidateQueries({ queryKey: queryKeys.intelligence(meId), exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.contacts });
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
}

/** The server's order for the viewer's organizations: by name, ignoring case, then by id. */
function byName(a: Organization, b: Organization): number {
  const [x, y] = [a.name.toLowerCase(), b.name.toLowerCase()];
  if (x !== y) return x < y ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/**
 * An organization the server answered with: cached, and in the viewer's list at once, where the
 * server will place it (the list is fetched again to confirm).
 */
function storeOrg(qc: QueryClient, org: Organization) {
  qc.setQueryData(queryKeys.org(org.id), org);
  qc.setQueryData<Organization[]>(
    queryKeys.orgs,
    (old) => old && [...old.filter((o) => o.id !== org.id), org].sort(byName),
  );
  qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
}

/** Creates an organization (ORG-01): the creator becomes its admin, holding the root position. */
export function useCreateOrg() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateOrganizationRequest) => api<Organization>("POST", "/orgs", req),
    onSuccess: (org) => {
      storeOrg(qc, org);
      invalidateOrganizations(qc);
    },
  });
}

/** Renames an organization or changes its address (OMEM-04): 409 `conflict` for an address taken. */
export function useUpdateOrg(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateOrganizationRequest) =>
      api<Organization>("PATCH", `/orgs/${encodeURIComponent(id)}`, req),
    onSuccess: (org) => {
      storeOrg(qc, org);
      qc.invalidateQueries({ queryKey: queryKeys.contacts });
    },
  });
}

/** Uploads the organization's picture: the file goes as the raw body; 422 `avatar_invalid`. */
export function useUploadOrgAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (image: Blob) =>
      putAvatarImage<Organization>(`/orgs/${encodeURIComponent(id)}/avatar`, image),
    onSuccess: (org) => storeOrg(qc, org),
  });
}

/** Removes the picture: the initials show again. */
export function useRemoveOrgAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("DELETE", `/orgs/${encodeURIComponent(id)}/avatar`),
    onSuccess: () => {
      qc.setQueryData<Organization>(queryKeys.org(id), (old) => {
        if (!old) return old;
        const { avatar_url: _removed, ...rest } = old;
        return rest;
      });
      qc.invalidateQueries({ queryKey: queryKeys.org(id), exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
    },
  });
}

/**
 * Deletes an empty organization (ORG-06). 409 `organization_not_empty` carries the counts of what
 * it still owns in `ApiError.details`.
 */
export function useDeleteOrg() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/orgs/${encodeURIComponent(id)}`),
    onSuccess: (_, id) => {
      qc.removeQueries({ queryKey: queryKeys.org(id) });
      qc.setQueryData<Organization[]>(queryKeys.orgs, (old) => old?.filter((o) => o.id !== id));
      invalidateOrganizations(qc);
    },
  });
}

/**
 * What changing the chart touches: the chart itself, the position names the members list shows, and
 * the organization as the viewer sees it, which carries their own permission and position (an admin
 * who moves or demotes themselves changes what the pages offer them).
 */
function refreshChart(qc: QueryClient, orgId: string) {
  qc.invalidateQueries({ queryKey: queryKeys.orgChart(orgId) });
  qc.invalidateQueries({ queryKey: queryKeys.orgMembers(orgId) });
  qc.invalidateQueries({ queryKey: queryKeys.org(orgId), exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
}

/** Adds a position under an optional parent (POS-01); an admin only. */
export function useCreatePosition(orgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreatePositionRequest) =>
      api<Position>("POST", `/orgs/${encodeURIComponent(orgId)}/positions`, req),
    onSuccess: () => refreshChart(qc, orgId),
  });
}

/** Renames a position or moves it under another; 409 `position_cycle` for a move that closes a loop (POS-02). */
export function useUpdatePosition(orgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...req }: UpdatePositionRequest & { id: string }) =>
      api<Position>("PATCH", `/orgs/${encodeURIComponent(orgId)}/positions/${id}`, req),
    onSuccess: () => refreshChart(qc, orgId),
  });
}

/** Deletes a position: its holders and child positions move to its parent (POS-09). */
export function useDeletePosition(orgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api<void>("DELETE", `/orgs/${encodeURIComponent(orgId)}/positions/${id}`),
    onSuccess: () => refreshChart(qc, orgId),
  });
}

/**
 * Changes a member's permission (an admin only; 409 `last_admin` for the only admin, OMEM-05) or
 * its position (POS-04: an admin to any, a manager to a position below its own).
 */
export function useUpdateOrgMember(orgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ iid, ...req }: UpdateOrgMemberRequest & { iid: string }) =>
      api<OrgMember>("PATCH", `/orgs/${encodeURIComponent(orgId)}/members/${iid}`, req),
    onSuccess: (_, { iid }) => {
      refreshChart(qc, orgId);
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(iid), exact: true });
    },
  });
}

/** Invites an identity by @handle (OMEM-01); every member may invite guests, only admins more. */
export function useInviteToOrg(orgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateInvitationRequest) =>
      api<Invitation>("POST", `/orgs/${encodeURIComponent(orgId)}/invitations`, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.orgInvitations(orgId) }),
  });
}

/** Withdraws a pending invitation of the organization; an admin only. */
export function useRevokeInvitation(orgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api<void>("DELETE", `/orgs/${encodeURIComponent(orgId)}/invitations/${id}`),
    onSuccess: (_, id) => {
      qc.setQueryData<Invitation[]>(queryKeys.orgInvitations(orgId), (old) =>
        old?.filter((i) => i.id !== id),
      );
      qc.invalidateQueries({ queryKey: queryKeys.orgInvitations(orgId) });
    },
  });
}

/**
 * Answers an invitation (OMEM-02): the invited user, or whoever manages the invited AI. Answered,
 * or gone because it expired (410, OMEM-07), was withdrawn (404) or became pointless (409
 * already_member), it leaves the inbox at once; accepting also adds the organization to the viewer's.
 */
export function useAnswerInvitation(verb: "accept" | "decline") {
  const qc = useQueryClient();
  const drop = (id: string) =>
    qc.setQueryData<Invitation[]>(queryKeys.invitations, (old) => old?.filter((i) => i.id !== id));
  return useMutation({
    // Accepting answers with the organization the invitee now belongs to; declining with nothing.
    mutationFn: (id: string) => api<Organization | undefined>("POST", `/invitations/${id}/${verb}`),
    onSuccess: (org, id) => {
      drop(id);
      if (verb === "accept") {
        if (org?.id) storeOrg(qc, org);
        invalidateOrganizations(qc);
      }
    },
    onError: (err, id) => {
      // Gone, expired, or answered another way (the invitee became a member meanwhile).
      if (
        isApiError(err) &&
        (err.status === 410 || err.status === 404 || err.code === ErrorCodeAlreadyMember)
      ) {
        drop(id);
      }
    },
  });
}

export const useAcceptInvitation = () => useAnswerInvitation("accept");

export const useDeclineInvitation = () => useAnswerInvitation("decline");

/**
 * Takes an identity out of an organization (POS-05, POS-06): it leaves the organization's groups
 * and its position there, and nothing else changes. The identity's profile lists its
 * organizations, the contacts group the AIs by them and a DM is filed under the shared ones.
 */
export function useRemoveFromOrg() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ orgId, iid }: { orgId: string; iid: string }) =>
      api<void>("DELETE", `/orgs/${orgId}/members/${iid}`),
    onSuccess: (_, { orgId, iid }) => {
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(iid), exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.contacts });
      qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
      // The members and the chart of the organization lose it; leaving (iid is the viewer) also
      // takes the organization out of the viewer's own list.
      qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.org(orgId) });
    },
  });
}

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

export type { DeviceAction } from "@/entities/account";
export {
  ACTIVITY_FETCH,
  ACTIVITY_PER_ROOM,
  ACTIVITY_SIZE,
  activityQuery,
  inviteQuery,
  meQuery,
  newestFirst,
  setupStatusQuery,
  trimActivity,
  useAcceptInvite,
  useActivity,
  useAdminInvites,
  useAdminUsers,
  useAudit,
  useChangePassword,
  useCreateInvite,
  useCreateToken,
  useDeleteAccount,
  useDeleteIdentity,
  useDeviceAction,
  useIdentities,
  useInviteInfo,
  useLogin,
  useLogout,
  useMe,
  usePurgeToken,
  usePushKey,
  useRevokeInvite,
  useRevokeSession,
  useRevokeToken,
  useSessions,
  useSetDefaultOrg,
  useSetup,
  useSetupStatus,
  useStartPairing,
  useSubscribePush,
  useTestPush,
  useTokens,
  useUnsubscribePush,
  useUpdateMe,
  useUpdateUser,
  useUsers,
} from "@/entities/account";
export {
  agentsQuery,
  contactsQuery,
  intelligenceQuery,
  patchIntelligence,
  peopleQuery,
  useAgents,
  useContacts,
  useDeleteAgent,
  useEndSession,
  useIntelligence,
  useMergeAgent,
  usePeople,
  useRemoveAvatar,
  useUpdateIntelligence,
  useUploadAvatar,
} from "@/entities/intelligence";
export type { PostMessageInput, ReactionToggle } from "@/entities/message";
export {
  applyMessageUpdate,
  applyReactions,
  attachmentUrl,
  fetchMessages,
  fileKindOf,
  messageFiles,
  normalizePage,
  olderPageCursor,
  postMessageKey,
  readReactions,
  replaceMessage,
  roomMessagesQuery,
  useDeleteMessage,
  useEditMessage,
  useNewestSeq,
  usePostMessage,
  useRoomMessages,
  useToggleReaction,
} from "@/entities/message";
export {
  conversationsQuery,
  markConversationRead,
  newestActivityFirst,
  patchConversation,
  previewOf,
  roomFilesQuery,
  roomQuery,
  roomsQuery,
  useConversations,
  useCreateGroup,
  useCreateRoom,
  useDeleteRoom,
  useLeaveRoom,
  useMarkRead,
  useOpenDM,
  useRemoveRoomAvatar,
  useResumeRoom,
  useRoom,
  useRoomFiles,
  useRooms,
  useUpdateRoom,
  useUploadRoomAvatar,
} from "@/entities/room";
export {
  useAddMember,
  useRemoveMember,
  useRoomMembers,
  useUpdateMember,
} from "@/entities/room-member";
export { AvatarUploadTimeoutMs } from "@/shared/api/avatar-upload";
export type { FileFilter, FilePages, MessagePages, PeoplePages } from "@/shared/api/cache-types";
export { queryKeys } from "@/shared/api/query-keys";
