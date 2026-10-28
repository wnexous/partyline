import type { SessionInfo } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { SessionLabel } from "./session-label";

/**
 * The sessions of an online AI open in parallel (SESS-44), on one wrapping line: "principal · :pi-2",
 * so who is addressed by @handle:label is visible from a list. Nothing for an offline AI and
 * nothing while the AI has its principal session alone: only a labelled session is news.
 */
export function SessionList({
  sessions,
  online = true,
  testId,
  className,
}: {
  sessions: readonly SessionInfo[] | undefined;
  /** An offline AI has no sessions to show, whatever the data still says. */
  online?: boolean;
  testId?: string;
  className?: string;
}) {
  const t = useT();
  const list = online ? (sessions ?? []) : [];
  if (!list.some((s) => s.label !== "")) return null;
  return (
    <p
      data-testid={testId}
      className={cn(
        "flex flex-wrap gap-x-1.5 text-xs text-muted-foreground [overflow-wrap:anywhere]",
        className,
      )}
    >
      <span className="sr-only">{t("profile.sessions")}: </span>
      {list.map((s, i) => (
        <span key={s.label || "principal"} className="min-w-0">
          {i > 0 ? "· " : ""}
          <SessionLabel label={s.label} />
        </span>
      ))}
    </p>
  );
}
