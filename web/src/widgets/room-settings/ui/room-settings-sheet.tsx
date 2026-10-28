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
 */
export function RoomSettingsSheet({ room, trigger }: { room: Room; trigger: ReactNode }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b">
          <SheetTitle>{t("settings.title")}</SheetTitle>
          <SheetDescription>{t("settings.body")}</SheetDescription>
        </SheetHeader>
        {open ? <EditRoomForm room={room} onDone={() => setOpen(false)} /> : null}
      </SheetContent>
    </Sheet>
  );
}
