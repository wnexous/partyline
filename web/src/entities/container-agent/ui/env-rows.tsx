import { PlusIcon, XIcon } from "lucide-react";
import type { Control } from "react-hook-form";
import { useFieldArray } from "react-hook-form";
import { useT } from "@/shared/i18n";
import { SelectField, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import type { ContainerEnvValues } from "../model/env-schema";
import {
  type ContainerHarness,
  CREDENTIAL_HINT,
  CREDENTIAL_SUGGESTION,
  CREDENTIAL_VARS,
} from "../model/harness";

/**
 * The credentials fieldset of a container AI (CA-04): one row per secret, the variable the app reads
 * and the secret that fills it, with the app's own table as the hint and its first variable suggested
 * by **Map a secret**. It is the same fieldset when a container AI is created and when its
 * credentials are edited later, so the two forms cannot drift; the surrounding form owns the submit
 * and the server errors.
 */
export function ContainerEnvRows<T extends ContainerEnvValues>({
  control,
  harness,
  app,
  secrets,
}: {
  control: Control<T>;
  /** The app the container runs: decides the suggested variable and the hint. */
  harness: string;
  /** The app's readable name for the credentials sentence (the caller translates it). */
  app: string;
  /** The owner's secrets, by name: the choices of each row. */
  secrets: readonly { name: string }[];
}) {
  const t = useT();
  // The rows of either form keep the shared shape, so the fieldset works with both form types.
  const form = control as unknown as Control<ContainerEnvValues>;
  const env = useFieldArray({ control: form, name: "env" });
  const suggestion = CREDENTIAL_SUGGESTION[harness as ContainerHarness] ?? "";
  const vars = (CREDENTIAL_VARS[harness as ContainerHarness] ?? []).join(", ");
  const hint = CREDENTIAL_HINT[harness as ContainerHarness];

  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium">{t("containerAgent.credentials")}</legend>
      <p className="text-xs text-pretty text-muted-foreground">
        {t("containerAgent.credentialsBody", { app, vars })}
      </p>
      {hint ? <p className="text-xs text-pretty text-muted-foreground">{t(hint)}</p> : null}
      {secrets.length === 0 ? (
        <p className="text-xs text-pretty text-muted-foreground">{t("containerAgent.noSecrets")}</p>
      ) : null}
      {env.fields.length > 0 ? (
        <ul className="space-y-3">
          {env.fields.map((row, index) => (
            <li
              key={row.id}
              className="grid gap-3 rounded-md border px-3 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end"
            >
              <TextField
                control={form}
                name={`env.${index}.var_name`}
                label={t("containerAgent.varName")}
                autoComplete="off"
                inputClassName="font-mono"
              />
              <SelectField
                control={form}
                name={`env.${index}.secret_name`}
                label={t("containerAgent.secretName")}
              >
                {secrets.length === 0 ? <option value="">—</option> : null}
                {secrets.map((secret) => (
                  <option key={secret.name} value={secret.name}>
                    {secret.name}
                  </option>
                ))}
              </SelectField>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="pointer-coarse:size-11"
                aria-label={t("containerAgent.removeEnv", {
                  name: row.var_name || t("containerAgent.varName"),
                })}
                disabled={env.fields.length === 0}
                onClick={() => env.remove(index)}
              >
                <XIcon aria-hidden="true" />
              </Button>
            </li>
          ))}
        </ul>
      ) : null}
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={secrets.length === 0}
        onClick={() =>
          env.append({
            var_name: suggestion,
            secret_name: secrets[0]?.name ?? "",
          })
        }
      >
        <PlusIcon aria-hidden="true" />
        {t("containerAgent.addEnv")}
      </Button>
    </fieldset>
  );
}
