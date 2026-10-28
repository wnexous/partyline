import { PlusIcon, XIcon } from "lucide-react";
import { type KeyboardEvent, useId } from "react";
import { validationVars } from "@/lib/schemas";
import { MaxMemberKeywords } from "@/shared/api/types.gen";
import { type MessageKey, useI18n } from "@/shared/i18n";
import { errorText } from "@/shared/lib/forms";
import { FieldError } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  useFormField,
} from "@/shared/ui/primitives/form";
import { Input } from "@/shared/ui/primitives/input";

/**
 * Keywords as removable chips plus one input; Enter, the add button or Save commits the draft.
 * The draft message is a live region that also describes the input (A11Y-01).
 */
export function KeywordsField(props: {
  draft: string;
  draftError: MessageKey | null;
  onDraft: (value: string) => void;
  onCommit: () => boolean;
}) {
  return (
    <FormField
      name="keywords"
      render={({ field }) => (
        <FormItem>
          <KeywordsBody {...props} list={field.value as string[]} onChange={field.onChange} />
        </FormItem>
      )}
    />
  );
}

function KeywordsBody({
  draft,
  draftError,
  onDraft,
  onCommit,
  list,
  onChange,
}: {
  draft: string;
  draftError: MessageKey | null;
  onDraft: (value: string) => void;
  onCommit: () => boolean;
  list: string[];
  onChange: (list: string[]) => void;
}) {
  const { t } = useI18n();
  const { error, formDescriptionId, formMessageId } = useFormField();
  const messageId = useId();
  const full = list.length >= MaxMemberKeywords;
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      onCommit();
    }
  };
  const message = draftError
    ? errorText(t, draftError)
    : full
      ? t("validation.keywords", validationVars["validation.keywords"])
      : null;
  const describedBy = [formDescriptionId, message ? messageId : "", error ? formMessageId : ""]
    .filter(Boolean)
    .join(" ");
  return (
    <>
      <FormLabel>{t("memberSettings.keywords")}</FormLabel>
      {list.length > 0 ? (
        <ul className="flex flex-wrap gap-1.5">
          {list.map((k) => (
            <li
              key={k}
              className="inline-flex max-w-full items-center gap-0.5 rounded-sm border border-tape-strong/70 bg-tape/40 py-0.5 pr-0.5 pl-2 font-mono text-xs"
            >
              <span className="truncate">{k}</span>
              <button
                type="button"
                className="inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-foreground/10 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/70 focus-visible:outline-none"
                aria-label={t("memberSettings.keywordRemove", { keyword: k })}
                onClick={() => onChange(list.filter((x) => x !== k))}
              >
                <XIcon className="size-3" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="flex gap-2">
        <FormControl>
          <Input
            value={draft}
            onChange={(e) => onDraft(e.target.value)}
            onKeyDown={onKeyDown}
            disabled={full}
            placeholder={t("memberSettings.keywordPlaceholder")}
            aria-invalid={draftError || error ? true : undefined}
            aria-describedby={describedBy}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            className="font-mono text-sm"
          />
        </FormControl>
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="shrink-0"
          disabled={full}
          aria-label={t("memberSettings.keywordAdd")}
          onClick={onCommit}
        >
          <PlusIcon aria-hidden="true" />
        </Button>
      </div>
      <FormDescription className="text-xs">{t("memberSettings.keywordsHint")}</FormDescription>
      <p id={messageId} aria-live="polite" className="text-sm text-destructive empty:hidden">
        {message}
      </p>
      <FieldError />
    </>
  );
}
