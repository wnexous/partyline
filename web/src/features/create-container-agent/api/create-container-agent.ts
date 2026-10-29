import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { ContainerAgent, CreateContainerAgentRequest } from "@/shared/api/types.gen";

/**
 * Creates an AI in a container on one of the owner's servers (CA-04): the AI, its token and the
 * run all happen server-side. The servers show one more container afterwards and the contacts
 * list one more AI.
 */
export function useCreateContainerAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateContainerAgentRequest) =>
      api<ContainerAgent>("POST", "/container-agents", req),
    onSuccess: (container) => {
      qc.setQueryData(queryKeys.container(container.agent.id), container);
      qc.invalidateQueries({ queryKey: queryKeys.runners, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.contacts, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.agents });
    },
  });
}
