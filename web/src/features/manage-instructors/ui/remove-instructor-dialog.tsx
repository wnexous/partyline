import { useState } from "react";
import { toast } from "sonner";
import { errorText } from "@/shared/api/error-text";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import { useRemoveInstructor } from "../api/instructors";

/**
 * Asks the host before an instructor stops instructing the AI (SR2-10): its messages are no longer
 * trusted and, for a user, it no longer edits the AI. `instructor` open means the dialog is; a
 * failure is told inside it and the dialog stays for another try.
 */
export function RemoveInstructorDialog({
  ai,
  instructor,
  onClose,
}: {
  ai: { id: string; name: string };
  instructor: IntelligenceRef | null;
  onClose: () => void;
}) {
  const { t, locale } = useI18n();
  const remove = useRemoveInstructor(ai.id);
  const [error, setError] = useState<string | null>(null);
  const who = instructor?.display_name ?? "";

  const close = () => {
    setError(null);
    onClose();
  };

  return (
    <ConfirmDialog
      open={instructor !== null}
      onClose={close}
      title={t("instructors.removeTitle", { who, name: ai.name })}
      body={t("instructors.removeBody", { who, name: ai.name })}
      confirmLabel={t("instructors.remove")}
      destructive
      pending={remove.isPending}
      onConfirm={() => {
        if (!instructor) return;
        setError(null);
        remove.mutate(instructor.id, {
          onSuccess: () => {
            toast(t("instructors.removed", { who, name: ai.name }));
            close();
          },
          onError: (err) => setError(errorText(t, err, "instructor", locale)),
        });
      }}
    >
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </ConfirmDialog>
  );
}
