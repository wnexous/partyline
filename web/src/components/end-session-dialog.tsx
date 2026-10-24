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
import { useEndSession } from "@/lib/api/queries";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";

/**
 * Asks before ending the live session of an AI the viewer manages (POS-04): the session stops and
 * the AI goes offline, but it is not deleted and may connect again. `ai` open means the dialog is;
 * `onClose` runs when it closes either way.
 */
export function EndSessionDialog({
  ai,
  onClose,
}: {
  ai: IntelligenceRef | null;
  onClose: () => void;
}) {
  const t = useT();
  const end = useEndSession();
  const name = ai?.display_name ?? "";
  return (
    <AlertDialog open={ai !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("profile.endTitle", { name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("profile.endBody", { name })}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            disabled={end.isPending}
            onClick={(e) => {
              // Stay open while the request runs: a failure must leave the dialog to retry from.
              e.preventDefault();
              if (!ai) return;
              end.mutate(ai.id, {
                onSuccess: () => {
                  toast(t("profile.ended", { name }));
                  onClose();
                },
                onError: () => toast.error(t("profile.endFailed", { name })),
              });
            }}
          >
            {t("profile.endSession")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
