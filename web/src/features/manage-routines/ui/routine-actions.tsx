import { PencilIcon, PlayIcon, Trash2Icon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { errorText } from "@/shared/api/error-text";
import type { Schedule } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import { Button } from "@/shared/ui/primitives/button";
import { Switch } from "@/shared/ui/primitives/switch";
import { useDeleteRoutine, useRunRoutine, useUpdateRoutine } from "../api/routines";
import { RoutineDialog } from "./routine-dialog";

const touch = "pointer-coarse:h-11 pointer-coarse:px-4";

/**
 * The controls of one routine for whoever manages its AI (HB-13): a switch that pauses and resumes it,
 * "Run now", "Edit" and "Delete" (after a question). They wrap on a phone and each is a finger's
 * target. A refusal is said under them; one that means the list is stale (403, 404, 409) reads it
 * again (the mutations do).
 */
export function RoutineActions({
  agentId,
  aiName,
  routine,
}: {
  agentId: string;
  aiName: string;
  routine: Schedule;
}) {
  const { t, locale } = useI18n();
  const update = useUpdateRoutine(agentId);
  const run = useRunRoutine(agentId);
  const remove = useDeleteRoutine(agentId);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const [deleteFailed, setDeleteFailed] = useState<string | null>(null);
  const switchId = useId();
  const fail = (err: unknown) => setFailed(errorText(t, err, "routine", locale));

  const toggle = (enabled: boolean) => {
    setFailed(null);
    update.mutate(
      { id: routine.id, body: { enabled } },
      {
        onSuccess: () =>
          toast.success(
            enabled
              ? t("routines.resumed", { name: routine.name })
              : t("routines.pausedToast", { name: routine.name }),
          ),
        onError: fail,
      },
    );
  };

  const runNow = () => {
    setFailed(null);
    run.mutate(routine.id, {
      onSuccess: () => toast.success(t("routines.ran", { name: routine.name, ai: aiName })),
      onError: fail,
    });
  };

  const confirmDelete = () => {
    setDeleteFailed(null);
    remove.mutate(routine.id, {
      onSuccess: () => {
        setDeleting(false);
        toast.success(t("routines.deleted", { name: routine.name }));
      },
      onError: (err) => setDeleteFailed(errorText(t, err, "routine", locale)),
    });
  };

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 pt-1">
        {routine.can_edit ? (
          <label
            htmlFor={switchId}
            className="mr-1 inline-flex min-h-9 cursor-pointer items-center gap-2 text-sm font-medium pointer-coarse:min-h-11"
          >
            <Switch
              id={switchId}
              data-testid="routine-toggle"
              aria-label={t("routines.toggle", { name: routine.name })}
              checked={routine.enabled}
              disabled={update.isPending}
              onCheckedChange={toggle}
            />
            {t("routines.active")}
          </label>
        ) : null}
        <Button
          type="button"
          variant="outline"
          size="sm"
          className={touch}
          aria-label={t("routines.runNowLabel", { name: routine.name })}
          disabled={run.isPending}
          onClick={runNow}
        >
          <PlayIcon aria-hidden="true" />
          {t("routines.runNow")}
        </Button>
        {routine.can_edit ? (
          <>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className={touch}
              aria-label={t("routines.editLabel", { name: routine.name })}
              onClick={() => setEditing(true)}
            >
              <PencilIcon aria-hidden="true" />
              {t("routines.edit")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className={`text-destructive hover:text-destructive ${touch}`}
              aria-label={t("routines.deleteLabel", { name: routine.name })}
              onClick={() => {
                setDeleteFailed(null);
                setDeleting(true);
              }}
            >
              <Trash2Icon aria-hidden="true" />
              {t("routines.delete")}
            </Button>
          </>
        ) : null}
      </div>
      {failed ? (
        <p role="alert" className="text-sm text-destructive">
          {failed}
        </p>
      ) : null}
      <RoutineDialog
        agentId={agentId}
        aiName={aiName}
        routine={routine}
        open={editing}
        onOpenChange={setEditing}
      />
      <ConfirmDialog
        open={deleting}
        onClose={() => setDeleting(false)}
        title={t("routines.deleteTitle", { name: routine.name })}
        body={t("routines.deleteBody", { ai: aiName })}
        confirmLabel={t("routines.delete")}
        pending={remove.isPending}
        onConfirm={confirmDelete}
      >
        {deleteFailed ? (
          <p role="alert" className="text-sm text-destructive">
            {deleteFailed}
          </p>
        ) : null}
      </ConfirmDialog>
    </div>
  );
}
