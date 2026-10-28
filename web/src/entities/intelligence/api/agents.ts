import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { Agent, Contacts, List, MergeAgentRequest } from "@/shared/api/types.gen";
import { withoutAI } from "../lib/contacts";

export const agentsQuery = () =>
  queryOptions({
    queryKey: queryKeys.agents,
    queryFn: () => api<List<Agent>>("GET", "/agents").then(items),
  });

export const useAgents = () => useQuery(agentsQuery());

/**
 * Merges an AI into another one of the caller's (MRG-01): the survivor keeps its groups, DMs,
 * history, cursors, organizations and connectors, and the AI in `from` is deleted. Refused with 409
 * `identity_online` while `from` has a live session (MRG-02).
 */
export function useMergeAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ from, into }: { from: string; into: string }) =>
      api<Agent>("POST", `/agents/${from}/merge`, { into } satisfies MergeAgentRequest),
    onSuccess: (_, { from }) => {
      // The AI is gone from the flat lists at once; the tree, the conversations and the rooms it
      // was in are the server's to rebuild.
      qc.setQueryData<Contacts>(queryKeys.contacts, (old) => old && withoutAI(old, from));
      qc.invalidateQueries({ queryKey: queryKeys.contacts, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.rooms });
      // Its profile is marked stale without a refetch: the row that shows it is about to leave.
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(from), refetchType: "none" });
    },
  });
}

export function useDeleteAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/agents/${id}`),
    onSuccess: (_, id) => {
      qc.invalidateQueries({ queryKey: queryKeys.agents });
      // The AI leaves the members and the chart of every organization it was in.
      qc.invalidateQueries({ queryKey: queryKeys.orgs });
      // Gone from the flat lists at once; the server rebuilds the tree (TREE-06). The profile is
      // marked stale without a refetch: the page showing it is about to leave.
      qc.setQueryData<Contacts>(queryKeys.contacts, (old) => old && withoutAI(old, id));
      qc.invalidateQueries({ queryKey: queryKeys.contacts, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(id), refetchType: "none" });
    },
  });
}

/**
 * Ends an AI's live session (POS-04): the AI is not deleted and may connect again. Whether it is
 * online is the live presence's to say; the profile is marked stale to read the new state.
 */
export function useEndSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/agents/${id}/session`),
    onSuccess: (_, id) => {
      qc.invalidateQueries({ queryKey: queryKeys.intelligence(id), exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.agents });
    },
  });
}
