import { z } from "zod";
import {
  type ConnectorField,
  FieldTypeBool,
  FieldTypeStringList,
  FieldTypeURL,
} from "@/shared/api/types.gen";
import type { MessageKey } from "@/shared/i18n";

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

export function emptyValue(field: ConnectorField): ConfigValue {
  if (field.type === FieldTypeBool) return false;
  if (field.type === FieldTypeStringList) return [];
  return "";
}

export function coerce(field: ConnectorField, value: unknown): ConfigValue | undefined {
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
