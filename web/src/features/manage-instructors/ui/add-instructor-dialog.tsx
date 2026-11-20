import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { isApiError } from "@/shared/api/client";
import { errorText } from "@/shared/api/error-text";
import { ErrorCodeContactNotFound } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Form } from "@/shared/ui/primitives/form";
import { useAddInstructor } from "../api/instructors";
import { type AddInstructorFormValues, addInstructorSchema } from "../model/schema";

/**
 * The host gives an AI one more instructor by @handle (SR2-10): a user, or an AI the host can see.
 * Its messages become the AI's trusted ones and, for a user, it may also edit the AI (SR2-07,
 * SR2-13), which the dialog says before it is sent.
 */
export function AddInstructorDialog({
  ai,
  open,
  onOpenChange,
}: {
  ai: { id: string; name: string };
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  // Mounted only while open: every opening starts with an empty form.
  return open ? <AddInstructorForm ai={ai} onOpenChange={onOpenChange} /> : null;
}

function AddInstructorForm({
  ai,
  onOpenChange,
}: {
  ai: { id: string; name: string };
  onOpenChange: (open: boolean) => void;
}) {
  const { t, locale } = useI18n();
  const add = useAddInstructor(ai.id);
  const [error, setError] = useState<string | null>(null);
  const form = useForm<AddInstructorFormValues>({
    resolver: zodResolver(addInstructorSchema),
    defaultValues: { handle: "" },
  });

  const onSubmit = form.handleSubmit((values) => {
    if (add.isPending) return;
    setError(null);
    const { handle } = addInstructorSchema.parse(values);
    add.mutate(
      { handle },
      {
        onSuccess: (added) => {
          toast.success(
            t("instructors.added", { who: added.intelligence.display_name, name: ai.name }),
          );
          onOpenChange(false);
        },
        onError: (err) => {
          if (isApiError(err) && err.code === ErrorCodeContactNotFound) {
            form.setError("handle", { type: "server", message: t("instructors.unknown") });
          } else if (!applyFieldErrors(err, form.setError, ["handle"])) {
            setError(errorText(t, err, "instructor", locale));
          }
        },
      },
    );
  });

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title={t("instructors.addTitle", { name: ai.name })}
      description={t("instructors.addBody", { name: ai.name })}
      onSubmit={onSubmit}
      submitLabel={add.isPending ? t("instructors.adding") : t("instructors.addSubmit")}
      pending={add.isPending}
    >
      <Form {...form}>
        <FormAlert>{error}</FormAlert>
        <TextField
          control={form.control}
          name="handle"
          label={t("instructors.handle")}
          description={t("instructors.handleHint")}
          autoComplete="off"
          autoFocus
        />
      </Form>
    </FormDialog>
  );
}
