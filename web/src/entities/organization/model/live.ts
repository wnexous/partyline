import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/shared/api/query-keys";
import type { Invitation } from "@/shared/api/types.gen";

/** An invitation came for the viewer (invitation_received): newest first, as the inbox lists them. */
export function invitationReceived(qc: QueryClient, inv: Invitation) {
  // One sent again replaces the earlier terms. An inbox nobody has opened is not cached: its
  // first fetch has the invitation.
  qc.setQueryData<Invitation[]>(
    queryKeys.invitations,
    (old) => old && [inv, ...old.filter((i) => i.id !== inv.id)],
  );
}
