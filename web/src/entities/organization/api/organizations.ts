import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { putAvatarImage } from "@/shared/api/avatar-upload";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  CreateOrganizationRequest,
  List,
  Organization,
  OrgMember,
  UpdateOrganizationRequest,
} from "@/shared/api/types.gen";
import { toMyOrgs } from "../lib/orgs";
import { invalidateOrganizations, storeOrg } from "../model/cache";

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
