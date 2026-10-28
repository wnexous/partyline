import { toast } from "sonner";
import { useDeleteMessage } from "@/lib/api/queries";
import type { Message } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";

/*
 * What a viewer may do with one message: edit their own text (EDT-02, EDT-03) and delete their own or, in a
 * group they manage, anyone's but a system notice (DLT-02). The server decides; these rules only keep the menu
 * from offering what it would refuse. The menu itself is the message's (components/message-menu.tsx), which
 * opens as a sheet on a touch device and as a menu with a mouse; this file holds the rules and the question
 * that comes before a delete.
 */

/** The viewer wrote the message here: a person's own message, not a chat's or an AI's, whoever they host. */
export const isOwnMessage = (m: Message, meId: string) =>
  m.author_kind === "user" && m.author_id === meId;

/** Edit: the viewer's own text message that is still there (EDT-02, EDT-03). */
export const canEditMessage = (m: Message, meId: string) =>
  isOwnMessage(m, meId) && m.kind === "text" && m.deleted_at === undefined;

/**
 * Delete: the viewer's own message and, when `canModerate` (they manage the group; nobody manages a DM), any
 * other, but never a system notice and never one that is deleted already (DLT-02, DLT-03).
 */
export const canDeleteMessage = (m: Message, meId: string, canModerate: boolean) =>
  m.deleted_at === undefined &&
  m.author_kind !== "system" &&
  (isOwnMessage(m, meId) || canModerate);

/**
 * Asks before deleting (DLT-09): the message goes for everyone, files included, and it cannot be undone. It
 * stays open while the request runs and when it fails, so the person can try again or give up; a failure is
 * said in a toast and the message stays as it was.
 */
export function DeleteMessageDialog({
  message,
  open,
  onOpenChange,
  onCloseFocus,
}: {
  message: Message;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Where focus goes when it closes; `preventDefault()` it to place it yourself. */
  onCloseFocus?: (event: Event) => void;
}) {
  const t = useT();
  const remove = useDeleteMessage(message.room_id);
  return (
    <ConfirmDialog
      open={open}
      onClose={() => onOpenChange(false)}
      title={t("message.deleteTitle")}
      body={t("message.deleteBody")}
      confirmLabel={t("message.deleteConfirm")}
      pending={remove.isPending}
      onConfirm={() =>
        remove.mutate(message.id, {
          onSuccess: () => onOpenChange(false),
          onError: () => toast.error(t("message.deleteFailed")),
        })
      }
      onCloseFocus={onCloseFocus}
    />
  );
}
