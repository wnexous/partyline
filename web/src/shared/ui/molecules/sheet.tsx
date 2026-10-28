import type * as React from "react";
import { useT } from "@/shared/i18n";
import { SheetContent as BaseSheetContent } from "@/shared/ui/primitives/sheet";

export {
  Sheet,
  SheetClose,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/shared/ui/primitives/sheet";

/*
 * The app's sheet: the primitive with the translated close label filled in. App code imports sheets
 * from here, never from shared/ui/primitives/sheet.
 */
export function SheetContent(props: React.ComponentProps<typeof BaseSheetContent>) {
  const t = useT();
  return <BaseSheetContent closeLabel={t("common.close")} {...props} />;
}
