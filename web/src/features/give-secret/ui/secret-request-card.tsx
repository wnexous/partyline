import { useQuery } from "@tanstack/react-query";
import { KeyRoundIcon } from "lucide-react";
import { type FormEvent, type ReactNode, useId, useState } from "react";
import { handleText, intelligenceQuery, managesAI } from "@/entities/intelligence";
import { authorDisplayName } from "@/entities/message";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeAIOffline,
  ErrorCodeAlreadyAnswered,
  ErrorCodeClientOutdated,
  ErrorCodeConflict,
  ErrorCodeForbidden,
  ErrorCodeNotSaved,
  MaxSecretValue,
  type Message,
} from "@/shared/api/types.gen";
import { type MessageKey, useI18n } from "@/shared/i18n";
import { PasswordInput } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Label } from "@/shared/ui/primitives/label";
import { useGiveSecret } from "../api/use-give-secret";

/*
 * The card of an AI's secret request (SR-04): the secret's name under the reason the message text
 * gives, and, for a viewer who manages the asking AI and may post here, a masked field and Save. The
 * value is posted once and the field is emptied at once, whatever the answer: it is never kept in the
 * card, the cache or a toast. Everyone else reads who may give it. The saved state comes from the
 * timeline (the answer reply, whoever gave it), so a reload keeps it.
 */

export interface SecretRequestCardProps {
  /** The secret request: a message whose interactive part names a secret. */
  message: Message;
  /** The viewer's intelligence id: whether they manage the asking AI decides what they see. */
  meId: string;
  /** The answer that saved the secret, by whoever gave it, when the timeline holds it (SR-06). */
  answer?: Message;
  /** The viewer may post in this conversation; giving a secret posts the answer. */
  canAnswer?: boolean;
  /** The conversation is history (CID-21): nobody gives a secret in it. */
  frozen?: boolean;
}

/** The sentence of a refusal, for the error codes the route answers (SR-05, SR-06, SR-10). */
const REFUSALS: Record<string, MessageKey> = {
  [ErrorCodeAIOffline]: "secret.offline",
  [ErrorCodeClientOutdated]: "secret.outdated",
  [ErrorCodeNotSaved]: "secret.notSaved",
  [ErrorCodeAlreadyAnswered]: "secret.alreadySaved",
  [ErrorCodeConflict]: "secret.busy",
  [ErrorCodeForbidden]: "secret.forbidden",
};

const byteLength = (value: string) => new TextEncoder().encode(value).length;

export function SecretRequestCard({
  message,
  meId,
  answer,
  canAnswer = false,
  frozen = false,
}: SecretRequestCardProps) {
  const { t } = useI18n();
  const fieldId = useId();
  const hintId = useId();
  const give = useGiveSecret(message.room_id);
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  // This card's own save, while the timeline catches up with the answer's live event.
  const [savedHere, setSavedHere] = useState<Message | null>(null);

  const name = message.interactive?.secret?.name ?? "";
  const handle = message.author?.handle ? handleText(message.author.handle) : message.author_name;
  const saved = answer?.answers?.secret?.saved ? answer : savedHere;
  const mayGive = !saved && canAnswer && !frozen;
  // Only a viewer who could give it needs to know whether they manage the AI.
  const profile = useQuery({
    ...intelligenceQuery(message.author_id ?? ""),
    enabled: mayGive && !!message.author_id,
  });
  const manages = managesAI(profile.data, meId);
  const pending = give.isPending;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (pending || value === "") return;
    const given = value;
    setValue(""); // the field never keeps the value after a save attempt
    if (byteLength(given) > MaxSecretValue) {
      setError(t("secret.tooLong", { max: MaxSecretValue }));
      return;
    }
    setError(null);
    give.mutate(
      { messageId: message.id, value: given },
      {
        onSuccess: (reply) => setSavedHere(reply),
        onError: (err) => {
          const key = isApiError(err) ? REFUSALS[err.code] : undefined;
          setError(t(key ?? "secret.failed", { handle }));
        },
        // The mutation would keep its variables, the value among them, until the next one.
        onSettled: () => give.reset(),
      },
    );
  };

  let body: ReactNode = null;
  if (saved) {
    body = (
      <p role="status" className="text-sm text-muted-foreground">
        {t("secret.saved", { name: authorDisplayName(saved) })}
      </p>
    );
  } else if (frozen) {
    body = <p className="text-sm text-muted-foreground">{t("secret.frozen")}</p>;
  } else if (mayGive && profile.isPending) {
    body = null; // whether the viewer may give it is not known yet
  } else if (mayGive && manages) {
    body = (
      <form className="flex min-w-0 flex-col gap-2" onSubmit={submit} noValidate>
        {/* The card names the secret above: the visible label is short, its accessible name is whole. */}
        <Label htmlFor={fieldId}>{t("secret.value")}</Label>
        <div className="flex min-w-0 flex-wrap gap-2">
          <div className="min-w-0 flex-1 basis-40">
            <PasswordInput
              id={fieldId}
              name={`secret-${message.id}`}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              aria-label={t("secret.label", { name })}
              aria-describedby={hintId}
              aria-invalid={error ? true : undefined}
              disabled={pending}
              className="pointer-coarse:h-11 pointer-coarse:pr-12"
              toggleClassName="pointer-coarse:right-0 pointer-coarse:size-11"
              showLabel={t("secret.show")}
              hideLabel={t("secret.hide")}
            />
          </div>
          <Button
            type="submit"
            aria-disabled={pending || value === "" ? "true" : undefined}
            className="pointer-coarse:min-h-11 aria-disabled:cursor-default aria-disabled:opacity-50"
          >
            {pending ? t("secret.saving") : t("secret.save")}
          </Button>
        </div>
        <p id={hintId} className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
          {t("secret.hint", { handle })}
        </p>
        <p role="status" className="sr-only">
          {pending ? t("secret.saving") : ""}
        </p>
        {error ? (
          <p role="alert" className="text-sm text-destructive [overflow-wrap:anywhere]">
            {error}
          </p>
        ) : null}
      </form>
    );
  } else {
    body = (
      <p className="text-sm text-muted-foreground [overflow-wrap:anywhere]">
        {t("secret.waiting", { handle })}
      </p>
    );
  }

  return (
    <section
      aria-label={t("secret.card", { name })}
      data-testid="secret-request"
      className="mt-1.5 flex min-w-0 max-w-full flex-col gap-2 rounded-md border border-border bg-card p-3"
    >
      <p className="flex min-w-0 items-center gap-2 text-sm font-medium text-foreground">
        <KeyRoundIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
        <code className="min-w-0 font-mono [overflow-wrap:anywhere]">{name}</code>
      </p>
      {body}
    </section>
  );
}
