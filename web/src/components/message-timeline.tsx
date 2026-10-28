import type { InfiniteData } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDownIcon, MessagesSquareIcon } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { collectImages, ImageViewer } from "@/components/image-viewer";
import { MessageItem } from "@/components/message-item";
import {
  hasMessageActions,
  type MessageCapabilities,
  MessageMenu,
  type MessageMenuTarget,
  messageActionSet,
} from "@/components/message-menu";
import { useReact } from "@/hooks/use-react";
import { useStickToEnd } from "@/hooks/use-stick-to-end";
import { useRoomMessages } from "@/lib/api/queries";
import type { Message, Page, RoomMember } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { useTouchFirst } from "@/shared/lib/device";
import { dayKey, formatDayLabel } from "@/shared/lib/time";
import { cn } from "@/shared/lib/utils";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

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

/**
 * The room's messages by id, as the timeline has them now: what a reply, an edit and the actions of
 * a message follow, so an edit or a deletion that arrives live shows through (EDT-06, DLT-06).
 */
export function useTimelineMessages(roomId: string): Map<string, Message> {
  const query = useRoomMessages(roomId);
  return useMemo(() => new Map(flattenPages(query.data).map((m) => [m.id, m])), [query.data]);
}

function isGrouped(prev: Message | undefined, m: Message): boolean {
  if (!prev || m.reply_to) return false;
  // A question and an answer always show their author and time (QR-10, QR-12).
  if (m.interactive || m.answers) return false;
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
  /**
   * Makes a message the one the next message answers (ACT-07). Absent where the viewer may not
   * post: the actions of a message then offer no Reply.
   */
  onReply?: (message: Message) => void;
  /** The viewer manages this group: they delete any message of it (DLT-02). */
  canModerate?: boolean;
  /** Starts editing a message in the composer (EDT-10). */
  onEdit?: (message: Message) => void;
  /** The viewer may react to the messages (RCT-03): they may post. Without it the chips are only shown. */
  canReact?: boolean;
  /** The conversation is history (CID-21): nothing in it is answered, edited, deleted or reacted to; Copy text stays. */
  frozen?: boolean;
}

