import type * as React from "react";
import { useT } from "@/shared/i18n";
import { isCoarsePointer } from "@/shared/lib/device";
import {
  DialogContent as BaseDialogContent,
  DialogFooter as BaseDialogFooter,
} from "@/shared/ui/primitives/dialog";

export {
  Dialog,
  DialogClose,
  DialogDescription,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
} from "@/shared/ui/primitives/dialog";

/*
 * The app's dialog: the primitive with the translated close label and the touch-first focus rule
 * (UI-24) filled in, so no screen has to remember them. App code imports dialogs from here, never
 * from shared/ui/primitives/dialog.
 */
export function DialogContent(props: React.ComponentProps<typeof BaseDialogContent>) {
  const t = useT();
  return (
    <BaseDialogContent closeLabel={t("common.close")} touchFirst={isCoarsePointer()} {...props} />
  );
}

export function DialogFooter(props: React.ComponentProps<typeof BaseDialogFooter>) {
  const t = useT();
  return <BaseDialogFooter closeLabel={t("common.close")} {...props} />;
}
