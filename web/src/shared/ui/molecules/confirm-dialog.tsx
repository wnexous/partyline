import type { ReactNode } from "react";
import { useT } from "@/shared/i18n";
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

/*
 * The question before something that cannot be taken back (ACT-04): an alert dialog that names
 * what it is about, with Cancel focused. Confirming does not close it: the request is running,
 * and a failure must leave the dialog where it is, to try again from. The caller closes it when
 * the request succeeds, by setting `open` to false; `onClose` runs whenever it closes either way.
 *
 * `children` is extra content between the question and the buttons (an error to show, say).
 */

export function ConfirmDialog({
  open,
  onClose,
  title,
  body,
  confirmLabel,
  cancelLabel,
  destructive = true,
  pending = false,
  onConfirm,
  onCloseFocus,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  body: ReactNode;
  /** The action, in its own word: "Leave group", not "OK". */
  confirmLabel: string;
  /** Defaults to the translated "Cancel". */
  cancelLabel?: string;
  /** Styles the action as destructive (the default: every confirmation here removes something). */
  destructive?: boolean;
  /** Disables the action while its request runs. */
  pending?: boolean;
  onConfirm: () => void;
  /** Where focus goes when it closes; `preventDefault()` it to place it yourself. */
  onCloseFocus?: (event: Event) => void;
  children?: ReactNode;
}) {
  const t = useT();
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <AlertDialogContent onCloseAutoFocus={onCloseFocus}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{body}</AlertDialogDescription>
        </AlertDialogHeader>
        {children}
        <AlertDialogFooter>
          <AlertDialogCancel>{cancelLabel ?? t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className={
              destructive
                ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                : undefined
            }
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
