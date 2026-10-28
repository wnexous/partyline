import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { toast } from "sonner";
import { readReactions, useToggleReaction } from "@/lib/api/queries";
import { atReactionLimit, reactedWith } from "@/lib/reactions";
import { isApiError } from "@/shared/api/client";
import { ErrorCodeReactionLimit, MaxMessageEmoji, type Message } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";

/*
 * What a tap on a reaction does in a conversation (RCT-01, RCT-04, RCT-08): the viewer reacts with the emoji or,
 * if they used it already, takes the reaction back. Which of the two goes by what the cache holds for the message
 * now, not by the copy the tap was made on, so a second tap before the screen drew the first still undoes it. A
 * message that holds the most emoji says so instead of asking the server; a request that fails says so, and the
 * screen goes back to what it showed.
 */

/** The callback is the same from one render to the next, so the rows that hold it are not drawn again. */
export function useReact(roomId: string, meId: string) {
  const t = useT();
  const qc = useQueryClient();
  const limit = useCallback(() => toast.error(t("reactions.limit", { max: MaxMessageEmoji })), [t]);
  const { mutate } = useToggleReaction(roomId, (error) => {
    if (isApiError(error) && error.code === ErrorCodeReactionLimit) limit();
    else toast.error(t("reactions.failed"));
  });
  return useCallback(
    (message: Message, emoji: string) => {
      const reactions = readReactions(qc, roomId, message.id) ?? message.reactions;
      const add = !reactedWith(reactions, emoji, meId);
      if (add && atReactionLimit(reactions, emoji)) {
        limit();
        return;
      }
      mutate({ messageId: message.id, emoji, add, by: meId });
    },
    [qc, roomId, meId, limit, mutate],
  );
}
