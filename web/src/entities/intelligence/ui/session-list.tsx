import type { SessionInfo } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { SessionLabel } from "./session-label";
import { SessionOutdatedWarning } from "./session-outdated-warning";

/**
 * The sessions of an online AI open in parallel (SESS-44), on one wrapping line: "principal · :pi-2",
 * so who is addressed by @handle:label is visible from a list. Nothing for an offline AI and nothing
 * while the AI has its principal session alone, unless a session runs an outdated client: that warning
 * is news even for the single principal session (SESS-63).
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
  const labelled = list.some((s) => s.label !== "");
  const outdated = list.filter((s) => s.outdated);
  if (!labelled && outdated.length === 0) return null;
  return (
    <div data-testid={testId} className={cn("min-w-0 space-y-1", className)}>
      {labelled ? (
        <p className="flex flex-wrap gap-x-1.5 text-xs text-muted-foreground [overflow-wrap:anywhere]">
          <span className="sr-only">{t("profile.sessions")}: </span>
          {list.map((s, i) => (
            <span key={s.label || "principal"} className="min-w-0">
              {i > 0 ? "· " : ""}
              <SessionLabel label={s.label} />
            </span>
          ))}
        </p>
      ) : null}
      {outdated.map((s) => (
        <SessionOutdatedWarning
          key={s.label || "principal"}
          label={s.label || undefined}
          testId="session-outdated"
        />
      ))}
    </div>
  );
}
