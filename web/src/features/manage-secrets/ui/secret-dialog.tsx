import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { usePutSecret } from "@/entities/secret";
import { errorText } from "@/shared/api/error-text";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Form } from "@/shared/ui/primitives/form";
import { type SecretForm, secretSchema } from "../model/schema";

/**
 * Writes one secret (CA-03): a new one, or a new value under an existing name in place. The value
 * goes in as a password field and the API never returns it, so nothing here can show an old value.
 * Mounted only while open: every opening starts from an empty form.
 */
export function SecretDialog({
  name,
  onClose,
}: {
  /** The name being replaced; absent creates a new secret. */
  name?: string;
  onClose: () => void;
}) {
  const t = useT();
  const put = usePutSecret();
  const replace = name !== undefined;
  const [error, setError] = useState<string | null>(null);
  const form = useForm<SecretForm>({
    resolver: zodResolver(secretSchema),
    defaultValues: { name: name ?? "", value: "" },
  });

  const onSubmit = form.handleSubmit((values) => {
    if (put.isPending) return;
    setError(null);
    const parsed = secretSchema.parse(values);
    put.mutate(
      { name: parsed.name, value: parsed.value },
      {
        onSuccess: () => {
          toast(t("secrets.saved", { name: parsed.name }));
          onClose();
        },
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["name", "value"])) return;
          setError(errorText(t, err, "secret"));
        },
      },
    );
  });

  return (
    <FormDialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={replace ? t("secrets.replaceTitle", { name }) : t("secrets.addTitle")}
      description={replace ? t("secrets.replaceBody") : t("secrets.addBody")}
      onSubmit={onSubmit}
      submitLabel={
        put.isPending ? t("secrets.saving") : replace ? t("secrets.replace") : t("secrets.save")
      }
      pending={put.isPending}
      formLabel={replace ? t("secrets.replaceTitle", { name }) : t("secrets.addTitle")}
    >
      <Form {...form}>
        <FormAlert>{error}</FormAlert>
        <TextField
          control={form.control}
          name="name"
          label={t("secrets.name")}
          description={t("secrets.nameHint")}
          autoComplete="off"
          inputClassName="font-mono"
          disabled={replace}
          autoFocus={!replace}
        />
        <TextField
          control={form.control}
          name="value"
          label={t("secrets.value")}
          description={t("secrets.valueHint")}
          type="password"
          autoComplete="new-password"
          autoFocus={replace}
        />
      </Form>
    </FormDialog>
  );
}
