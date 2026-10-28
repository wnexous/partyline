import { CornerDownRightIcon } from "lucide-react";
import type { Message } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { authorDisplayName } from "../model/author";

export function ReplyContext({
  target,
  onJumpTo,
}: {
  target: Message | null;
  onJumpTo?: (id: string) => void;
}) {
  const { t } = useI18n();
  const body = target ? (
    <>
      <span className="font-medium text-foreground/80">{authorDisplayName(target)}</span>{" "}
      {target.deleted_at !== undefined ? (
        <span className="truncate italic">{t("message.deleted")}</span>
      ) : (
        <span className="truncate">{target.text || ""}</span>
      )}
    </>
  ) : (
    <span>{t("message.replyMissing")}</span>
  );
  const className =
    "my-0.5 flex max-w-full min-w-0 items-center gap-1.5 truncate text-left text-xs text-muted-foreground";
  return target && onJumpTo ? (
    <button
      type="button"
      data-testid="reply-context"
      className={cn(className, "rounded-sm hover:text-foreground focus-visible:outline-2")}
      onClick={() => onJumpTo(target.id)}
      aria-label={t("message.replyTo", { name: authorDisplayName(target) })}
    >
      <CornerDownRightIcon className="size-3 shrink-0" aria-hidden="true" />
      {body}
    </button>
  ) : (
    <p data-testid="reply-context" className={className}>
      <CornerDownRightIcon className="size-3 shrink-0" aria-hidden="true" />
      {body}
    </p>
  );
}
