import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { List, PutSecretRequest, Secret } from "@/shared/api/types.gen";

/**
 * The owner's named secrets for container AIs (CA-03): names and dates only. A value goes in with a
 * PUT and is never returned by any route (CA-08), so it is never in this cache.
 */
export const secretsQuery = () =>
  queryOptions({
    queryKey: queryKeys.secrets,
    queryFn: () => api<List<Secret>>("GET", "/secrets").then(items),
  });

export const useSecrets = (enabled = true) => useQuery({ ...secretsQuery(), enabled });

/**
 * Stores a secret, or replaces the value of one with that name in place (204): the containers that
 * expose it keep working with the new value.
 */
export function usePutSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      api<void>("PUT", `/secrets/${encodeURIComponent(name)}`, {
        value,
      } satisfies PutSecretRequest),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.secrets, exact: true }),
  });
}

/** Deletes one of the owner's secrets by name (404 when there is none). */
export function useDeleteSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api<void>("DELETE", `/secrets/${encodeURIComponent(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.secrets, exact: true }),
  });
}
