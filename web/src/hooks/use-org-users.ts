import { useMemo } from "react";
import { useMe } from "@/entities/account";
import { useOrgMembers } from "@/entities/organization";
import { userMembers } from "@/lib/orgs";
import type { IntelligenceRef } from "@/shared/api/types.gen";

/**
 * The users who are members of an organization, for the pickers of a group of it (ORG-03).
 * `users` is undefined while there is nothing to offer yet: no organization is known, the members
 * are still loading (`loading`), or they could not be read. The pickers then offer the instance's
 * users, and the server refuses the ones who are not members.
 */
export function useOrgUsers(orgId: string | undefined): {
  users: IntelligenceRef[] | undefined;
  loading: boolean;
} {
  const meId = useMe().data?.intelligence.id;
  const members = useOrgMembers(orgId ?? "", orgId !== undefined);
  const users = useMemo(
    () => (members.data ? userMembers(members.data, meId) : undefined),
    [members.data, meId],
  );
  return { users, loading: orgId !== undefined && members.isLoading };
}
