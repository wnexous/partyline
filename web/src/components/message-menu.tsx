import { CopyIcon, PencilIcon, ReplyIcon, Trash2Icon } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { ActionMenu, type MenuAction } from "@/components/action-menu";
import {
  canDeleteMessage,
  canEditMessage,
  DeleteMessageDialog,
} from "@/components/message-actions";
import { authorDisplayName } from "@/components/message-item";
import type { Message } from "@/lib/api/types.gen";
import { copyText } from "@/lib/clipboard";
import { useT } from "@/lib/i18n";
import { messageExcerpt } from "@/lib/message-excerpt";

/*
 * The actions of a message (ACT-05, EDT-10, DLT-09): Reply, Copy text when it has any, and for the
 * viewer's own text message Edit; Delete for their own and, in a group they manage, for any message
 * but a system notice, after a question. One list, whether it opens as a sheet or as a menu. A system
 * notice and a deleted message (a placeholder) offer nothing: they are no one's to answer or copy.
 */

/** What a message's menu is open for. */
export interface MessageMenuTarget {
  message: Message;
  /** Where a mouse menu hangs: the pointer, or the edge of the button that opened it. */
  at: { x: number; y: number; align?: "start" | "end" };
  /** The row, or the "⋯" that opened it: it takes focus back when the menu closes. */
  opener: HTMLElement | null;
}

/** What the viewer may do in the conversation, which decides what each message offers. */
export interface MessageCapabilities {
  /** The viewer's id: Edit and Delete are for their own messages. */
  meId: string;
  /** The viewer may post, so there is a composer to answer in. */
  canReply: boolean;
  /** The viewer manages this group: they delete any message of it (DLT-02). */
  canModerate: boolean;
  /** There is a composer to edit in (EDT-10). */
  canEdit: boolean;
  /** The conversation is history (CID-21): nothing in it is edited, deleted or answered. */
  frozen: boolean;
}

export interface MessageActionSet {
  reply: boolean;
  copy: boolean;
  edit: boolean;
  remove: boolean;
}

/** Which actions a message offers this viewer. */
export function messageActionSet(message: Message, c: MessageCapabilities): MessageActionSet {
  if (message.author_kind === "system" || message.deleted_at !== undefined) {
    return { reply: false, copy: false, edit: false, remove: false };
  }
  return {
    reply: c.canReply && !c.frozen,
    copy: message.text.trim() !== "",
    edit: c.canEdit && !c.frozen && canEditMessage(message, c.meId),
    remove: !c.frozen && canDeleteMessage(message, c.meId, c.canModerate),
  };
}

/** Whether a message has anything to offer: a row with nothing leaves the browser's own menu alone. */
export function hasMessageActions(message: Message, c: MessageCapabilities): boolean {
  const set = messageActionSet(message, c);
  return set.reply || set.copy || set.edit || set.remove;
}

export function MessageMenu({
  target,
  onClose,
  touch,
  capabilities,
  onReply,
  onEdit,
}: {
  target: MessageMenuTarget | null;
  onClose: () => void;
  touch: boolean;
  capabilities: MessageCapabilities;
  /** Makes the message the one the next answers (ACT-07). */
  onReply?: (message: Message) => void;
  /** Starts editing the message in the composer (EDT-10). */
  onEdit?: (message: Message) => void;
}) {
  const t = useT();
  // The last target stays drawn while the menu fades out.
  const last = useRef<MessageMenuTarget | null>(null);
  if (target) last.current = target;
  const shown = last.current;
  const [deleting, setDeleting] = useState<{ message: Message; opener: HTMLElement | null } | null>(
    null,
  );
  const [asking, setAsking] = useState(false);
  // Choosing Reply or Edit sends focus to the composer, and Delete to its question: the menu must
  // not take it back.
  const handedOver = useRef(false);

  const message = shown?.message;
  const set = message ? messageActionSet(message, capabilities) : null;
  const name = message ? authorDisplayName(message) : "";
  const actions: MenuAction[] = [];
  if (message && shown && set) {
    if (set.reply && onReply) {
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
    if (set.copy) {
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
    if (set.edit && onEdit) {
      actions.push({
        id: "edit",
        label: t("message.edit"),
        icon: PencilIcon,
        onSelect: () => {
          handedOver.current = true;
          onEdit(message);
        },
      });
    }
    if (set.remove) {
      actions.push({
        id: "delete",
        label: t("message.delete"),
        icon: Trash2Icon,
        destructive: true,
        onSelect: () => {
          handedOver.current = true;
          setDeleting({ message, opener: shown.opener });
          setAsking(true);
        },
      });
    }
  }

  return (
    <>
      {shown && message ? (
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
      ) : null}
      {deleting ? (
        <DeleteMessageDialog
          message={deleting.message}
          open={asking}
          onOpenChange={setAsking}
          onCloseFocus={(event) => {
            event.preventDefault();
            deleting.opener?.focus({ preventScroll: true });
          }}
        />
      ) : null}
    </>
  );
}
