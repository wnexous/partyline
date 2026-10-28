import { toast } from "sonner";
import { useDeleteMessage } from "@/entities/message";
import type { Message } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";

/*
 * The question that comes before a delete (DLT-09). The rules of what may be deleted are in
 * entities/message (model/permissions.ts); the menu that offers it is the message's.
 */

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
