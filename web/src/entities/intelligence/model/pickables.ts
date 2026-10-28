import { useMemo } from "react";
import { useOrgUsers } from "@/hooks/use-org-users";
import { useContacts } from "../api/contacts";
import { type Pickable, pickableList } from "../lib/contacts";

/**
 * The people and AIs a picker offers (AD-024), one list by name. Scoped to an organization when
 * `orgId` is given (its members and its AIs), unscoped otherwise. Null while the contacts are
 * loading, and while the organization's users are: the instance's users are not listed first.
 */
export function usePickables({ orgId }: { orgId?: string }): Pickable[] | null {
  const contacts = useContacts();
  const { users: orgUsers, loading: usersLoading } = useOrgUsers(orgId);
  return useMemo(
    () => (contacts.data && !usersLoading ? pickableList(contacts.data, orgId, orgUsers) : null),
    [contacts.data, orgId, orgUsers, usersLoading],
  );
}
