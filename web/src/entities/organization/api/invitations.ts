import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, isApiError } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import {
  type CreateInvitationRequest,
  ErrorCodeAlreadyMember,
  type Invitation,
  type List,
  type Organization,
} from "@/shared/api/types.gen";
import { invalidateOrganizations, storeOrg } from "../model/cache";

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
