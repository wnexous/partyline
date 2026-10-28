import type { FormEvent, ReactNode } from "react";
import { useT } from "@/shared/i18n";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/shared/ui/molecules/dialog";
import { Button } from "@/shared/ui/primitives/button";

/*
 * The shell of a dialog that asks for input: a title and what it is for, the form's fields, and the
 * footer with Cancel and the submit button. The fields are the children; the form element is this
 * shell's, so Enter submits it and the submit button is disabled while `pending` (or `submitDisabled`).
 * Cancel and a close (Escape, the close button) both call `onOpenChange(false)`.
 */
export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  onSubmit,
  submitLabel,
  cancelLabel,
  pending = false,
  submitDisabled = false,
  formLabel,
  contentClassName,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  /** The submit button's text: the caller says what it does ("Send invite"). */
  submitLabel: ReactNode;
  /** Defaults to the translated "Cancel". */
  cancelLabel?: string;
  /** Disables the submit button while its request runs. */
  pending?: boolean;
  /** Disables the submit button until the form can be sent. */
  submitDisabled?: boolean;
  /** The accessible name of the form element, when it is not the dialog's title. */
  formLabel?: string;
  contentClassName?: string;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={contentClassName}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        <form aria-label={formLabel} onSubmit={onSubmit} noValidate className="space-y-4">
          {children}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {cancelLabel ?? t("common.cancel")}
            </Button>
            <Button type="submit" disabled={pending || submitDisabled}>
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
