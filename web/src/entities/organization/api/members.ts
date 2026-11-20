import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { OrgMember, UpdateOrgMemberRequest } from "@/shared/api/types.gen";

/**
 * Changes a member's permission: an admin only, and 409 `last_admin` for the only admin (OMEM-04,
 * OMEM-05). The members list and the organization as the viewer sees it (it carries their own
 * permission) are read again, and so is the member's profile.
 */
export function useUpdateOrgMember(orgId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ iid, ...req }: UpdateOrgMemberRequest & { iid: string }) =>
      api<OrgMember>("PATCH", `/orgs/${encodeURIComponent(orgId)}/members/${iid}`, req),
    onSuccess: (_, { iid }) => {
      qc.invalidateQueries({ queryKey: queryKeys.orgMembers(orgId) });
      qc.invalidateQueries({ queryKey: queryKeys.org(orgId), exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(iid), exact: true });
    },
  });
}
