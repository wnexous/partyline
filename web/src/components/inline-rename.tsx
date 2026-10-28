import { CheckIcon, XIcon } from "lucide-react";
import { type FormEvent, useId, useState } from "react";
import { Button } from "@/shared/ui/primitives/button";
import { Input } from "@/shared/ui/primitives/input";
import { useT } from "@/lib/i18n";

/**
 * Renames in place: Enter saves, Escape cancels. The text is trimmed and counted in characters
 * (code points, like the server); `max` is the longest it may be, and `allowEmpty` lets it be
 * empty (the thing then goes back to its default). Saving what is already there just closes.
 * `rule` says what is wrong when the text is too long (or empty); `error` is the server's reason.
 */
export function InlineRename({
  label,
  initial,
  max,
  allowEmpty = false,
  placeholder,
  pending,
  rule,
  error,
  onSave,
  onCancel,
}: {
  /** Names the field for assistive technology. */
  label: string;
  initial: string;
  max: number;
  allowEmpty?: boolean;
  placeholder?: string;
  pending: boolean;
  rule: string;
  error?: string | null;
  onSave: (text: string) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [value, setValue] = useState(initial);
  const messageId = useId();
  const trimmed = value.trim();
  const length = [...trimmed].length;
  const invalid = length > max || (!allowEmpty && length < 1);
  const message = invalid ? rule : error;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (invalid || pending) return;
    if (trimmed === initial) return onCancel();
    onSave(trimmed);
  };
  return (
    <form onSubmit={submit} className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
      <Input
        autoFocus
        aria-label={label}
        aria-invalid={invalid || Boolean(error)}
        aria-describedby={message ? messageId : undefined}
        placeholder={placeholder}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            onCancel();
          }
        }}
        className="h-8 w-full max-w-72 min-w-0 font-semibold"
      />
      <Button
        type="submit"
        size="icon-sm"
        aria-label={t("common.save")}
        disabled={invalid || pending}
      >
        <CheckIcon aria-hidden="true" />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label={t("common.cancel")}
        onClick={onCancel}
      >
        <XIcon aria-hidden="true" />
      </Button>
      {message ? (
        <p id={messageId} className="basis-full text-xs text-destructive">
          {message}
        </p>
      ) : null}
    </form>
  );
}
