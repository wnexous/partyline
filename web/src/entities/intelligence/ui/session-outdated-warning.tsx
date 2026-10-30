import { TriangleAlertIcon } from "lucide-react";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";

/** The command the warning tells the person to run; it is set in the interface's data face. */
const UPDATE_COMMAND = "partyline update";

/**
 * "Outdated client — run `partyline update` and reopen this session" (SESS-63): the notes-and-cautions
 * style of DESIGN.md, a muted panel with a `tape-strong` hairline and an alert icon, and no colour of
 * its own. With a label it names the session; the profile header passes the principal's own sentence.
 */
export function SessionOutdatedWarning({
  label,
  text,
  testId,
  className,
}: {
  /** The session's label; absent for the principal and for the profile header's notice. */
  label?: string;
  /** The sentence to show; the per-session one when omitted. */
  text?: string;
  testId?: string;
  className?: string;
}) {
  const t = useT();
  const [before, after] = splitUpdateCommand(text ?? t("profile.sessionOutdated"));
  return (
    <p
      data-testid={testId}
      className={cn(
        "flex items-start gap-1.5 rounded-md border border-tape-strong/40 bg-muted px-2 py-1 text-xs [overflow-wrap:anywhere]",
        className,
      )}
    >
      <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0">
        {label ? <span className="font-mono">:{label} — </span> : null}
        {before}
        <span className="font-mono">{UPDATE_COMMAND}</span>
        {after}
      </span>
    </p>
  );
}

/** splitUpdateCommand cuts the sentence around the command, so the command can wear the mono face. */
function splitUpdateCommand(sentence: string): [string, string] {
  const at = sentence.indexOf(UPDATE_COMMAND);
  if (at < 0) return [sentence, ""];
  return [sentence.slice(0, at), sentence.slice(at + UPDATE_COMMAND.length)];
}
