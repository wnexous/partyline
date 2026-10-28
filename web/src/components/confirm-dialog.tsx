import type { ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/shared/ui/primitives/alert-dialog";
import { useT } from "@/lib/i18n";

/*
 * The question before something that cannot be taken back (ACT-04): an alert dialog that names
 * what it is about, with Cancel focused. Confirming does not close it: the request is running,
 * and a failure must leave the dialog where it is, to try again from.
 */

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  body,
  confirmLabel,
  pending,
  onConfirm,
  onCloseFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  body: ReactNode;
  /** The action, in its own word: "Leave group", not "OK". */
  confirmLabel: string;
  pending: boolean;
  onConfirm: () => void;
  /** Where focus goes when it closes; `preventDefault()` it to place it yourself. */
  onCloseFocus?: (event: Event) => void;
}) {
  const t = useT();
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent onCloseAutoFocus={onCloseFocus}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{body}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            disabled={pending}
            onClick={(e) => {
              e.preventDefault();
              onConfirm();
            }}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
