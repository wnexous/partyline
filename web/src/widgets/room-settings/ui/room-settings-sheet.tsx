import { type ReactNode, useState } from "react";
import { EditRoomForm } from "@/features/edit-room";
import type { Room } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/shared/ui/molecules/sheet";

/**
 * Room settings for managers: the group's photo (GAV-06), name, description, instructions for
 * agents (INS-01), retention, and deleting the room (ROOMDEL-01). Chat bridging, broadcast and the
 * catch-all lock are gone with platform chats as members (CID-22); the stored columns are dead
 * until the server drops them.
 *
 * It opens from its `trigger`, or, without one, from the caller (`open`, `onOpenChange`): a menu
 * item that is gone once the sheet opens, so `onCloseFocus` says where focus goes back to.
 */
export function RoomSettingsSheet({
  room,
  trigger,
  open: controlled,
  onOpenChange,
  onCloseFocus,
}: {
  room: Room;
  trigger?: ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onCloseFocus?: (event: Event) => void;
}) {
  const { t } = useI18n();
  const [own, setOwn] = useState(false);
  const open = controlled ?? own;
  const setOpen = (next: boolean) => {
    setOwn(next);
    onOpenChange?.(next);
  };
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      {trigger ? <SheetTrigger asChild>{trigger}</SheetTrigger> : null}
      <SheetContent className="w-full gap-0 sm:max-w-md" onCloseAutoFocus={onCloseFocus}>
        <SheetHeader className="border-b">
          <SheetTitle>{t("settings.title")}</SheetTitle>
          <SheetDescription>{t("settings.body")}</SheetDescription>
        </SheetHeader>
        {open ? <EditRoomForm room={room} onDone={() => setOpen(false)} /> : null}
      </SheetContent>
    </Sheet>
  );
}
