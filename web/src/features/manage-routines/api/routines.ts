import { type QueryClient, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, isApiError } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { Schedule, ScheduleList, ScheduleRequest, ScheduleRun } from "@/shared/api/types.gen";

/*
 * The writes of an AI's routines by whoever manages it (HB-13). Each answer is put in the cached list
 * at once, and the list and any open history are read again: the server's `agent.schedules` frame says
 * the same, and a refusal that means the list is stale (403, 404, 409) reads it again too.
 */

const routinesPath = (agentId: string) => `/agents/${agentId}/schedules`;
const routinePath = (agentId: string, id: string) =>
  `${routinesPath(agentId)}/${encodeURIComponent(id)}`;

function refresh(qc: QueryClient, agentId: string) {
  qc.invalidateQueries({ queryKey: queryKeys.agentRoutines(agentId), exact: true });
  // A write changes the runs too (a run now adds one, a pause cancels the pending ones).
  qc.invalidateQueries({ queryKey: queryKeys.agentRoutineRunsAll(agentId) });
}

/** A refusal after which what the page shows is not what the server has: read the list again. */
function refreshWhenStale(qc: QueryClient, agentId: string, err: unknown) {
  if (isApiError(err) && [403, 404, 409].includes(err.status)) refresh(qc, agentId);
}

function storeRoutine(qc: QueryClient, agentId: string, routine: Schedule) {
  qc.setQueryData<ScheduleList>(queryKeys.agentRoutines(agentId), (old) => {
    if (!old) return old;
    const known = old.items.some((item) => item.id === routine.id);
    const items = known
      ? old.items.map((item) => (item.id === routine.id ? routine : item))
      : [...old.items, routine];
    return { ...old, items, count: known ? old.count : old.count + 1 };
  });
}

export function useCreateRoutine(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ScheduleRequest) => api<Schedule>("POST", routinesPath(agentId), body),
    onSuccess: (routine) => {
      storeRoutine(qc, agentId, routine);
      refresh(qc, agentId);
    },
    onError: (err) => refreshWhenStale(qc, agentId, err),
  });
}

/** Changes the fields given of one routine: an edit, or `enabled` alone to pause or resume it. */
export function useUpdateRoutine(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: ScheduleRequest }) =>
      api<Schedule>("PATCH", routinePath(agentId, id), body),
    onSuccess: (routine) => {
      storeRoutine(qc, agentId, routine);
      refresh(qc, agentId);
    },
    onError: (err) => refreshWhenStale(qc, agentId, err),
  });
}

export function useDeleteRoutine(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", routinePath(agentId, id)),
    onSuccess: (_, id) => {
      qc.setQueryData<ScheduleList>(queryKeys.agentRoutines(agentId), (old) =>
        old
          ? {
              ...old,
              items: old.items.filter((item) => item.id !== id),
              count: Math.max(0, old.count - 1),
            }
          : old,
      );
      refresh(qc, agentId);
    },
    onError: (err) => refreshWhenStale(qc, agentId, err),
  });
}

/** "Run now": a run due at once, delivered to the principal session like any other. */
export function useRunRoutine(agentId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<ScheduleRun>("POST", `${routinePath(agentId, id)}/run`),
    onSuccess: () => refresh(qc, agentId),
    onError: (err) => refreshWhenStale(qc, agentId, err),
  });
}
