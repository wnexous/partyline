import { CopyIcon, PencilIcon, ReplyIcon, Trash2Icon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import {
  canDeleteMessage,
  canEditMessage,
  DeleteMessageDialog,
} from "@/components/message-actions";
import { ReactionBar, ReactionMenuRow } from "@/components/reaction-bar";
import { ReactionPicker, MORE_REACTIONS, PICKER_COLUMNS } from "@/features/react-to-message";
import { authorDisplayName, messageExcerpt } from "@/entities/message";
import type { Message } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { copyText } from "@/shared/lib/clipboard";
import { ActionMenu, type MenuAction } from "@/shared/ui/molecules/action-menu";
import { EmojiGrid } from "@/shared/ui/molecules/emoji-grid";
import { PopoverAnchor } from "@/shared/ui/primitives/popover";

/*
 * The actions of a message (ACT-05, EDT-10, DLT-09, RCT-02): a bar of six quick reactions with a way to
 * the rest, then Reply, Copy text when it has any, and for the viewer's own text message Edit; Delete for
 * their own and, in a group they manage, for any message but a system notice, after a question. One list,
 * whether it opens as a sheet or as a menu. A system notice and a deleted message (a placeholder) offer
 * nothing: they are no one's to answer, copy or react to.
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
  /** The viewer may react: they may post, and the conversation takes reactions (RCT-03). */
  canReact: boolean;
  /** The conversation is history (CID-21): nothing in it is edited, deleted, answered or reacted to. */
  frozen: boolean;
}

export interface MessageActionSet {
  react: boolean;
  reply: boolean;
  copy: boolean;
  edit: boolean;
  remove: boolean;
}

/** Which actions a message offers this viewer. */
export function messageActionSet(message: Message, c: MessageCapabilities): MessageActionSet {
  if (message.author_kind === "system" || message.deleted_at !== undefined) {
    return { react: false, reply: false, copy: false, edit: false, remove: false };
  }
  return {
    react: c.canReact && !c.frozen,
    reply: c.canReply && !c.frozen,
    copy: message.text.trim() !== "",
    edit: c.canEdit && !c.frozen && canEditMessage(message, c.meId),
    remove: !c.frozen && canDeleteMessage(message, c.meId, c.canModerate),
  };
}

/** Whether a message has anything to offer: a row with nothing leaves the browser's own menu alone. */
export function hasMessageActions(message: Message, c: MessageCapabilities): boolean {
  const set = messageActionSet(message, c);
  return set.react || set.reply || set.copy || set.edit || set.remove;
}

