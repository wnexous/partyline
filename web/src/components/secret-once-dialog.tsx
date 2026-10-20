import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";
import { CodeBlock } from "@/components/copy-field";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

/*
 * A secret the server returns exactly once (token secret, invite link): shown with a copy
 * button and a warning, and dropped from memory when the dialog closes (UI-19). An outside click
 * does not close it, so the secret is not lost by accident.
 */
export function SecretOnceDialog({
  secret,
  title,
  description,
  label,
  warning,
  confirmLabel,
  children,
  onClose,
}: {
  /** The secret to show; null closes the dialog. */
  secret: string | null;
  title: string;
  description?: ReactNode;
  /** Caption of the value block, usually the resource name. */
  label: string;
  warning: string;
  confirmLabel: string;
  children?: ReactNode;
  onClose: () => void;
}) {
  return (
    <Dialog open={secret !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <DialogContent className="sm:max-w-lg" onInteractOutside={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        {secret !== null ? (
          <div className="min-w-0 space-y-4">
            <CodeBlock label={label} value={secret} />
            <div
              role="note"
              className="flex gap-2.5 rounded-md bg-warning/15 px-3 py-2.5 text-sm ring-1 ring-warning/45 ring-inset"
            >
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <p>{warning}</p>
            </div>
            {children}
          </div>
        ) : null}
        <DialogFooter>
          <Button onClick={onClose}>{confirmLabel}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
