import { zodResolver } from "@hookform/resolvers/zod";
import { XIcon } from "lucide-react";
import { type KeyboardEvent, type ReactNode, useMemo, useState } from "react";
import { type Path, useForm } from "react-hook-form";
import { z } from "zod";
import {
  type ConnectorField,
  type ConnectorKind,
  FieldTypeBool,
  FieldTypeSecret,
  FieldTypeStringList,
  FieldTypeURL,
} from "@/shared/api/types.gen";
import { type MessageKey, useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { FieldError, FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
} from "@/shared/ui/primitives/form";
import { Switch } from "@/shared/ui/primitives/switch";

/*
 * Connector forms rendered from the kind's field schema (AD-007, UI-16): string → text,
 * url → url, secret → password with reveal, bool → switch, string_list → tag input. The zod
 * schema is built from the same fields at runtime, so required and URL errors show before the
 * request; 422 reasons from the server land under the same inputs. A `generated` field (the
 * webhook secret) may stay empty even when required: the server fills it on create and keeps
 * the stored value when an edit sends it empty.
 */

export type ConfigValue = string | boolean | string[];

export interface ConnectorFormValues {
  name: string;
  config: Record<string, ConfigValue>;
}

export interface ConnectorFormApi {
  /** Puts 422 field reasons under their inputs (`name`, `config.<field>` or `<field>`). */
  applyServerErrors: (fields: Record<string, string>) => boolean;
  setFormError: (message: string | null) => void;
}

const msg = (key: MessageKey) => ({ message: key });

function isUrl(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}

/** Whether the form must have a value: the server generates `generated` fields left empty. */
function isRequired(field: ConnectorField) {
  return field.required && !field.generated;
}

function fieldSchema(field: ConnectorField): z.ZodType<ConfigValue> {
  const required = isRequired(field);
  switch (field.type) {
    case FieldTypeBool:
      return z.boolean();
    case FieldTypeStringList: {
      const list = z.array(z.string());
      return required ? list.min(1, msg("validation.required")) : list;
    }
    default: {
      let text = z.string().trim();
      if (required) text = text.min(1, msg("validation.required"));
      if (field.type === FieldTypeURL) {
        return text.refine((v) => v === "" || isUrl(v), msg("validation.url"));
      }
      return text;
    }
  }
}

/** zod schema of the whole form for one connector kind. */
export function connectorFormSchema(fields: ConnectorField[]) {
  return z.object({
    name: z.string().trim().min(1, msg("validation.required")),
    config: z.object(Object.fromEntries(fields.map((f) => [f.name, fieldSchema(f)]))),
  });
}

function emptyValue(field: ConnectorField): ConfigValue {
  if (field.type === FieldTypeBool) return false;
  if (field.type === FieldTypeStringList) return [];
  return "";
}

function coerce(field: ConnectorField, value: unknown): ConfigValue | undefined {
  if (field.type === FieldTypeBool) return typeof value === "boolean" ? value : undefined;
  if (field.type === FieldTypeStringList) {
    return Array.isArray(value) ? value.filter((v) => typeof v === "string") : undefined;
  }
  return typeof value === "string" ? value : undefined;
}

/** Form values from the stored config (editing) or the schema defaults (adding). */
export function connectorFormDefaults(
  fields: ConnectorField[],
  initial?: { name: string; config: Record<string, unknown> },
): ConnectorFormValues {
  const config: Record<string, ConfigValue> = {};
  for (const f of fields) {
    config[f.name] = coerce(f, initial?.config[f.name]) ?? coerce(f, f.default) ?? emptyValue(f);
  }
  return { name: initial?.name ?? "", config };
}

export function ConnectorForm({
  kind,
  initial,
  submitLabel,
  pending,
  onSubmit,
  onCancel,
  footer,
}: {
  kind: ConnectorKind;
  initial?: { name: string; config: Record<string, unknown> };
  submitLabel: string;
  pending: boolean;
  onSubmit: (values: ConnectorFormValues, form: ConnectorFormApi) => void;
  onCancel?: () => void;
  /** Extra content between the fields and the buttons. */
  footer?: ReactNode;
}) {
  const t = useT();
  const schema = useMemo(() => connectorFormSchema(kind.fields), [kind.fields]);
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<ConnectorFormValues>({
    resolver: zodResolver(schema) as never,
    defaultValues: connectorFormDefaults(kind.fields, initial),
  });

  const api: ConnectorFormApi = {
    setFormError,
    applyServerErrors: (fields) => {
      let applied = false;
      for (const [key, reason] of Object.entries(fields)) {
        const bare = key.replace(/^config\./, "");
        const path =
          key === "name"
            ? "name"
            : kind.fields.some((f) => f.name === bare)
              ? `config.${bare}`
              : null;
        if (!path) continue;
        form.setError(path as Path<ConnectorFormValues>, { type: "server", message: reason });
        applied = true;
      }
      return applied;
    },
  };

  const submit = form.handleSubmit((values) => {
    setFormError(null);
    onSubmit(schema.parse(values) as ConnectorFormValues, api);
  });

  return (
    <Form {...form}>
      <form onSubmit={submit} noValidate className="space-y-5">
        <FormAlert>{formError}</FormAlert>
        <TextField
          control={form.control}
          name="name"
          label={t("connectors.name")}
          description={t("connectors.nameHint")}
          autoComplete="off"
        />
        {kind.fields.map((field) => (
          <SchemaField key={field.name} field={field} form={form} />
        ))}
        {footer}
        <div className="flex flex-col-reverse gap-2 pt-1 sm:flex-row sm:justify-end">
          {onCancel ? (
            <Button type="button" variant="outline" onClick={onCancel}>
              {t("common.cancel")}
            </Button>
          ) : null}
          <Button type="submit" disabled={pending}>
            {pending ? t("common.saving") : submitLabel}
          </Button>
        </div>
      </form>
    </Form>
  );
}

function SchemaField({
  field,
  form,
}: {
  field: ConnectorField;
  form: ReturnType<typeof useForm<ConnectorFormValues>>;
}) {
  const name = `config.${field.name}` as Path<ConnectorFormValues>;
  // A generated field keeps its plain label: its help says what leaving it empty does.
  const optional = !field.required && field.type !== FieldTypeBool;

  if (field.type === FieldTypeBool) {
    return (
      <FormField
        control={form.control}
        name={name}
        render={({ field: input }) => (
          <FormItem className="flex flex-row items-start justify-between gap-4 rounded-md border px-3 py-2.5">
            <div className="space-y-1">
              <FormLabel>{field.label}</FormLabel>
              {field.help ? (
                <FormDescription className="text-xs">{field.help}</FormDescription>
              ) : null}
              <FieldError />
            </div>
            <FormControl>
              <Switch checked={input.value === true} onCheckedChange={input.onChange} />
            </FormControl>
          </FormItem>
        )}
      />
    );
  }

  if (field.type === FieldTypeStringList) {
    return (
      <FormField
        control={form.control}
        name={name}
        render={({ field: input }) => (
          <FormItem>
            <OptionalLabel label={field.label} optional={optional} />
            <FormControl>
              <TagInput
                value={Array.isArray(input.value) ? input.value : []}
                onChange={input.onChange}
                onBlur={input.onBlur}
              />
            </FormControl>
            {field.help ? (
              <FormDescription className="text-xs">{field.help}</FormDescription>
            ) : null}
            <FieldError />
          </FormItem>
        )}
      />
    );
  }

  return (
    <TextField
      control={form.control}
      name={name}
      label={field.label}
      description={field.help}
      optional={optional}
      type={
        field.type === FieldTypeSecret ? "password" : field.type === FieldTypeURL ? "url" : "text"
      }
      autoComplete={field.type === FieldTypeSecret ? "new-password" : "off"}
      inputClassName={field.type === FieldTypeSecret ? "font-mono" : undefined}
    />
  );
}

function OptionalLabel({ label, optional }: { label: string; optional: boolean }) {
  const t = useT();
  return (
    <FormLabel>
      {label}
      {optional ? (
        <span className="font-normal text-muted-foreground">({t("auth.optional")})</span>
      ) : null}
    </FormLabel>
  );
}

/** Tag input for string lists: Enter or comma adds, Backspace on an empty box removes the last. */
function TagInput({
  value,
  onChange,
  onBlur,
  id,
  ...aria
}: {
  value: string[];
  onChange: (value: string[]) => void;
  onBlur: () => void;
  id?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
}) {
  const t = useT();
  const [draft, setDraft] = useState("");

  const commit = (raw: string) => {
    const parts = raw
      .split(",")
      .map((p) => p.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    onChange([...value, ...parts.filter((p) => !value.includes(p))]);
    setDraft("");
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      commit(draft);
    } else if (e.key === "Backspace" && draft === "" && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  return (
    <div
      className={cn(
        "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-md border border-input bg-transparent px-2 py-1.5 shadow-xs transition-[color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/70 dark:bg-input/30",
        aria["aria-invalid"] && "border-destructive",
      )}
    >
      {value.map((tag) => (
        <span
          key={tag}
          className="inline-flex max-w-full items-center gap-0.5 rounded-[3px] bg-muted py-0.5 pr-0.5 pl-1.5 font-mono text-xs"
        >
          <span className="truncate">{tag}</span>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("schema.removeTag", { value: tag })}
            onClick={() => onChange(value.filter((v) => v !== tag))}
          >
            <XIcon aria-hidden="true" />
          </Button>
        </span>
      ))}
      <input
        id={id}
        {...aria}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        onBlur={() => {
          commit(draft);
          onBlur();
        }}
        placeholder={value.length === 0 ? t("schema.tagPlaceholder") : undefined}
        autoComplete="off"
        spellCheck={false}
        className="min-w-24 flex-1 bg-transparent px-1 font-mono text-sm outline-none placeholder:font-sans placeholder:text-muted-foreground"
      />
    </div>
  );
}
