import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { CreatedToken, CreateTokenRequest, List, Token } from "@/shared/api/types.gen";

/** Every token of the user, revoked ones included: the tokens page decides what to show. */
export const useTokens = () =>
  useQuery({
    queryKey: queryKeys.tokens,
    queryFn: () =>
      api<List<Token>>("GET", "/tokens", undefined, { query: { include_revoked: true } }).then(
        items,
      ),
  });

export function useCreateToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateTokenRequest) => api<CreatedToken>("POST", "/tokens", req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.tokens }),
  });
}

/** Deletes a revoked token for good (TOKH-02); the row leaves the cached list at once. */
export function usePurgeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/tokens/${id}/purge`),
    onSuccess: (_, id) => {
      qc.setQueryData<Token[]>(queryKeys.tokens, (old) => old?.filter((t) => t.id !== id));
      qc.invalidateQueries({ queryKey: queryKeys.tokens });
    },
  });
}

export function useRevokeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/tokens/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.tokens }),
  });
}
