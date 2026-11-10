import { zodResolver } from "@hookform/resolvers/zod";
import { useId, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { useUpdateInstanceSettings } from "@/entities/account";
import { formFailureText } from "@/shared/api/error-text";
import type { InstanceSettings, UpdateInstanceSettingsRequest } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FieldError, FormAlert } from "@/shared/ui/molecules/form-fields";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  Form as FormProvider,
} from "@/shared/ui/primitives/form";
import { Input } from "@/shared/ui/primitives/input";
import {
  type AttachmentRetentionForm,
  attachmentRetentionSchema,
  MAX_ATTACHMENT_RETENTION_DAYS,
} from "../model/schema";

/*
 * The attachment retention of the instance (RET-01, RET-07): files of conversations older than N
 * days are deleted every day, 0 keeps them forever. The server's default comes from its
 * configuration; a value saved here applies instead until "Use the server default".
 */
export function AttachmentRetentionSection({ settings }: { settings: InstanceSettings }) {
  const t = useT();
  const id = useId();
  const update = useUpdateInstanceSettings();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<AttachmentRetentionForm>({
    resolver: zodResolver(attachmentRetentionSchema),
    // The form follows what the server answers, after a save or a reset.
    values: { attachment_retention_days: settings.attachment_retention_days },
  });

  const send = (req: UpdateInstanceSettingsRequest) => {
    setError(null);
    update.mutate(req, {
      onSuccess: () => toast.success(t("instance.retentionSaved")),
      onError: (err) => {
        if (applyFieldErrors(err, form.setError, ["attachment_retention_days"])) return;
        setError(formFailureText(t, err));
      },
    });
  };
  const onSubmit = form.handleSubmit((values) =>
    send({
      attachment_retention_days: attachmentRetentionSchema.parse(values).attachment_retention_days,
    }),
  );
  const defaultDays = settings.attachment_retention_default_days;

  return (
    <Section
      id={`${id}-retention`}
      title={t("instance.retentionTitle")}
      description={t("instance.retentionBody")}
    >
      <FormProvider {...form}>
        <form
          aria-labelledby={`${id}-retention`}
          onSubmit={onSubmit}
          noValidate
          className="space-y-4"
        >
          <FormAlert>{error}</FormAlert>
          <FormField
            control={form.control}
            name="attachment_retention_days"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("instance.retentionLabel")}</FormLabel>
                <FormControl>
                  <Input
                    type="number"
                    inputMode="numeric"
                    min={0}
                    max={MAX_ATTACHMENT_RETENTION_DAYS}
                    step={1}
                    className="w-32 font-mono tabular"
                    name={field.name}
                    ref={field.ref}
                    onBlur={field.onBlur}
                    value={Number.isNaN(field.value) ? "" : field.value}
                    onChange={(e) =>
                      field.onChange(e.target.value === "" ? Number.NaN : e.target.valueAsNumber)
                    }
                  />
                </FormControl>
                <FormDescription className="text-xs">
                  {t("instance.retentionHint")}{" "}
                  {defaultDays > 0
                    ? t("instance.retentionDefault", { days: defaultDays })
                    : t("instance.retentionDefaultForever")}
                </FormDescription>
                <FieldError />
              </FormItem>
            )}
          />
          {settings.attachment_retention_overridden ? (
            <p className="text-xs text-muted-foreground">{t("instance.retentionOverridden")}</p>
          ) : null}
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={update.isPending}>
              {t("common.save")}
            </Button>
            {settings.attachment_retention_overridden ? (
              <Button
                type="button"
                variant="outline"
                disabled={update.isPending}
                onClick={() => send({ reset_attachment_retention: true })}
              >
                {t("instance.retentionUseDefault")}
              </Button>
            ) : null}
          </div>
        </form>
      </FormProvider>
    </Section>
  );
}
