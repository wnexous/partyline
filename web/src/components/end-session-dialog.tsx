import { toast } from "sonner";
import { useEndSession } from "@/entities/intelligence";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";

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
    <ConfirmDialog
      open={ai !== null}
      onClose={onClose}
      title={t("profile.endTitle", { name })}
      body={t("profile.endBody", { name })}
      confirmLabel={t("profile.endSession")}
      pending={end.isPending}
      onConfirm={() => {
        // Stay open while the request runs: a failure must leave the dialog to retry from.
        if (!ai) return;
        end.mutate(ai.id, {
          onSuccess: () => {
            toast(t("profile.ended", { name }));
            onClose();
          },
          onError: () => toast.error(t("profile.endFailed", { name })),
        });
      }}
    />
  );
}
