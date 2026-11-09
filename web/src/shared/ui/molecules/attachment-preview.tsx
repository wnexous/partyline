import {
  AudioLinesIcon,
  FileTextIcon,
  FilmIcon,
  ImageIcon,
  type LucideIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { type FileKind, FileKindImage } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { formatBytes } from "@/shared/lib/bytes";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";

/*
 * What the composer is about to send (UI-07): one card per picked, pasted or dropped file. An image
 * shows a thumbnail made from an object URL that lives exactly as long as the card, so removing the
 * file, sending it or leaving the page releases it. Everything else shows the icon of its kind with
 * its name and size; a video or an audio file is never played here. The row scrolls sideways instead
 * of wrapping, which keeps the composer's height steady on a phone.
 *
 * A file that is going up through an upload session (LF-01) shows how far it got, as a bar (a
 * progressbar with its percentage) and as "42% of 3 GB" in place of the size, and its button
 * cancels the upload instead of only removing the file.
 */

/** The kind of a file from its MIME family, document for everything else. */
function fileKindOf(mime: string): FileKind {
  const family = mime.toLowerCase().split("/")[0];
  return family === "image" || family === "audio" || family === "video" ? family : "document";
}

/** Files of this kind get a thumbnail; the rest get an icon. */
const isImageKind = (kind: FileKind) => kind === FileKindImage;

const KIND_ICON: Record<FileKind, LucideIcon> = {
  image: ImageIcon,
  audio: AudioLinesIcon,
  video: FilmIcon,
  document: FileTextIcon,
};

/** A file in the composer with the id it got when it was added; the id keys and removes it. */
export interface AttachedFile {
  id: number;
  file: File;
}

/** How far a file's upload got: bytes the server holds of `total`. */
export interface AttachmentProgress {
  sent: number;
  total: number;
}

/** The whole percent of an upload, 100 only once every byte is there. */
function percentOf({ sent, total }: AttachmentProgress): number {
  if (total <= 0) return 0;
  return sent >= total ? 100 : Math.min(99, Math.floor((sent / total) * 100));
}

/**
 * One attachment's preview. `onRemove` takes it out of the composer; with `progress` the file is
 * uploading and `onCancel` (when given) is what its button does.
 */
export function AttachmentPreview({
  file,
  onRemove,
  progress,
  onCancel,
}: {
  file: File;
  onRemove: () => void;
  progress?: AttachmentProgress;
  onCancel?: () => void;
}) {
  const { t, locale } = useI18n();
  const kind = fileKindOf(file.type);
  const size = formatBytes(file.size, locale);
  const percent = progress ? percentOf(progress) : null;
  const uploading = percent !== null;
  const Icon = KIND_ICON[kind];
  const [url, setUrl] = useState<string | null>(null);

  // The thumbnail's address belongs to this card: it is revoked when the card leaves, which is
  // what removing the file, sending it or closing the page does.
  useEffect(() => {
    if (!isImageKind(kind)) return;
    const created = URL.createObjectURL(file);
    setUrl(created);
    return () => URL.revokeObjectURL(created);
  }, [file, kind]);

  return (
    <li className="max-w-full shrink-0">
      {/* biome-ignore lint/a11y/useSemanticElements: a preview is named as one group; a fieldset brings a border, a legend and a min-width of its own */}
      <div
        role="group"
        aria-label={t("composer.previewLabel", { name: file.name, size })}
        className="flex max-w-[16rem] items-center gap-2 rounded-lg border bg-muted/40 p-1.5"
      >
        <span
          className="flex size-14 shrink-0 items-center justify-center overflow-hidden rounded-md border bg-background text-muted-foreground"
          aria-hidden="true"
        >
          {url ? (
            <img src={url} alt="" decoding="async" className="size-full object-cover" />
          ) : (
            <Icon className="size-5" />
          )}
        </span>
        {/* An uploading card is wide enough for its bar to read, whatever the name's length. */}
        <span className={cn("flex min-w-0 flex-1 flex-col", uploading && "min-w-28")}>
          <span className="truncate text-xs font-medium" title={file.name}>
            {file.name}
          </span>
          <span className="truncate text-[0.7rem] text-muted-foreground tabular">
            {uploading ? t("composer.uploadPercent", { percent, size }) : size}
          </span>
          {uploading ? (
            <span
              role="progressbar"
              aria-label={t("composer.uploading", { name: file.name })}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={percent}
              className="mt-1 block h-1 w-full overflow-hidden rounded-full bg-foreground/15"
            >
              <span
                className="block h-full rounded-full bg-primary transition-[width] duration-300 motion-reduce:transition-none"
                style={{ width: `${percent}%` }}
              />
            </span>
          ) : null}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t(uploading && onCancel ? "composer.cancelUpload" : "composer.removeFile", {
            name: file.name,
          })}
          onClick={uploading && onCancel ? onCancel : onRemove}
          className="size-11 shrink-0 text-muted-foreground"
        >
          <XIcon aria-hidden="true" />
        </Button>
      </div>
    </li>
  );
}

/** The composer's attachment row: every file in the order it was added, scrolling sideways. */
export function AttachmentPreviews({
  files,
  onRemove,
  progressOf,
  onCancel,
}: {
  files: AttachedFile[];
  /** Takes this attachment out by its id; the rest stay attached, even identical files. */
  onRemove: (id: number) => void;
  /** How far the upload of a file got, for a file that is uploading. */
  progressOf?: (id: number) => AttachmentProgress | undefined;
  /** Cancels the upload of a file that is uploading. */
  onCancel?: (id: number) => void;
}) {
  const { t } = useI18n();
  return (
    <ul
      aria-label={t("composer.attachments")}
      className="flex gap-2 overflow-x-auto overscroll-x-contain px-3 pt-2.5 pb-1"
    >
      {files.map(({ id, file }) => (
        <AttachmentPreview
          key={id}
          file={file}
          onRemove={() => onRemove(id)}
          progress={progressOf?.(id)}
          onCancel={onCancel ? () => onCancel(id) : undefined}
        />
      ))}
    </ul>
  );
}
