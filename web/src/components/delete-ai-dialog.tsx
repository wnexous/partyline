import { toast } from "sonner";
import { useDeleteAgent } from "@/lib/api/queries";
import { isApiError } from "@/shared/api/client";
import { ErrorCodeLastAdmin, type IntelligenceRef } from "@/shared/api/types.gen";
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

/**
 * Asks before deleting an AI in its home organization (the contacts page for the unlinked ones, its
 * profile for the rest): it leaves every group and organization, its direct messages go with it and
 * the connections it operates lose their operator. `ai` open means the dialog is; `onDeleted` runs
 * once it is gone, `onClose` when the dialog closes either way.
 */
export function DeleteAIDialog({
  ai,
  onClose,
  onDeleted,
}: {
  ai: IntelligenceRef | null;
  onClose: () => void;
  onDeleted?: () => void;
}) {
  const t = useT();
  const remove = useDeleteAgent();
  const name = ai?.display_name ?? "";
  return (
    <AlertDialog open={ai !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("ai.deleteTitle", { name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("ai.deleteBody")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            disabled={remove.isPending}
            onClick={(e) => {
              // Stay open while the request runs: a failure must leave the dialog to retry from.
              e.preventDefault();
              if (!ai) return;
              remove.mutate(ai.id, {
                onSuccess: () => {
                  toast(t("ai.deleted", { name }));
                  onClose();
                  onDeleted?.();
                },
                // An organization cannot be left without an admin (OMEM-05).
                onError: (err) =>
                  toast.error(
                    isApiError(err) && err.code === ErrorCodeLastAdmin
                      ? t("ai.deleteLastAdmin", { name })
                      : t("ai.deleteFailed", { name }),
                  ),
              });
            }}
          >
            {t("ai.deleteConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
