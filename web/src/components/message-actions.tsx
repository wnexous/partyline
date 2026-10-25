import { EllipsisIcon, PencilIcon, Trash2Icon } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useDeleteMessage } from "@/lib/api/queries";
import type { Message } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * What a viewer may do with one message: edit their own text (EDT-02, EDT-03) and delete their own or, in a
 * group they manage, anyone's but a system notice (DLT-02). The server decides; these rules only keep the menu
 * from offering what it would refuse. The "..." button, the items and the confirmation are separate pieces so
 * that a message menu of another shape - a long-press sheet - can offer the same two actions.
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

export interface MessageActionsProps {
  message: Message;
  /** The viewer's id. */
  meId: string;
  /** The viewer manages this group (not a DM): they delete any message of it. */
  canModerate: boolean;
  /** Starts editing the message in the composer; without it there is no composer to edit in and no Edit. */
  onEdit?: (message: Message) => void;
  className?: string;
}

/**
 * The "..." menu of a message row: Edit and Delete, whichever the viewer may use. It renders nothing when they
 * may use neither. The button shows on hover and keyboard focus, and always on a device that cannot hover.
 */
export function MessageActions({
  message,
  meId,
  canModerate,
  onEdit,
  className,
}: MessageActionsProps) {
  const t = useT();
  const [confirming, setConfirming] = useState(false);
  // Choosing Edit moves focus into the composer, and the menu holds focus until it has closed (Radix selects an
  // item before it closes the menu), so editing starts when the menu hands focus back: the composer then keeps
  // it, and the menu does not return it to its button.
  const pendingEdit = useRef<Message | null>(null);
  const canEdit = onEdit !== undefined && canEditMessage(message, meId);
  const canDelete = canDeleteMessage(message, meId, canModerate);
  if (!canEdit && !canDelete) return null;
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-xs"
            className={cn(
              "shrink-0 text-muted-foreground opacity-0 group-hover/message:opacity-100 group-focus-within/message:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100 pointer-coarse:size-8",
              className,
            )}
            aria-label={t("message.actions")}
          >
            <EllipsisIcon aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="w-40"
          onCloseAutoFocus={(e) => {
            const chosen = pendingEdit.current;
            if (!chosen) return;
            pendingEdit.current = null;
            e.preventDefault();
            onEdit?.(chosen);
          }}
        >
          <MessageActionItems
            message={message}
            canEdit={canEdit}
            canDelete={canDelete}
            onEdit={(m) => {
              pendingEdit.current = m;
            }}
            onDelete={() => setConfirming(true)}
          />
        </DropdownMenuContent>
      </DropdownMenu>
      <DeleteMessageDialog message={message} open={confirming} onOpenChange={setConfirming} />
    </>
  );
}

/** The two items, for whichever menu holds them. */
export function MessageActionItems({
  message,
  canEdit,
  canDelete,
  onEdit,
  onDelete,
}: {
  message: Message;
  canEdit: boolean;
  canDelete: boolean;
  onEdit?: (message: Message) => void;
  onDelete: () => void;
}) {
  const t = useT();
  return (
    <>
      {canEdit ? (
        <DropdownMenuItem onSelect={() => onEdit?.(message)}>
          <PencilIcon aria-hidden="true" />
          {t("message.edit")}
        </DropdownMenuItem>
      ) : null}
      {canDelete ? (
        <DropdownMenuItem variant="destructive" onSelect={onDelete}>
          <Trash2Icon aria-hidden="true" />
          {t("message.delete")}
        </DropdownMenuItem>
      ) : null}
    </>
  );
}

/**
 * Asks before deleting (DLT-09): the message goes for everyone, files included, and it cannot be undone. It
 * stays open while the request runs and when it fails, so the person can try again or give up; a failure is
 * said in a toast and the message stays as it was.
 */
export function DeleteMessageDialog({
  message,
  open,
  onOpenChange,
}: {
  message: Message;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  const remove = useDeleteMessage(message.room_id);
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("message.deleteTitle")}</AlertDialogTitle>
          <AlertDialogDescription>{t("message.deleteBody")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            disabled={remove.isPending}
            onClick={(e) => {
              e.preventDefault();
              remove.mutate(message.id, {
                onSuccess: () => onOpenChange(false),
                onError: () => toast.error(t("message.deleteFailed")),
              });
            }}
          >
            {t("message.deleteConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
