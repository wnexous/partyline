import { CopyIcon, ReplyIcon } from "lucide-react";
import { useRef } from "react";
import { toast } from "sonner";
import { ActionMenu, type MenuAction } from "@/components/action-menu";
import { authorDisplayName } from "@/components/message-item";
import type { Message } from "@/lib/api/types.gen";
import { copyText } from "@/lib/clipboard";
import { useT } from "@/lib/i18n";
import { messageExcerpt } from "@/lib/message-excerpt";

/*
 * The actions of a message (ACT-05): Reply, and Copy text when it has any. There is no Delete: the
 * API has no route to delete or edit a message, so the menu offers none until one exists (the
 * items are a list on purpose, so that the actions of such a route join it). A system notice
 * offers nothing: it is no one's to answer.
 */

/** What a message's menu is open for. */
export interface MessageMenuTarget {
  message: Message;
  /** Where a mouse menu hangs: the pointer, or the edge of the button that opened it. */
  at: { x: number; y: number; align?: "start" | "end" };
  /** The row, or the "⋯" that opened it: it takes focus back when the menu closes. */
  opener: HTMLElement | null;
}

/** Whether a message has anything to offer; `canReply` is whether the viewer may post in the room. */
export function hasMessageActions(message: Message, canReply: boolean): boolean {
  if (message.author_kind === "system") return false;
  return canReply || message.text.trim() !== "";
}

export function MessageMenu({
  target,
  onClose,
  touch,
  onReply,
}: {
  target: MessageMenuTarget | null;
  onClose: () => void;
  touch: boolean;
  /** Absent where the viewer may not post: there is no composer to answer in. */
  onReply?: (message: Message) => void;
}) {
  const t = useT();
  // The last target stays drawn while the menu fades out.
  const last = useRef<MessageMenuTarget | null>(null);
  if (target) last.current = target;
  const shown = last.current;
  // Choosing Reply sends focus to the composer; the menu must not take it back.
  const handedOver = useRef(false);
  if (!shown) return null;

  const { message } = shown;
  const name = authorDisplayName(message);
  const actions: MenuAction[] = [];
  if (onReply) {
    actions.push({
      id: "reply",
      label: t("actions.reply"),
      icon: ReplyIcon,
      onSelect: () => {
        handedOver.current = true;
        onReply(message);
      },
    });
  }
  if (message.text.trim() !== "") {
    actions.push({
      id: "copy",
      label: t("actions.copyText"),
      icon: CopyIcon,
      onSelect: () => {
        // At the top of the screen, for a moment: the composer is at the bottom, whatever its height.
        void copyText(message.text).then((copied) =>
          copied
            ? toast(t("actions.copied"), { position: "top-center", duration: 2_000 })
            : toast.error(t("actions.copyFailed"), { position: "top-center" }),
        );
      },
    });
  }

  return (
    <ActionMenu
      open={target !== null}
      onClose={onClose}
      touch={touch}
      title={name}
      label={t("actions.message", { name })}
      description={messageExcerpt(message, t("player.voice")) || undefined}
      at={shown.at}
      actions={actions}
      onCloseFocus={(event) => {
        event.preventDefault();
        if (handedOver.current) {
          handedOver.current = false;
          return;
        }
        shown.opener?.focus({ preventScroll: true });
      }}
    />
  );
}
