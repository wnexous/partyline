import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { List, ScheduleList, ScheduleRun } from "@/shared/api/types.gen";

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

/**
 * The newest runs of one routine (HB-11), newest first and at most 50, for whoever manages the AI: read
 * when they open its history, not with the list. Anyone else is refused (403 for a viewer, 404 otherwise).
 */
export const routineRunsQuery = (agentId: string, id: string, enabled = true) =>
  queryOptions({
    queryKey: queryKeys.agentRoutineRuns(agentId, id),
    queryFn: () =>
      api<List<ScheduleRun>>(
        "GET",
        `/agents/${agentId}/schedules/${encodeURIComponent(id)}/runs`,
      ).then((list) => list.items),
    enabled: enabled && agentId !== "" && id !== "",
  });

export const useRoutineRuns = (agentId: string, id: string, enabled = true) =>
  useQuery(routineRunsQuery(agentId, id, enabled));
