import type { QueryClient } from "@tanstack/react-query";
import { fetchMessages, lastSeq, mergeMessages } from "@/entities/message";
import type { MessagePages } from "@/shared/api/cache-types";
import { queryKeys } from "@/shared/api/query-keys";

/** Room ids whose timelines are cached. */
function cachedTimelines(qc: QueryClient): { roomId: string; active: boolean }[] {
  return qc
    .getQueryCache()
    .findAll({ queryKey: ["rooms"] })
    .filter((q) => q.queryKey.length === 3 && q.queryKey[2] === "messages")
    .map((q) => ({ roomId: String(q.queryKey[1]), active: q.isActive() }));
}

/**
 * After a reconnect (UI-06): refetch the rooms list, fetch each open timeline after its last
 * seen sequence, and mark closed timelines stale so they reload when reopened.
 */
export async function catchUp(qc: QueryClient): Promise<void> {
  qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.agents });
  qc.invalidateQueries({ queryKey: queryKeys.contacts });
  qc.invalidateQueries({ queryKey: queryKeys.connectors });
  qc.invalidateQueries({ queryKey: queryKeys.activity });
  // Files lists refetch: messages missed while offline may have carried files.
  qc.invalidateQueries({
    predicate: (q) => q.queryKey[0] === "rooms" && q.queryKey[2] === "files",
  });
  await Promise.all(
    cachedTimelines(qc).map(async ({ roomId, active }) => {
      const after = lastSeq(qc, roomId);
      if (!active || after === undefined) {
        qc.invalidateQueries({ queryKey: queryKeys.roomMessages(roomId) });
        return;
      }
      try {
        let cursor = after;
        for (;;) {
          const page = await fetchMessages(roomId, { after_seq: cursor });
          qc.setQueryData<MessagePages>(queryKeys.roomMessages(roomId), (old) =>
            mergeMessages(old, page.items),
          );
          const last = page.items.at(-1);
          if (!page.has_more || !last) break;
          cursor = last.seq;
        }
      } catch {
        qc.invalidateQueries({ queryKey: queryKeys.roomMessages(roomId) });
      }
    }),
  );
}
