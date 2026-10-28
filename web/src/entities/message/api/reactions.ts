import { useMutation, useQueryClient } from "@tanstack/react-query";
import { withReaction } from "../lib/reaction-list";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { AddReactionRequest, List, Reaction } from "@/shared/api/types.gen";
import { applyReactions, readReactions } from "../model/cache";
import { roomMessagesQuery } from "./messages";

/** One tap on a reaction: to react with an emoji (`add`) or to take that reaction back. */
export interface ReactionToggle {
  messageId: string;
  emoji: string;
  add: boolean;
  /** The viewer's intelligence id: the reactions list who used an emoji by it (RCT-05). */
  by: string;
}

/**
 * Reacts to a message of the room, or takes the reaction back (RCT-03). The reaction shows at once and the answer
 * (the message's reactions as they are then) settles it. While another toggle of the same message is still on its
 * way the answer of an earlier one is not written: it knows nothing of that one and would take it off the screen.
 * A request that fails tells `onFailure` (a callback of `mutate` would be lost when another toggle follows) and,
 * once no later toggle of the message is pending, puts back what it changed and refetches the room's messages so
 * that the screen follows the server rather than a local opposite toggle (RCT-08). While one is still pending it
 * leaves the screen alone: the last toggle to settle writes the server's list.
 */
export function useToggleReaction(roomId: string, onFailure?: (error: unknown) => void) {
  const qc = useQueryClient();
  const pendingFor = (messageId: string) =>
    qc.isMutating({
      predicate: (m) =>
        m.options.mutationKey?.[0] === "reactions" &&
        (m.state.variables as ReactionToggle | undefined)?.messageId === messageId,
    });
  const change = ({ messageId, emoji, add, by }: ReactionToggle) => {
    const current = readReactions(qc, roomId, messageId);
    if (current) applyReactions(qc, roomId, messageId, withReaction(current, emoji, by, add));
  };
  return useMutation({
    mutationKey: ["reactions", roomId],
    mutationFn: ({ messageId, emoji, add }: ReactionToggle) =>
      (add
        ? api<List<Reaction>>("POST", `/rooms/${roomId}/messages/${messageId}/reactions`, {
            emoji,
          } satisfies AddReactionRequest)
        : api<List<Reaction>>(
            "DELETE",
            `/rooms/${roomId}/messages/${messageId}/reactions/${encodeURIComponent(emoji)}`,
          )
      ).then(items),
    onMutate: change,
    onError: (error, toggle) => {
      onFailure?.(error);
      // This one is still pending while its callbacks run: more than one is a later toggle on its way.
      if (pendingFor(toggle.messageId) > 1) return;
      change({ ...toggle, add: !toggle.add });
      // Mark the timeline stale first: it is kept fresh by the live frames and may have no observer
      // (and so no query function) of its own; fetching the room's messages query gives the truth.
      qc.invalidateQueries({ queryKey: queryKeys.roomMessages(roomId), refetchType: "none" });
      void qc.fetchInfiniteQuery(roomMessagesQuery(roomId)).catch(() => {});
    },
    onSuccess: (reactions, { messageId }) => {
      if (pendingFor(messageId) <= 1) applyReactions(qc, roomId, messageId, reactions);
    },
  });
}
