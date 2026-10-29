import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { List, RenameRunnerRequest, Runner } from "@/shared/api/types.gen";

/**
 * How often an open runners section reads the list again (CA-01): a server connects, drops or
 * reports Docker without a live event of its own, so the page polls while it is open.
 */
export const RunnerPollMs = 10_000;

export const runnersQuery = () =>
  queryOptions({
    queryKey: queryKeys.runners,
    queryFn: () => api<List<Runner>>("GET", "/runners").then(items),
  });

/** The owner's servers; `poll` keeps the online state and the container count current. */
export const useRunners = (poll = false) =>
  useQuery({ ...runnersQuery(), refetchInterval: poll ? RunnerPollMs : false });

/** Renames one server (CA-01); a name another server of the owner has is a 409. */
export function useRenameRunner() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) =>
      api<Runner>("PATCH", `/runners/${id}`, { name } satisfies RenameRunnerRequest),
    onSuccess: (runner) => {
      qc.setQueryData<Runner[]>(queryKeys.runners, (old) =>
        old?.map((r) => (r.id === runner.id ? runner : r)),
      );
      qc.invalidateQueries({ queryKey: queryKeys.runners, exact: true });
    },
  });
}

/** Removes a server and revokes its token (CA-01); a server that still runs containers is a 409. */
export function useDeleteRunner() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/runners/${id}`),
    onSuccess: (_, id) => {
      qc.setQueryData<Runner[]>(queryKeys.runners, (old) => old?.filter((r) => r.id !== id));
      qc.invalidateQueries({ queryKey: queryKeys.runners, exact: true });
    },
  });
}
