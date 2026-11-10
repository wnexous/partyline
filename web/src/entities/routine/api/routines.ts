import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { ScheduleList } from "@/shared/api/types.gen";

/**
 * The routines of an AI (HB-13): whoever manages it gets every routine with its prompt; anyone else
 * who sees the AI gets only how many there are (`count`, no items: HB-12). A 403 or 404 is a viewer
 * who may not read them at all.
 */
export const routinesQuery = (agentId: string, enabled = true) =>
  queryOptions({
    queryKey: queryKeys.agentRoutines(agentId),
    queryFn: () => api<ScheduleList>("GET", `/agents/${agentId}/schedules`),
    enabled: enabled && agentId !== "",
  });

export const useRoutines = (agentId: string, enabled = true) =>
  useQuery(routinesQuery(agentId, enabled));
