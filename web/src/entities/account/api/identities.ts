import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { Identity, List, PairingCode, PairingRequest } from "@/shared/api/types.gen";

export const useIdentities = () =>
  useQuery({
    queryKey: queryKeys.identities,
    queryFn: () => api<List<Identity>>("GET", "/identities").then(items),
  });

export function useStartPairing() {
  return useMutation({
    // A code is bound to the connector it is requested for (IDN-01, IDN-02).
    mutationFn: (connectorId: string) =>
      api<PairingCode>("POST", "/identities/pairing", {
        connector_id: connectorId,
      } satisfies PairingRequest),
  });
}

export function useDeleteIdentity() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/identities/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.identities }),
  });
}
