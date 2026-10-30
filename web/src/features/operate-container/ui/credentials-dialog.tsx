import { zodResolver } from "@hookform/resolvers/zod";
import { useMemo, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";
import {
  ContainerEnvRows,
  formPathOf,
  hasCredential,
  useUpdateContainerEnv,
} from "@/entities/container-agent";
import { harnessLabel } from "@/entities/intelligence";
import { useSecrets } from "@/entities/secret";
import { isApiError } from "@/shared/api/client";
import { errorText } from "@/shared/api/error-text";
import { useT } from "@/shared/i18n";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert } from "@/shared/ui/molecules/form-fields";
import { Form } from "@/shared/ui/primitives/form";
import { containerCredentialsSchema } from "../model/credentials-schema";

/**
 * Edits the credentials of a container AI (CA-04): the same variable/secret rows as the create form,
 * prefilled with the current mapping. Saving creates the container again on the runner with the new
 * environment, the same workspace volume and a fresh token, so the app signs in with the new
 * credential; the server validates the rows exactly as it does for a create.
 */
export function ContainerCredentialsDialog({
  agentId,
  harness,
  env,
  onClose,
}: {
  agentId: string;
  /** The app the container runs: its credential table and the suggested variable. */
  harness: string;
  /** The current mapping, as the container reports it (variables and secret names only). */
  env: { var: string; secret_name: string }[];
  onClose: () => void;
}) {
  const t = useT();
  const update = useUpdateContainerEnv(agentId);
  const secrets = useSecrets();
  const [error, setError] = useState<string | null>(null);
  const schema = useMemo(() => containerCredentialsSchema(harness), [harness]);
  const form = useForm({
    resolver: zodResolver(schema),
    defaultValues: {
      env: env.map((row) => ({ var_name: row.var, secret_name: row.secret_name })),
    },
  });
  const rows = useWatch({ control: form.control, name: "env" });
  const credentialMapped = hasCredential(harness, rows);
  const secretList = secrets.data ?? [];

  const onSubmit = form.handleSubmit((values) => {
    if (update.isPending) return;
    setError(null);
    update.mutate(
      (values.env ?? []).map((row) => ({ var: row.var_name, secret_name: row.secret_name })),
      {
        onSuccess: () => {
          toast.success(t("container.credentialsSaved"));
          onClose();
        },
        onError: (err) => {
          if (isApiError(err) && err.status === 422) {
            let applied = false;
            for (const [field, reason] of Object.entries(err.fields)) {
              const path = formPathOf(field);
              if (!path) continue;
              form.setError(path as Parameters<typeof form.setError>[0], {
                type: "server",
                message: reason,
              });
              applied = true;
            }
            if (applied) return;
          }
          setError(errorText(t, err, "container"));
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
      title={t("container.credentialsTitle")}
      description={t("container.credentialsBody")}
      onSubmit={onSubmit}
      submitLabel={update.isPending ? t("common.saving") : t("container.credentialsSave")}
      pending={update.isPending}
      submitDisabled={!credentialMapped}
      formLabel={t("container.credentialsTitle")}
      contentClassName="max-h-[calc(100dvh-2rem)] overflow-y-auto overscroll-contain sm:max-w-xl"
    >
      <Form {...form}>
        <FormAlert>{error}</FormAlert>
        <ContainerEnvRows
          control={form.control}
          harness={harness}
          app={harnessLabel(harness) ?? harness}
          secrets={secretList}
        />
        {!credentialMapped ? (
          <p className="text-xs text-pretty text-muted-foreground">
            {t("containerAgent.credentialsNeeded")}
          </p>
        ) : null}
      </Form>
    </FormDialog>
  );
}
