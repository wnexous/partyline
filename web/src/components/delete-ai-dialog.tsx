import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { useDeleteAgent } from "@/lib/api/queries";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";

/**
 * Asks before deleting an AI (the contacts page for the unlinked ones, its profile for the rest):
 * it leaves every group, its direct messages go with it and its sharing is lost. `ai` open means
 * the dialog is; `onDeleted` runs once it is gone, `onClose` when the dialog closes either way.
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
                onError: () => toast.error(t("ai.deleteFailed", { name })),
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
