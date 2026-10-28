import { ReplyIcon, XIcon } from "lucide-react";
import { authorDisplayName } from "@/entities/message";
import type { Message } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";
import { keepFocus } from "../lib/keep-focus";

/*
 * The message the next one answers (ACT-07), shown above the text box with a way to take it back.
 * Taking it back keeps what was typed.
 */
export function ReplyStrip({
  id,
  replyTo,
  excerpt,
  onClear,
}: {
  id: string;
  replyTo: Message;
  /** The text of the message, or what stands for it (a voice message, a file). */
  excerpt: string;
  onClear?: () => void;
}) {
  const { t } = useI18n();
  return (
    <div id={id} data-testid="reply-strip" className="flex items-center gap-2.5 border-b px-3 py-2">
      <ReplyIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0 flex-1 text-xs leading-snug">
        <p className="truncate font-medium">
          {t("composer.replyingTo", { name: authorDisplayName(replyTo) })}
        </p>
        {excerpt ? <p className="truncate text-muted-foreground">{excerpt}</p> : null}
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label={t("composer.cancelReply")}
        // Taking it back must not close the keyboard of someone who is typing (KBD-03).
        onPointerDown={keepFocus}
        onMouseDown={keepFocus}
        onClick={onClear}
        className="text-muted-foreground pointer-coarse:size-11"
      >
        <XIcon aria-hidden="true" />
      </Button>
    </div>
  );
}
