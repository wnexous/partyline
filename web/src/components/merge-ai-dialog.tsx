import { type FormEvent, useId, useState } from "react";
import { toast } from "sonner";
import { FormAlert } from "@/shared/ui/molecules/form-fields";
import { NativeSelect } from "@/shared/ui/atoms/native-select";
import { Button } from "@/shared/ui/primitives/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/shared/ui/primitives/dialog";
import { isApiError } from "@/lib/api/client";
import { useMergeAgent } from "@/lib/api/queries";
import { ErrorCodeIdentityOnline, type IntelligenceRef } from "@/lib/api/types.gen";
import { useT } from "@/shared/i18n";
import { isCoarsePointer } from "@/shared/lib/device";

/*
 * Merge one of the caller's AIs into another one (MRG-01, MRG-02): the one picked keeps
 * everything the other had - groups, direct messages, history, organizations and connectors - and the
 * other is deleted. A page that shows the AI afterwards (its profile) passes `onMerged` to open
 * the survivor. An AI with a live session is refused until its session ends.
 */

export function MergeAIDialog({
  ai,
  candidates,
  onClose,
  onMerged,
}: {
  /** The AI that is merged away; null means the dialog is closed. */
  ai: IntelligenceRef | null;
  /** The AIs it may merge into (the owner's, itself left out). */
  candidates: IntelligenceRef[];
  onClose: () => void;
  /** Called with the id of the AI that remains once the merge succeeded, before the dialog closes. */
  onMerged?: (survivorId: string) => void;
}) {
  const t = useT();
  return (
    <Dialog open={ai !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent closeLabel={t("common.close")} touchFirst={isCoarsePointer()}>
        {/* Keyed by the AI: a different opening starts from nothing picked and no old error. */}
        {ai ? (
          <MergeForm
            key={ai.id}
            ai={ai}
            candidates={candidates}
            onClose={onClose}
            onMerged={onMerged}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function MergeForm({
  ai,
  candidates,
  onClose,
  onMerged,
}: {
  ai: IntelligenceRef;
  candidates: IntelligenceRef[];
  onClose: () => void;
  onMerged?: (survivorId: string) => void;
}) {
  const t = useT();
  const id = useId();
  const merge = useMergeAgent();
  const [into, setInto] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | null>(null);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (into === "" || merge.isPending) return;
    setError(null);
    setFieldError(null);
    merge.mutate(
      { from: ai.id, into },
      {
        onSuccess: (survivor) => {
          toast.success(
            t("merge.done", { name: ai.display_name, into: survivor.intelligence.display_name }),
          );
          onClose();
          onMerged?.(survivor.id);
        },
        onError: (err) => {
          if (isApiError(err) && err.code === ErrorCodeIdentityOnline) {
            setError(t("merge.online", { name: ai.display_name }));
            return;
          }
          if (isApiError(err) && err.status === 403) {
            setError(t("merge.forbidden", { name: ai.display_name }));
            return;
          }
          if (isApiError(err) && err.status === 422 && err.fields.into) {
            setFieldError(err.fields.into);
            return;
          }
          setError(
            isApiError(err) && err.status === 0
              ? t("error.network")
              : t("merge.failed", { name: ai.display_name }),
          );
        },
      },
    );
  };

  const title = t("merge.title", { name: ai.display_name });
  const fieldId = `${id}-into`;
  const fieldErrorId = `${id}-into-error`;

  return (
    <form aria-label={title} onSubmit={submit} noValidate className="space-y-4">
      <DialogHeader>
        {/* The dialog is named by Radix's own id on the title; adding one here would orphan it. */}
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{t("merge.body", { name: ai.display_name })}</DialogDescription>
      </DialogHeader>
      <FormAlert>{error}</FormAlert>
      <div className="space-y-1">
        <label htmlFor={fieldId} className="block text-sm font-medium">
          {t("merge.into")}
        </label>
        <NativeSelect
          id={fieldId}
          value={into}
          onChange={(e) => {
            setInto(e.target.value);
            setFieldError(null);
          }}
          aria-invalid={fieldError ? true : undefined}
          aria-describedby={fieldError ? fieldErrorId : undefined}
        >
          <option value="">{t("merge.pick")}</option>
          {candidates.map((candidate) => (
            <option key={candidate.id} value={candidate.id}>
              {candidate.display_name} (@{candidate.handle})
            </option>
          ))}
        </NativeSelect>
        {fieldError ? (
          <p id={fieldErrorId} role="alert" className="text-sm text-destructive">
            {fieldError}
          </p>
        ) : null}
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" disabled={into === "" || merge.isPending}>
          {t("merge.confirm")}
        </Button>
      </DialogFooter>
    </form>
  );
}
