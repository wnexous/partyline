import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";
import {
  CONTAINER_HARNESSES,
  ContainerEnvRows,
  formPathOf,
  hasCredential,
} from "@/entities/container-agent";
import { harnessLabel } from "@/entities/intelligence";
import { useRunners } from "@/entities/runner";
import { useSecrets } from "@/entities/secret";
import { isApiError } from "@/shared/api/client";
import { errorText } from "@/shared/api/error-text";
import { useT } from "@/shared/i18n";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert, SelectField, TextField } from "@/shared/ui/molecules/form-fields";
import { Form } from "@/shared/ui/primitives/form";
import { useCreateContainerAgent } from "../api/create-container-agent";
import { CPU_OPTIONS, MEMORY_OPTIONS } from "../model/harness-env";
import {
  type CreateContainerAgentForm,
  createContainerAgentDefaults,
  createContainerAgentSchema,
} from "../model/schema";

/**
 * "New AI in a container" (CA-04, CA-10): the server to run it on, its app, the profile of the new
 * AI, an optional repository and the owner's secrets as environment variables, then the limits.
 * On success the AI's profile opens, where its container can be operated. A server that is offline
 * is a 409 and an invalid field a 422, both with the shared error text (CA-07).
 */
export function NewContainerAgentDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  // The body mounts only while open: every opening starts from a fresh form.
  return open ? <NewContainerAgentForm onOpenChange={onOpenChange} /> : null;
}

function NewContainerAgentForm({ onOpenChange }: { onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const navigate = useNavigate();
  const create = useCreateContainerAgent();
  const runners = useRunners();
  const secrets = useSecrets();
  const [error, setError] = useState<string | null>(null);
  const online = (runners.data ?? []).filter((r) => r.online);
  const form = useForm<CreateContainerAgentForm>({
    resolver: zodResolver(createContainerAgentSchema),
    defaultValues: createContainerAgentDefaults(online[0]?.id ?? ""),
  });
  const harness = useWatch({ control: form.control, name: "harness" });
  const runnerId = useWatch({ control: form.control, name: "runner_id" });
  const envRows = useWatch({ control: form.control, name: "env" });
  const runnerIds = online.map((r) => r.id).join(",");
  // A create without a credential the app reads only restart-loops, so the server refuses it; the
  // form refuses to send it at all and says what to map.
  const credentialMapped = hasCredential(harness, envRows);

  // The servers arrive after the form mounts: the first connected one becomes the choice when the
  // form has none (or one that is not connected).
  useEffect(() => {
    const ids = runnerIds.split(",").filter(Boolean);
    const current = form.getValues("runner_id");
    if (!ids.includes(current) && ids[0]) form.setValue("runner_id", ids[0]);
  }, [runnerIds, form]);

  const onSubmit = form.handleSubmit((values) => {
    if (create.isPending) return;
    setError(null);
    const parsed = createContainerAgentSchema.parse(values);
    create.mutate(
      {
        runner_id: parsed.runner_id,
        harness: parsed.harness,
        display_name: parsed.display_name,
        handle: parsed.handle,
        ...(parsed.repo_url
          ? { repo_url: parsed.repo_url, ...(parsed.repo_ref ? { repo_ref: parsed.repo_ref } : {}) }
          : {}),
        env: parsed.env.map((row) => ({ var: row.var_name, secret_name: row.secret_name })),
        cpus: Number(parsed.cpus),
        memory_mb: Number(parsed.memory_mb),
      },
      {
        onSuccess: (container) => {
          toast.success(t("containerAgent.created", { name: parsed.display_name }));
          onOpenChange(false);
          void navigate({ to: "/contacts/$id", params: { id: container.agent.id } });
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

  const noRunners = runners.isSuccess && online.length === 0;
  const secretList = secrets.data ?? [];

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title={t("containerAgent.title")}
      description={t("containerAgent.body")}
      onSubmit={onSubmit}
      submitLabel={create.isPending ? t("containerAgent.creating") : t("containerAgent.create")}
      pending={create.isPending}
      submitDisabled={noRunners || !credentialMapped}
      formLabel={t("containerAgent.title")}
      contentClassName="max-h-[calc(100dvh-2rem)] overflow-y-auto overscroll-contain sm:max-w-xl"
    >
      <Form {...form}>
        <FormAlert>{error}</FormAlert>
        {noRunners ? <FormAlert>{t("containerAgent.noRunners")}</FormAlert> : null}

        <SelectField
          control={form.control}
          name="runner_id"
          label={t("containerAgent.runner")}
          description={t("containerAgent.runnerHint")}
        >
          {runnerId === "" ? <option value="">{t("common.loading")}</option> : null}
          {(runners.data ?? []).map((runner) => (
            <option key={runner.id} value={runner.id} disabled={!runner.online}>
              {runner.online
                ? `${runner.name} — ${runner.machine}`
                : t("containerAgent.runnerOfflineOption", { name: runner.name })}
            </option>
          ))}
        </SelectField>

        <SelectField control={form.control} name="harness" label={t("containerAgent.app")}>
          {CONTAINER_HARNESSES.map((id) => (
            <option key={id} value={id}>
              {harnessLabel(id) ?? id}
            </option>
          ))}
        </SelectField>

        <div className="grid gap-4 sm:grid-cols-2">
          <TextField
            control={form.control}
            name="display_name"
            label={t("containerAgent.displayName")}
            autoComplete="off"
            autoFocus
          />
          <TextField
            control={form.control}
            name="handle"
            label={t("containerAgent.handle")}
            description={t("containerAgent.handleHint")}
            autoComplete="off"
            inputClassName="font-mono"
          />
        </div>

        <fieldset className="space-y-3">
          <legend className="text-sm font-medium">{t("containerAgent.repo")}</legend>
          <TextField
            control={form.control}
            name="repo_url"
            label={t("containerAgent.repoUrl")}
            description={t("containerAgent.repoUrlHint")}
            optional
            type="url"
            autoComplete="off"
            inputClassName="font-mono"
          />
          <TextField
            control={form.control}
            name="repo_ref"
            label={t("containerAgent.repoRef")}
            description={t("containerAgent.repoRefHint")}
            optional
            autoComplete="off"
            inputClassName="font-mono"
          />
        </fieldset>

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

        <fieldset className="grid gap-4 sm:grid-cols-2">
          <legend className="text-sm font-medium">{t("containerAgent.limits")}</legend>
          <SelectField control={form.control} name="cpus" label={t("containerAgent.cpu")}>
            {CPU_OPTIONS.map((value) => (
              <option key={value} value={value}>
                {value} {t("containerAgent.cpu")}
              </option>
            ))}
          </SelectField>
          <SelectField control={form.control} name="memory_mb" label={t("containerAgent.memory")}>
            {MEMORY_OPTIONS.map((value) => (
              <option key={value} value={value}>
                {value} MB
              </option>
            ))}
          </SelectField>
        </fieldset>
      </Form>
    </FormDialog>
  );
}
