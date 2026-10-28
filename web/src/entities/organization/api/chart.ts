import {
  type QueryClient,
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  CreatePositionRequest,
  MovePreview,
  OrgChart,
  OrgMember,
  Position,
  UpdateOrgMemberRequest,
  UpdatePositionRequest,
} from "@/shared/api/types.gen";

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
