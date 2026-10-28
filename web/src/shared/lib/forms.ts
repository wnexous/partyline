import type { FieldValues, Path, UseFormSetError } from "react-hook-form";
import { isApiError } from "@/lib/api/client";
import { dictionaries, type MessageKey, type Translate } from "@/shared/i18n";
import { validationVars } from "@/lib/schemas";

/** Renders a field error: schema errors carry i18n keys, server errors carry plain text. */
export function errorText(t: Translate, message: string | undefined): string {
  if (!message) return "";
  if (message in dictionaries.en) {
    const key = message as MessageKey;
    return t(key, (validationVars as Record<string, Record<string, number>>)[key]);
  }
  return message;
}

/**
 * Maps a 422 response onto the form (UI-01, UI-03): known field reasons go under their inputs;
 * returns true when at least one field error was applied, so the caller can skip a form-level
 * message.
 */
export function applyFieldErrors<T extends FieldValues>(
  err: unknown,
  setError: UseFormSetError<T>,
  fields: readonly Path<T>[],
): boolean {
  if (!isApiError(err) || err.status !== 422) return false;
  let applied = false;
  for (const [name, reason] of Object.entries(err.fields)) {
    if ((fields as readonly string[]).includes(name)) {
      setError(name as Path<T>, { type: "server", message: reason });
      applied = true;
    }
  }
  return applied;
}

/** Only in-app absolute paths are valid login return targets (no open redirects). */
export function safeRedirect(target: string | undefined, fallback = "/"): string {
  if (!target?.startsWith("/") || target.startsWith("//") || target.startsWith("/\\")) {
    return fallback;
  }
  if (target === "/login" || target.startsWith("/login?") || target.startsWith("/setup")) {
    return fallback;
  }
  return target;
}
