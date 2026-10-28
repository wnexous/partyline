import type { MessagePages } from "@/shared/api/cache-types";
import type { Message, Page } from "@/shared/api/types.gen";

/** Sorts a page oldest first, whatever order the server used. */
export function normalizePage(page: Page<Message>): Page<Message> {
  return { ...page, items: [...page.items].sort((a, b) => a.seq - b.seq) };
}

/** The `before_seq` cursor of the next (older) page, or undefined when there is none. */
export function olderPageCursor(page: Page<Message>): number | undefined {
  if (!page.has_more || page.items.length === 0) return undefined;
  return Math.min(...page.items.map((m) => m.seq));
}

/** The highest seq of the newest cached page; undefined while the timeline is empty or not loaded. */
export function newestSeqOf(data: MessagePages): number | undefined {
  let newest: number | undefined;
  for (const m of data.pages[0]?.items ?? []) {
    if (newest === undefined || m.seq > newest) newest = m.seq;
  }
  return newest;
}

/**
 * Puts a message where the cached copy of it is - the same id, in whichever page holds it - and touches
 * nothing else: an edit or a delete keeps the message's place (EDT-01, DLT-01). A message the pages do not hold
 * is not added: its first fetch has it as it is.
 */
export function replaceMessage(
  data: MessagePages | undefined,
  m: Message,
): MessagePages | undefined {
  if (!data) return data;
  let found = false;
  const pages = data.pages.map((page) => {
    const index = page.items.findIndex((x) => x.id === m.id);
    if (index < 0) return page;
    found = true;
    const items = page.items.slice();
    items[index] = m;
    return { ...page, items };
  });
  return found ? { ...data, pages } : data;
}

/** The message with this id, in whichever page of the timeline holds it. */
export function findMessage(data: MessagePages | undefined, id: string): Message | undefined {
  for (const page of data?.pages ?? []) {
    const found = page.items.find((m) => m.id === id);
    if (found) return found;
  }
  return undefined;
}
