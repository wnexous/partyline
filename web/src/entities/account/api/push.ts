import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  PushKey,
  PushSubscription,
  PushSubscriptionRequest,
  PushTest,
} from "@/shared/api/types.gen";

/**
 * The instance's VAPID public key. It is not read on mount: only pressing Enable needs it, so the
 * query starts disabled and Settings calls `refetch` (PUSH-18).
 */
export const usePushKey = () =>
  useQuery({
    queryKey: queryKeys.pushKey,
    queryFn: () => api<PushKey>("GET", "/push/key"),
    enabled: false,
    staleTime: Number.POSITIVE_INFINITY,
  });

/** Stores this browser's subscription; the same endpoint again updates its row (PUSH-03). */
export function useSubscribePush() {
  return useMutation({
    mutationFn: (req: PushSubscriptionRequest) =>
      api<PushSubscription>("POST", "/push/subscriptions", req),
  });
}

/** Removes this browser's row; an endpoint the server does not know is a 404 (PUSH-05). */
export function useUnsubscribePush() {
  return useMutation({
    mutationFn: (endpoint: string) => api<void>("DELETE", "/push/subscriptions", { endpoint }),
  });
}

/** Queues one test notification per subscription and answers how many it took (PUSH-15). */
export function useTestPush() {
  return useMutation({ mutationFn: () => api<PushTest>("POST", "/push/test") });
}