export function MessageMenu({
  target,
  messages,
  onClose,
  touch,
  capabilities,
  onReply,
  onEdit,
  onReact,
}: {
  target: MessageMenuTarget | null;
  /** The timeline's messages by id, as they are now: the actions follow the live copy (EDT-06, DLT-06). */
  messages?: Map<string, Message>;
  onClose: () => void;
  touch: boolean;
  capabilities: MessageCapabilities;
  /** Makes the message the one the next answers (ACT-07). */
  onReply?: (message: Message) => void;
  /** Starts editing the message in the composer (EDT-10). */
  onEdit?: (message: Message) => void;
  /** Reacts to the message with the emoji, or takes the viewer's reaction back (RCT-01, RCT-02). */
  onReact?: (message: Message, emoji: string) => void;
}) {
  const t = useT();
  // The last target stays drawn while the menu fades out.
  const last = useRef<MessageMenuTarget | null>(null);
  if (target) last.current = target;
  const shown = last.current;
  // The rest of the emoji (RCT-02): open in the sheet of a touch device, for the target it was opened
  // on, so the next message's sheet starts shut; a popover where the menu was with a mouse.
  const [expandedFor, setExpandedFor] = useState<MessageMenuTarget | null>(null);
  const expanded = target !== null && expandedFor === target;
  const [picking, setPicking] = useState(false);
  const [deleting, setDeleting] = useState<{ message: Message; opener: HTMLElement | null } | null>(
    null,
  );
  const [asking, setAsking] = useState(false);
  // Choosing Reply or Edit sends focus to the composer, and Delete to its question: the menu must
  // not take it back.
  const handedOver = useRef(false);

  // The actions act on the message as it is now, not on the copy taken when they opened: an edit
  // shows through at once, and a message deleted meanwhile (DLT-06) takes them - and the question
  // about deleting it - back with it.
  const live = (m: Message) => messages?.get(m.id) ?? m;
  const message = shown ? live(shown.message) : undefined;
  const retired = message !== undefined && message.deleted_at !== undefined;
  const pending = deleting ? live(deleting.message) : null;
  const pendingRetired = pending !== null && pending.deleted_at !== undefined;
  useEffect(() => {
    if (!retired && !pendingRetired) return;
    setExpandedFor(null);
    setPicking(false);
    setDeleting(null);
    setAsking(false);
    if (retired) onClose();
  }, [retired, pendingRetired, onClose]);

  const set = message ? messageActionSet(message, capabilities) : null;
  const name = message ? authorDisplayName(message) : "";
  const reacted = message
    ? message.reactions.filter((r) => r.by.includes(capabilities.meId)).map((r) => r.emoji)
    : [];
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

  // The quick reactions lead the menu (RCT-02). In the sheet, the rest of the emoji takes the place of
  // the actions under the bar.
  const header =
    set?.react && onReact && message && shown ? (
      touch ? (
        <>
          <ReactionBar
            reacted={reacted}
            expanded={expanded}
            onMore={() => setExpandedFor(expanded ? null : target)}
            onReact={(emoji) => {
              onReact(message, emoji);
              onClose();
            }}
          />
          {expanded ? (
            <div className="px-2 pb-2">
              <EmojiGrid
                emojis={MORE_REACTIONS}
                columns={PICKER_COLUMNS}
                reacted={reacted}
                onPick={(emoji) => {
                  onReact(message, emoji);
                  onClose();
                }}
                label={t("reactions.pick")}
                size="touch"
              />
            </div>
          ) : null}
        </>
      ) : (
        <ReactionMenuRow
          reacted={reacted}
          onReact={(emoji) => onReact(message, emoji)}
          onMore={() => {
            handedOver.current = true;
            setPicking(true);
          }}
        />
      )
    ) : undefined;

  return (
    <>
      {shown && message ? (
        <ActionMenu
          open={target !== null && !retired}
          onClose={onClose}
          touch={touch}
          title={name}
          label={t("actions.message", { name })}
          description={messageExcerpt(message, t("player.voice")) || undefined}
          at={shown.at}
          header={header}
          actions={expanded ? [] : actions}
          onCloseFocus={(event) => {
            event.preventDefault();
            if (handedOver.current) {
              handedOver.current = false;
              return;
            }
            focusMessage(shown.opener);
          }}
        />
      ) : null}
      {shown && message && onReact && !touch ? (
        <ReactionPicker
          open={picking}
          onOpenChange={setPicking}
          reacted={reacted}
          onPick={(emoji) => onReact(message, emoji)}
          // Where the menu was: at the pointer, or under the button that opened it.
          anchor={
            <PopoverAnchor asChild>
              <span
                aria-hidden="true"
                className="pointer-events-none fixed size-0"
                style={{ left: shown.at.x, top: shown.at.y }}
              />
            </PopoverAnchor>
          }
          onCloseFocus={(event) => {
            event.preventDefault();
            focusRow(shown.opener);
          }}
        />
      ) : null}
      {deleting && pending && !pendingRetired ? (
        <DeleteMessageDialog
          message={pending}
          open={asking}
          onOpenChange={setAsking}
          onCloseFocus={(event) => {
            event.preventDefault();
            focusMessage(deleting.opener);
          }}
        />
      ) : null}
    </>
  );
}

/**
 * Takes focus back to the message whose actions closed: the "⋯" that opened them when it has one,
 * else its own row, made focusable for the moment (it joins no Tab order).
 */
function focusMessage(opener: HTMLElement | null): void {
  if (!opener?.isConnected) return;
  const trigger = opener
    .closest<HTMLElement>("article")
    ?.querySelector<HTMLElement>('[aria-haspopup="menu"]');
  if (trigger) {
    trigger.focus({ preventScroll: true });
    return;
  }
  focusRow(opener);
}

/** Takes focus back to a message row, or to whatever opened a surface outside one. */
function focusRow(opener: HTMLElement | null): void {
  if (!opener?.isConnected) return;
  const row = opener.closest<HTMLElement>("article");
  if (!row) {
    opener.focus({ preventScroll: true });
    return;
  }
  // Programmatically focusable, never in the Tab order.
  if (row.tabIndex < 0) row.tabIndex = -1;
  row.focus({ preventScroll: true });
}
