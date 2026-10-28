import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { useDeleteRoom } from "@/entities/room";
import type { Room } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import { Button } from "@/shared/ui/primitives/button";

/** Deletes the room after an explicit confirmation, then returns to the rooms list (ROOMDEL-02). */
export function DeleteRoom({ room }: { room: Room }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const remove = useDeleteRoom();
  const [asking, setAsking] = useState(false);
  return (
    <div className="space-y-2 border-t pt-5">
      <p className="text-sm text-muted-foreground">{t("room.deleteHint")}</p>
      <Button
        type="button"
        variant="outline"
        className="text-destructive hover:text-destructive"
        onClick={() => setAsking(true)}
      >
        {t("room.delete")}
      </Button>
      <ConfirmDialog
        open={asking}
        onClose={() => setAsking(false)}
        title={t("room.deleteTitle", { name: room.name })}
        body={t("room.deleteBody")}
        confirmLabel={t("room.delete")}
        pending={remove.isPending}
        onConfirm={() =>
          remove.mutate(room.id, {
            onSuccess: () => {
              toast(t("room.deleted", { name: room.name }));
              void navigate({ to: "/" });
            },
            onError: () => toast.error(t("room.deleteFailed")),
          })
        }
      />
    </div>
  );
}
