import {
  AudioLinesIcon,
  FileTextIcon,
  FilmIcon,
  ImageIcon,
  type LucideIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { formatBytes } from "@/components/message-item";
import { Button } from "@/components/ui/button";
import { fileKindOf } from "@/lib/api/queries";
import { type FileKind, FileKindImage } from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";

/*
 * What the composer is about to send (UI-07): one card per picked, pasted or dropped file. An image
 * shows a thumbnail made from an object URL that lives exactly as long as the card, so removing the
 * file, sending it or leaving the page releases it. Everything else shows the icon of its kind with
 * its name and size; a video or an audio file is never played here. The row scrolls sideways instead
 * of wrapping, which keeps the composer's height steady on a phone.
 */

/** Files of this kind get a thumbnail; the rest get an icon. */
const isImageKind = (kind: FileKind) => kind === FileKindImage;

const KIND_ICON: Record<FileKind, LucideIcon> = {
  image: ImageIcon,
  audio: AudioLinesIcon,
  video: FilmIcon,
  document: FileTextIcon,
};

/** One attachment's preview. `onRemove` takes it out of the composer. */
export function AttachmentPreview({ file, onRemove }: { file: File; onRemove: () => void }) {
  const { t, locale } = useI18n();
  const kind = fileKindOf(file.type);
  const size = formatBytes(file.size, locale);
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
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-xs font-medium" title={file.name}>
            {file.name}
          </span>
          <span className="text-[0.7rem] text-muted-foreground">{size}</span>
        </span>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("composer.removeFile", { name: file.name })}
          onClick={onRemove}
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
}: {
  files: File[];
  /** Takes this file out; the rest stay attached. */
  onRemove: (file: File) => void;
}) {
  const { t } = useI18n();
  return (
    <ul
      aria-label={t("composer.attachments")}
      className="flex gap-2 overflow-x-auto overscroll-x-contain px-3 pt-2.5 pb-1"
    >
      {files.map((file) => (
        <AttachmentPreview
          key={`${file.name}-${file.size}-${file.lastModified}`}
          file={file}
          onRemove={() => onRemove(file)}
        />
      ))}
    </ul>
  );
}
