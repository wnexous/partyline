import type { InfiniteData } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDownIcon, MessagesSquareIcon } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { EmptyState } from "@/components/empty-state";
import { MessageItem } from "@/components/message-item";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useRoomMessages } from "@/lib/api/queries";
import type { Message, Page, RoomMember } from "@/lib/api/types.gen";
import { type Translate, useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * The live room timeline (UI-04, UI-05). Pages come from the infinite query (newest page first,
 * each page oldest first); live room.message events append to the cache and show up here without
 * a refetch. Rows are virtualized and anchored to the end: the view follows new messages while
 * the reader is at the bottom and holds still while they read history.
 */

const GROUP_WINDOW_MS = 5 * 60_000;
const LOAD_OLDER_PX = 400;
const AT_END_PX = 64;
/** Height of the "loading older / start of room" strip above the first row. */
const TOP_STRIP_PX = 40;
const FRESH_MS = 1_500;

export function flattenPages(data: InfiniteData<Page<Message>, unknown> | undefined): Message[] {
  if (!data) return [];
  const seen = new Set<string>();
  const out: Message[] = [];
  for (let i = data.pages.length - 1; i >= 0; i--) {
    for (const m of data.pages[i]?.items ?? []) {
      if (seen.has(m.id)) continue;
      seen.add(m.id);
      out.push(m);
    }
  }
  return out.sort((a, b) => a.seq - b.seq);
}

const dayKey = (iso: string) => new Date(iso).toDateString();

function dayLabel(iso: string, t: Translate, locale: string): string {
  const date = new Date(iso);
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (date.toDateString() === today.toDateString()) return t("timeline.today");
  if (date.toDateString() === yesterday.toDateString()) return t("timeline.yesterday");
  return new Intl.DateTimeFormat(locale, { weekday: "long", day: "numeric", month: "long" }).format(
    date,
  );
}

function isGrouped(prev: Message | undefined, m: Message): boolean {
  if (!prev || m.reply_to) return false;
  if (m.author_kind === "chat" || m.author_kind === "system") return false;
  if (prev.author_kind !== m.author_kind || prev.author_id !== m.author_id) return false;
  if (dayKey(prev.created_at) !== dayKey(m.created_at)) return false;
  return Date.parse(m.created_at) - Date.parse(prev.created_at) < GROUP_WINDOW_MS;
}

/** A message to bring into view (files panel "open in timeline"); a new object jumps again. */
export interface MessageFocus {
  id: string;
  seq: number;
}

export interface MessageTimelineProps {
  roomId: string;
  meId: string;
  members?: RoomMember[];
  focus?: MessageFocus | null;
  className?: string;
}

export function MessageTimeline({ roomId, meId, members, focus, className }: MessageTimelineProps) {
  const { t, locale } = useI18n();
  const query = useRoomMessages(roomId);
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = query;
  const items = useMemo(() => flattenPages(query.data), [query.data]);
  const byId = useMemo(() => new Map(items.map((m) => [m.id, m])), [items]);
  const memberMap = useMemo(() => new Map((members ?? []).map((m) => [m.member_id, m])), [members]);

  // Messages newer than everything seen before get the tape-settle highlight once.
  const firstSeen = useRef(new Map<string, number>());
  const maxSeq = useRef<number | null>(null);
  if (query.data) {
    const now = Date.now();
    let max = maxSeq.current ?? 0;
    for (const m of items) {
      if (firstSeen.current.has(m.id)) continue;
      const live = maxSeq.current !== null && m.seq > maxSeq.current;
      firstSeen.current.set(m.id, live ? now : 0);
      if (m.seq > max) max = m.seq;
    }
    maxSeq.current = max;
  }

  const scrollRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 64,
    getItemKey: (index) => items[index]?.id ?? index,
    overscan: 8,
    scrollMargin: TOP_STRIP_PX,
    anchorTo: "end",
    followOnAppend: true,
    scrollEndThreshold: AT_END_PX,
  });

  const [atEnd, setAtEnd] = useState(true);
  const initialized = useRef(false);
  useLayoutEffect(() => {
    if (initialized.current || items.length === 0) return;
    initialized.current = true;
    virtualizer.scrollToIndex(items.length - 1, { align: "end" });
  }, [items.length, virtualizer]);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el || !initialized.current) return;
    setAtEnd(el.scrollHeight - el.scrollTop - el.clientHeight <= AT_END_PX);
    if (el.scrollTop < LOAD_OLDER_PX && hasNextPage && !isFetchingNextPage) {
      void fetchNextPage();
    }
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  const jumpTo = useCallback(
    (id: string) => {
      const index = items.findIndex((m) => m.id === id);
      if (index >= 0) virtualizer.scrollToIndex(index, { align: "center" });
    },
    [items, virtualizer],
  );

  // Bring a focused message into view, loading older pages until its seq is reached; it then
  // gets the tape highlight. A message no longer stored (retention) ends the search.
  const [pending, setPending] = useState<MessageFocus | null>(null);
  const [flash, setFlash] = useState<{ id: string; at: number } | null>(null);
  useEffect(() => {
    if (focus) setPending(focus);
  }, [focus]);
  useLayoutEffect(() => {
    if (!pending || !query.data) return;
    const index = items.findIndex((m) => m.id === pending.id);
    if (index >= 0) {
      virtualizer.scrollToIndex(index, { align: "center" });
      setFlash({ id: pending.id, at: Date.now() });
      setPending(null);
      return;
    }
    const oldest = items[0]?.seq;
    if (hasNextPage && (oldest === undefined || oldest > pending.seq)) {
      if (!isFetchingNextPage) void fetchNextPage();
      return;
    }
    setPending(null);
    toast(t("timeline.messageGone"));
  }, [pending, query.data, items, hasNextPage, isFetchingNextPage, fetchNextPage, virtualizer, t]);

  const jumpToLatest = () => {
    virtualizer.scrollToIndex(items.length - 1, { align: "end" });
    setAtEnd(true);
  };

  if (query.isPending) return <TimelineSkeleton className={className} />;

  if (query.isError) {
    return (
      <div className={cn("flex-1 px-4 py-10 text-sm md:px-6", className)} role="alert">
        <p className="text-destructive">{t("timeline.loadError")}</p>
        <Button variant="outline" size="sm" className="mt-3" onClick={() => query.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  if (items.length === 0) {
    return (
      <div className={cn("flex flex-1 items-center justify-center", className)}>
        <EmptyState icon={MessagesSquareIcon} title={t("timeline.emptyTitle")}>
          <p>{t("timeline.emptyBody")}</p>
        </EmptyState>
      </div>
    );
  }

  const now = Date.now();
  const virtualRows = virtualizer.getVirtualItems();

  return (
    <div className={cn("relative flex min-h-0 flex-1 flex-col", className)}>
      <div
        ref={scrollRef}
        role="log"
        aria-label={t("timeline.label")}
        aria-busy={isFetchingNextPage}
        // biome-ignore lint/a11y/noNoninteractiveTabindex: the log scrolls; keyboard users need to reach it
        tabIndex={0}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain outline-none [overflow-anchor:none] focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:ring-inset"
      >
        <div className="flex min-h-full flex-col">
          <div className="flex-1" />
          <div
            className="flex items-center justify-center text-xs text-muted-foreground"
            style={{ height: TOP_STRIP_PX }}
          >
            {isFetchingNextPage
              ? t("timeline.loadingOlder")
              : hasNextPage
                ? null
                : t("timeline.start")}
          </div>
          <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
            {virtualRows.map((row) => {
              const m = items[row.index];
              if (!m) return null;
              const prev = items[row.index - 1];
              const newDay = !prev || dayKey(prev.created_at) !== dayKey(m.created_at);
              const seenAt = firstSeen.current.get(m.id) ?? 0;
              return (
                <div
                  key={row.key}
                  data-index={row.index}
                  ref={virtualizer.measureElement}
                  className="absolute top-0 left-0 w-full"
                  style={{ transform: `translateY(${row.start - TOP_STRIP_PX}px)` }}
                >
                  {newDay ? (
                    <div className="flex items-center gap-3 px-4 pt-4 pb-1 md:px-6">
                      <span className="label-caps text-[0.68rem] text-muted-foreground">
                        {dayLabel(m.created_at, t, locale)}
                      </span>
                      <span aria-hidden="true" className="h-px flex-1 bg-border" />
                    </div>
                  ) : null}
                  <MessageItem
                    message={m}
                    meId={meId}
                    members={memberMap}
                    replyTarget={m.reply_to ? (byId.get(m.reply_to) ?? null) : undefined}
                    grouped={!newDay && isGrouped(prev, m)}
                    fresh={
                      (seenAt > 0 && now - seenAt < FRESH_MS) ||
                      (flash?.id === m.id && now - flash.at < FRESH_MS)
                    }
                    onJumpTo={jumpTo}
                  />
                </div>
              );
            })}
          </div>
          <div className="h-3" />
        </div>
      </div>
      {!atEnd ? (
        <Button
          size="sm"
          variant="secondary"
          onClick={jumpToLatest}
          className="absolute right-4 bottom-3 shadow-md md:right-6"
        >
          <ArrowDownIcon aria-hidden="true" />
          {t("timeline.jumpLatest")}
        </Button>
      ) : null}
    </div>
  );
}

function TimelineSkeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={cn("flex flex-1 flex-col justify-end gap-5 px-4 py-6 md:px-6", className)}
    >
      {[48, 72, 36].map((w) => (
        <div key={w} className="grid grid-cols-[2rem_1fr] gap-3">
          <Skeleton className="size-8" />
          <div className="space-y-2">
            <Skeleton className="h-3.5 w-32" />
            <Skeleton className="h-3.5" style={{ width: `${w}%` }} />
          </div>
        </div>
      ))}
    </div>
  );
}
