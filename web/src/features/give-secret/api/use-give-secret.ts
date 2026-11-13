import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, isApiError } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import { ErrorCodeAlreadyAnswered, type Message } from "@/shared/api/types.gen";

/*
 * Gives the value of a secret request to the AI that asked (SR-05, SR-06). The value goes in the body of
 * this one request and nowhere else: not into the query cache (the mutation's variables are dropped once
 * it settles, and the card keeps no copy), not into a toast, not into the timeline. The answer is the reply
 * "🔑 NAME saved", which also arrives through the live room.message event. A request someone else answered
 * first refreshes the timeline so their answer shows.
 */

export interface GiveSecretInput {
  /** The secret request this value answers. */
  messageId: string;
  value: string;
}

export function useGiveSecret(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationKey: ["give-secret", roomId] as const,
    mutationFn: ({ messageId, value }: GiveSecretInput) =>
      api<Message>("POST", `/rooms/${roomId}/messages/${messageId}/secret`, { value }),
    // The cache would otherwise keep the variables, the value among them, for as long as the mutation lives.
    gcTime: 0,
    onError: (error) => {
      if (isApiError(error) && error.code === ErrorCodeAlreadyAnswered) {
        void qc.invalidateQueries({ queryKey: queryKeys.roomMessages(roomId) });
      }
    },
  });
}
