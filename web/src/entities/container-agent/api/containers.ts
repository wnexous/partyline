import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { ContainerActionRequest, ContainerAgent, ContainerLogs } from "@/shared/api/types.gen";

/**
 * How often an open "Container" section reads the state again (CA-06): the runner reports the
 * states it sees over its own connection and the page has no live event for them, so it polls.
 */
export const ContainerPollMs = 10_000;

/** The last log lines one read asks for (the server clamps to 500). */
export const ContainerLogsTail = 200;

/**
 * The container of one AI. A 404 means the AI has no container (or the viewer does not manage it),
 * which is a normal answer, so it is not retried.
 */
export const containerQuery = (agentId: string, enabled = true) =>
  queryOptions({
    queryKey: queryKeys.container(agentId),
    queryFn: () => api<ContainerAgent>("GET", `/agents/${agentId}/container`),
    enabled,
    retry: false,
  });

export const useContainerAgent = (agentId: string, { enabled = true, poll = false } = {}) =>
  useQuery({
    ...containerQuery(agentId, enabled),
    refetchInterval: poll ? ContainerPollMs : false,
  });

/** The last log lines of the container, read on demand (the sheet is open). */
export const useContainerLogs = (agentId: string, enabled: boolean) =>
  useQuery({
    queryKey: queryKeys.containerLogs(agentId),
    queryFn: () =>
      api<ContainerLogs>("GET", `/agents/${agentId}/container/logs`, undefined, {
        query: { tail: ContainerLogsTail },
      }),
    enabled,
    staleTime: 0,
    retry: false,
  });

/** Starts, stops or restarts the container (CA-06); the answer is the container as it is after. */
export function useContainerAction(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (action: ContainerActionRequest["action"]) =>
      api<ContainerAgent>("POST", `/agents/${agentId}/container`, {
        action,
      } satisfies ContainerActionRequest),
    onSuccess: (container) => {
      qc.setQueryData(queryKeys.container(agentId), container);
      // The owner's servers show how many container AIs each one runs (CA-01).
      qc.invalidateQueries({ queryKey: queryKeys.runners, exact: true });
    },
  });
}

/**
 * Removes the container, and with `volume` its workspace too (CA-06). The AI stays; only its
 * container row and token go.
 */
export function useDeleteContainer(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ volume }: { volume: boolean }) =>
      api<void>("DELETE", `/agents/${agentId}/container`, undefined, { query: { volume } }),
    onSuccess: () => {
      // The section that shows it goes away at once: the AI has no container any more.
      qc.setQueryData(queryKeys.container(agentId), undefined);
      qc.removeQueries({ queryKey: queryKeys.container(agentId) });
      qc.removeQueries({ queryKey: queryKeys.containerLogs(agentId) });
      qc.invalidateQueries({ queryKey: queryKeys.runners, exact: true });
    },
  });
}
