import type { QueryClient } from "@tanstack/react-query";
import type { FileFilter, FilePages, MessagePages } from "@/shared/api/cache-types";
import { queryKeys } from "@/shared/api/query-keys";
import type { Message } from "@/shared/api/types.gen";
import { messageFiles } from "../lib/files";
import { mergeMessages } from "../lib/pages";

/*
 * What the live frames do to the cached timelines and files lists of a message. Each function is
 * a pure cache patch: no network, no React.
 */

/** Highest sequence number cached for a room, or undefined when its timeline is not cached. */
export function lastSeq(qc: QueryClient, roomId: string): number | undefined {
  const data = qc.getQueryData<MessagePages>(queryKeys.roomMessages(roomId));
  if (!data) return undefined;
  let max: number | undefined;
  for (const page of data.pages) {
    for (const m of page.items) if (max === undefined || m.seq > max) max = m.seq;
  }
  return max;
}

/** A live room.message: it joins the room's cached timeline (its newest page, in seq order). */
export function receiveMessage(qc: QueryClient, m: Message) {
  qc.setQueryData<MessagePages>(queryKeys.roomMessages(m.room_id), (old) =>
    mergeMessages(old, [m]),
  );
}

/** Puts a live message's files at the top of the room's cached files lists (FILES-05). */
export function prependFiles(qc: QueryClient, m: Message) {
  if (m.attachments.length === 0) return;
  const files = messageFiles(m);
  for (const [key, data] of qc.getQueriesData<FilePages>({
    queryKey: ["rooms", m.room_id, "files"],
  })) {
    const first = data?.pages[0];
    if (!data || !first) continue;
    const kind = key[3] as FileFilter;
    const known = new Set(data.pages.flatMap((p) => p.items.map((f) => f.id)));
    const fresh = files.filter((f) => !known.has(f.id) && (kind === "all" || f.kind === kind));
    if (fresh.length === 0) continue;
    qc.setQueryData<FilePages>(key, {
      ...data,
      pages: [{ ...first, items: [...fresh, ...first.items] }, ...data.pages.slice(1)],
    });
  }
}
