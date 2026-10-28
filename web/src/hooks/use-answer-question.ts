import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, isApiError } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/queries";
import { type AnswerSubmission, ErrorCodeAlreadyAnswered, type Message } from "@/lib/api/types.gen";
import { useT } from "@/shared/i18n";

/*
 * Answers the question a message asks (QR-11, QR-12). It posts through the composer's path with the
 * answer body and nothing else, under a key of its own, so the bubble's pending state is separate from
 * the composer's and the stored message still arrives through the live room.message event. A second
 * answer by the same member is refused with 409 already_answered: the timeline is refreshed so the
 * answer posted elsewhere shows, and a failed post leaves the buttons acting.
 */

export interface AnswerQuestionInput {
  /** The question message this answer replies to. */
  replyTo: string;
  answers: AnswerSubmission;
}

export function useAnswerQuestion(roomId: string) {
  const t = useT();
  const qc = useQueryClient();
  return useMutation({
    mutationKey: ["answer-question", roomId] as const,
    mutationFn: ({ replyTo, answers }: AnswerQuestionInput) =>
      api<Message>("POST", `/rooms/${roomId}/messages`, {
        text: "",
        reply_to: replyTo,
        answers,
      }),
    onError: (error) => {
      if (isApiError(error) && error.code === ErrorCodeAlreadyAnswered) {
        toast.error(t("qr.alreadyAnswered"));
        void qc.invalidateQueries({ queryKey: queryKeys.roomMessages(roomId) });
        return;
      }
      toast.error(t("qr.failed"));
    },
  });
}
