import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { AddInstructorRequest, AgentInstructor } from "@/shared/api/types.gen";

/*
 * Who instructs an AI besides its host (SR2-06, SR2-10): only the host adds and removes them. The
 * profile carries the list (`ai.instructed_by`), so each write reads the profile again.
 */

const instructorsPath = (agentId: string) => `/agents/${encodeURIComponent(agentId)}/instructors`;

/** Adds the identity named by @handle as an instructor; 404 `contact_not_found` names nobody the host sees. */
export function useAddInstructor(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: AddInstructorRequest) =>
      api<AgentInstructor>("POST", instructorsPath(agentId), req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.intelligence(agentId) }),
  });
}

/** Takes an instructor off the AI; one that already was not there is no error. */
export function useRemoveInstructor(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (iid: string) =>
      api<void>("DELETE", `${instructorsPath(agentId)}/${encodeURIComponent(iid)}`),
    onSettled: () => qc.invalidateQueries({ queryKey: queryKeys.intelligence(agentId) }),
  });
}