export function MessageTimeline({
  roomId,
  meId,
  members,
  focus,
  className,
  onReply,
  canModerate,
  onEdit,
  canReact,
  frozen,
}: MessageTimelineProps) {
  const { t, locale } = useI18n();
  // The actions of a message (ACT-01, ACT-02, ACT-05): one menu for the whole timeline, opened by
  // a long press on a touch device and by a right click or the "⋯" with a mouse.
  const touch = useTouchFirst();
  const [menu, setMenu] = useState<MessageMenuTarget | null>(null);
  // The actions belong to one conversation: reaching another one closes them, and the question a
  // delete may have left pending (the caller's key may not have remounted this timeline).
  const [menuRoom, setMenuRoom] = useState(roomId);
  if (menuRoom !== roomId) {
    setMenuRoom(roomId);
    setMenu(null);
  }
  const openMenu = useCallback(
    (message: Message, at: MessageMenuTarget["at"], opener: HTMLElement) =>
      setMenu({ message, at, opener }),
    [],
  );
  const closeMenu = useCallback(() => setMenu(null), []);
  // What the viewer may do in this conversation decides what each message offers (ACT-05, EDT-10, DLT-09).
  const capabilities = useMemo<MessageCapabilities>(
    () => ({
      meId,
      canReply: onReply !== undefined,
      canModerate: canModerate ?? false,
      canEdit: onEdit !== undefined,
      canReact: canReact ?? false,
      frozen: frozen ?? false,
    }),
    [meId, onReply, canModerate, onEdit, canReact, frozen],
  );
  const react = useReact(roomId, meId);
  const query = useRoomMessages(roomId);
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = query;
  const items = useMemo(() => flattenPages(query.data), [query.data]);
  const byId = useMemo(() => new Map(items.map((m) => [m.id, m])), [items]);
  const memberMap = useMemo(() => new Map((members ?? []).map((m) => [m.member_id, m])), [members]);
  // The answers the timeline holds, by the question they reply to and their author (QR-12). The
  // viewer's own answer is what makes a question read as answered, on this device and after a reload.
  const answersByQuestion = useMemo(() => {
    const out = new Map<string, Map<string, Message>>();
    for (const m of items) {
      if (!m.answers || !m.reply_to || !m.author_id) continue;
      const byAuthor = out.get(m.reply_to) ?? new Map<string, Message>();
      byAuthor.set(m.author_id, m);
      out.set(m.reply_to, byAuthor);
    }
    return out;
  }, [items]);

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

  // Where the reader is: at the newest message or not. The ref is what a resize of the box reads
  // (the keyboard opening is no scroll, so the state it would read has not moved).
  const [atEnd, setAtEndState] = useState(true);
  const atEndRef = useRef(true);
  const setAtEnd = useCallback((value: boolean) => {
    atEndRef.current = value;
    setAtEndState(value);
  }, []);
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
  }, [hasNextPage, isFetchingNextPage, fetchNextPage, setAtEnd]);

  // The box changes height when the on-screen keyboard opens or closes (KBD-02): a reader at the
  // newest message stays at it, one higher up keeps their place.
  const [scroller, setScroller] = useState<HTMLDivElement | null>(null);
  const scrollerRef = useCallback((el: HTMLDivElement | null) => {
    scrollRef.current = el;
    setScroller(el);
  }, []);
  useStickToEnd(
    scroller,
    () => atEndRef.current,
    () => {
      const el = scrollRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    },
  );

  const jumpTo = useCallback(
    (id: string) => {
      const index = items.findIndex((m) => m.id === id);
      if (index >= 0) virtualizer.scrollToIndex(index, { align: "center" });
    },
    [items, virtualizer],
  );

  // The image viewer (IMG-01..IMG-06) walks the conversation's images as far as they are loaded.
  // The timeline behind it follows the image on show, so closing it lands on that image's
  // thumbnail and focus can go back to it (IMG-03).
  const images = useMemo(() => collectImages(items), [items]);
  const [viewing, setViewing] = useState<string | null>(null);
  // On the oldest image loaded, Previous would stop at the edge of what happens to be loaded: the
  // older pages are fetched meanwhile (page after page while they hold no image), until one brings
  // an image, the history ends or a request fails.
  const viewingOldest = viewing !== null && images[0]?.id === viewing;
  const olderFailed = query.isFetchNextPageError;
  const oldestSeq = items[0]?.seq;
  // biome-ignore lint/correctness/useExhaustiveDependencies: it runs again when a page arrives (the oldest message changes), even one with no image, and React may never render the fetch in between
  useEffect(() => {
    if (viewingOldest && hasNextPage && !isFetchingNextPage && !olderFailed) void fetchNextPage();
  }, [viewingOldest, hasNextPage, isFetchingNextPage, olderFailed, fetchNextPage, oldestSeq]);
  const openImage = useCallback((attachmentId: string) => setViewing(attachmentId), []);
  const showImage = useCallback(
    (attachmentId: string | null) => {
      setViewing(attachmentId);
      const image = images.find((i) => i.id === attachmentId);
      if (image) jumpTo(image.messageId);
    },
    [images, jumpTo],
  );
  const thumbnailOf = useCallback(
    (attachmentId: string) =>
      Array.from(
        scrollRef.current?.querySelectorAll<HTMLElement>("[data-attachment-id]") ?? [],
      ).find((el) => el.dataset.attachmentId === attachmentId) ?? scrollRef.current,
    [],
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

  // A refresh that fails (an older page that cannot be loaded) keeps the messages on screen, as the
  // conversation list does: only a timeline that never loaded is an error.
  if (query.isError && !query.data) {
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
        ref={scrollerRef}
        role="log"
        aria-label={t("timeline.label")}
        aria-busy={isFetchingNextPage}
        // biome-ignore lint/a11y/noNoninteractiveTabindex: the log scrolls; keyboard users need to reach it
        tabIndex={0}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain outline-none [overflow-anchor:none] focus-visible:ring-2 focus-visible:ring-ring/70 focus-visible:ring-inset"
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
                        {formatDayLabel(m.created_at, locale, {
                          today: t("timeline.today"),
                          yesterday: t("timeline.yesterday"),
                        })}
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
                    onOpenImage={openImage}
                    onMenu={hasMessageActions(m, capabilities) ? openMenu : undefined}
                    touch={touch}
                    menuOpen={menu?.message.id === m.id}
                    onSwipeReply={messageActionSet(m, capabilities).reply ? onReply : undefined}
                    onReact={messageActionSet(m, capabilities).react ? react : undefined}
                    myAnswer={answersByQuestion.get(m.id)?.get(meId)}
                    // Reacting is offered to whoever may post (RCT-03): the same permission answers.
                    canAnswer={canReact}
                    frozen={frozen}
                  />
                </div>
              );
            })}
          </div>
          <div className="h-3" />
        </div>
      </div>
      <MessageMenu
        key={roomId}
        target={menu}
        messages={byId}
        onClose={closeMenu}
        touch={touch}
        capabilities={capabilities}
        onReply={onReply}
        onEdit={onEdit}
        onReact={react}
      />
      <ImageViewer
        images={images}
        currentId={viewing}
        onCurrentChange={showImage}
        returnFocusTo={thumbnailOf}
        more={hasNextPage}
      />
      {/* A strip of its own, between the messages and the composer: nothing is laid over a message (KBD-06). */}
      {!atEnd ? (
        <div className="flex shrink-0 justify-center px-4 py-2 md:px-6">
          <Button size="sm" variant="secondary" onClick={jumpToLatest} className="shadow-md">
            <ArrowDownIcon aria-hidden="true" />
            {t("timeline.jumpLatest")}
          </Button>
        </div>
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
