import {
  AudioLinesIcon,
  FilesIcon,
  FileTextIcon,
  FileXIcon,
  FilmIcon,
  ImageIcon,
  type LucideIcon,
  MessageSquareShareIcon,
} from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { formatBytes, INLINE_IMAGES } from "@/components/message-item";
import { IntelligenceName } from "@/entities/intelligence";
import { attachmentUrl } from "@/entities/message";
import { useRoomFiles } from "@/entities/room";
import type { FileFilter } from "@/shared/api/cache-types";
import type { FileKind, RoomFile } from "@/shared/api/types.gen";
import { type MessageKey, useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/shared/ui/primitives/toggle-group";

/*
 * The room's files (FILES-04..06): everything shared in the room, newest first, so a document
 * from last week is a filter and a glance away instead of a long scroll. Files the server could
 * not store stay listed (so nobody wonders whether they were sent) but carry no link. Live
 * room.message events prepend new files into the cached pages (app/live/dispatch.ts).
 */

const FILTERS: { value: FileFilter; label: MessageKey }[] = [
  { value: "all", label: "files.filter.all" },
  { value: "image", label: "files.filter.image" },
  { value: "audio", label: "files.filter.audio" },
  { value: "video", label: "files.filter.video" },
  { value: "document", label: "files.filter.document" },
];

const KIND_ICON: Record<FileKind, LucideIcon> = {
  image: ImageIcon,
  audio: AudioLinesIcon,
  video: FilmIcon,
  document: FileTextIcon,
};

/** A message to bring into view in the timeline. */
export interface MessageTarget {
  id: string;
  seq: number;
}

export function RoomFiles({
  roomId,
  onOpenMessage,
}: {
  roomId: string;
  onOpenMessage: (target: MessageTarget) => void;
}) {
  const { t } = useI18n();
  const [filter, setFilter] = useState<FileFilter>("all");
  const query = useRoomFiles(roomId, filter);
  const files = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);

  // Files that arrive after the first load settle in with the tape highlight, once.
  const seen = useRef<Set<string> | null>(null);
  const fresh = new Set<string>();
  if (query.data) {
    if (seen.current === null) seen.current = new Set(files.map((f) => f.id));
    for (const f of files) {
      if (!seen.current.has(f.id)) {
        seen.current.add(f.id);
        fresh.add(f.id);
      }
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ToggleGroup
        type="single"
        variant="outline"
        size="sm"
        spacing={1}
        value={filter}
        onValueChange={(v) => {
          if (!v) return;
          seen.current = null;
          setFilter(v as FileFilter);
        }}
        aria-label={t("files.filterLabel")}
        className="flex w-full flex-wrap gap-1 border-b px-3 py-2"
      >
        {FILTERS.map(({ value, label }) => (
          <ToggleGroupItem
            key={value}
            value={value}
            className="h-7 flex-none rounded-[3px] px-2 text-xs data-[state=on]:border-tape-strong data-[state=on]:bg-tape/60 data-[state=on]:text-tape-foreground"
          >
            {t(label)}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>

      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        {query.isPending ? (
          <div className="space-y-3 p-4" aria-hidden="true">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-center gap-3">
                <Skeleton className="size-10 shrink-0" />
                <div className="flex-1 space-y-1.5">
                  <Skeleton className="h-3.5 w-36" />
                  <Skeleton className="h-3 w-24" />
                </div>
              </div>
            ))}
          </div>
        ) : query.isError ? (
          <div className="px-4 py-6 text-sm" role="alert">
            <p className="text-destructive">{t("files.loadError")}</p>
            <Button variant="outline" size="sm" className="mt-3" onClick={() => query.refetch()}>
              {t("common.retry")}
            </Button>
          </div>
        ) : files.length === 0 ? (
          <FilesEmpty filtered={filter !== "all"} />
        ) : (
          <>
            <ul aria-label={t("files.listLabel")} className="py-1.5">
              {files.map((f) => (
                <FileRow
                  key={f.id}
                  file={f}
                  fresh={fresh.has(f.id)}
                  onOpen={() => onOpenMessage({ id: f.message_id, seq: f.message_seq })}
                />
              ))}
            </ul>
            {query.hasNextPage ? (
              <div className="px-3 pb-4">
                <Button
                  variant="ghost"
                  size="sm"
                  className="w-full text-muted-foreground"
                  disabled={query.isFetchingNextPage}
                  onClick={() => query.fetchNextPage()}
                >
                  {query.isFetchingNextPage ? t("files.loadingMore") : t("files.loadMore")}
                </Button>
              </div>
            ) : null}
          </>
        )}
      </div>
    </div>
  );
}

function FilesEmpty({ filtered }: { filtered: boolean }) {
  const { t } = useI18n();
  return (
    <div className="flex flex-col items-center px-6 py-12 text-center">
      <div className="relative mb-4 flex h-10 w-20 items-center justify-center" aria-hidden="true">
        <span className="absolute inset-x-0 top-1/2 h-6 -translate-y-1/2 -rotate-2 rounded-[3px] bg-tape/80 shadow-sm" />
        <FilesIcon className="relative size-5 text-tape-foreground" strokeWidth={1.75} />
      </div>
      <h3 className="text-sm font-semibold">
        {filtered ? t("files.emptyFilteredTitle") : t("files.emptyTitle")}
      </h3>
      <p className="mt-1 max-w-[16rem] text-xs text-pretty text-muted-foreground">
        {filtered ? t("files.emptyFilteredBody") : t("files.emptyBody")}
      </p>
    </div>
  );
}

function FileRow({
  file: f,
  fresh,
  onOpen,
}: {
  file: RoomFile;
  fresh: boolean;
  onOpen: () => void;
}) {
  const { t, locale } = useI18n();
  const stored = f.status === "stored";
  const url = attachmentUrl(f.id);
  const inline = stored && INLINE_IMAGES.has(f.mime);
  const Icon = stored ? KIND_ICON[f.kind] : FileXIcon;

  return (
    <li
      data-testid={`file-${f.id}`}
      className={cn(
        "group/file flex items-center gap-3 px-3 py-2 hover:bg-accent/40",
        fresh && "animate-tape-settle",
      )}
    >
      <span
        className={cn(
          "flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-[3px] border",
          stored ? "bg-card shadow-xs" : "border-dashed bg-transparent text-muted-foreground",
        )}
      >
        {inline ? (
          <img
            src={url}
            alt=""
            width={40}
            height={40}
            loading="lazy"
            decoding="async"
            className="size-full object-cover"
          />
        ) : (
          <Icon className="size-4.5 text-muted-foreground" strokeWidth={1.75} aria-hidden="true" />
        )}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          {stored ? (
            <a
              href={url}
              {...(inline
                ? { target: "_blank", rel: "noopener noreferrer" }
                : { download: f.name })}
              className="min-w-0 flex-1 truncate text-sm font-medium underline-offset-2 hover:underline"
              title={f.name}
            >
              {f.name}
            </a>
          ) : (
            <p
              className="min-w-0 flex-1 truncate text-sm font-medium text-muted-foreground"
              title={f.name}
            >
              {f.name}
            </p>
          )}
          <RelativeTime
            value={f.created_at}
            locale={locale}
            className="shrink-0 text-[0.7rem] text-muted-foreground"
          />
        </div>
        <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
          {stored ? (
            <span className="shrink-0 font-mono tabular">{formatBytes(f.size, locale)}</span>
          ) : (
            <span className="shrink-0 rounded-[3px] border border-dashed px-1 text-[0.68rem]">
              {f.status === "too_large"
                ? t("message.attachmentTooLarge")
                : t("message.attachmentUnavailable")}
            </span>
          )}
          <span aria-hidden="true">·</span>
          <FileAuthor file={f} />
        </p>
      </div>
      <Button
        variant="ghost"
        size="icon-xs"
        className="shrink-0 text-muted-foreground opacity-70 group-hover/file:opacity-100 focus-visible:opacity-100"
        aria-label={t("files.openInTimeline", { name: f.name })}
        title={t("files.openInTimelineShort")}
        onClick={onOpen}
      >
        <MessageSquareShareIcon aria-hidden="true" />
      </Button>
    </li>
  );
}

/**
 * Who wrote a file: its author's name (PROF-09), the same for an AI as for a person. The name is
 * the one the timeline shows. A chat account the server could not resolve to an intelligence has
 * no id and keeps only that name.
 */
function FileAuthor({ file }: { file: RoomFile }) {
  const { author } = file;
  if (!author.id) return <span className="truncate">{file.author_name}</span>;
  return (
    <IntelligenceName
      intelligence={{ ...author, display_name: file.author_name || author.display_name }}
      profileLink
    />
  );
}
