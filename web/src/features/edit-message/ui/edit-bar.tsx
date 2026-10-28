import { PaperclipIcon, PencilIcon, XIcon } from "lucide-react";
import type { Message } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";

/*
 * The strip of a message being edited (EDT-10): its text, a way to leave the edit, and the files
 * it keeps. The files stay as they are: only the text changes.
 */
export function EditBar({
  id,
  message,
  onLeave,
}: {
  id: string;
  message: Message;
  /** Leaves editing; what was typed in the edit box is dropped. */
  onLeave: () => void;
}) {
  const { t } = useI18n();
  return (
    <>
      <div id={id} className="flex items-center gap-2 border-b px-3 py-1.5 text-xs">
        <PencilIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="shrink-0 font-medium">{t("composer.editing")}</span>
        <span className="min-w-0 flex-1 truncate text-muted-foreground">{message.text}</span>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("composer.cancelEdit")}
          onClick={onLeave}
        >
          <XIcon aria-hidden="true" />
        </Button>
      </div>
      {message.attachments.length > 0 ? (
        // The files of the message stay as they are: only its text changes.
        <ul className="flex flex-wrap gap-1.5 px-3 pt-2.5">
          {message.attachments.map((a) => (
            <li
              key={a.id}
              className="inline-flex max-w-full items-center gap-1 rounded-md border bg-muted px-2 py-0.5 text-xs"
            >
              <PaperclipIcon className="size-3 shrink-0" aria-hidden="true" />
              <span className="truncate">{a.name}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </>
  );
}
