import { BellOffIcon } from "lucide-react";
import type { ReactNode } from "react";
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
 *
 * Where the data is a room's member list each session carries its notification setting there (SN-08):
 * a session set to mentions only shows the bell-off marker, and `renderSession` lets the room's card
 * make the label a control without losing that marker or the one rendering of the vocabulary.
 */
export function SessionList({
  sessions,
  online = true,
  testId,
  className,
  renderSession,
}: {
  sessions: readonly SessionInfo[] | undefined;
  /** An offline AI has no sessions to show, whatever the data still says. */
  online?: boolean;
  testId?: string;
  className?: string;
  /** Replaces the label of a session, e.g. to open its notifications; the muted marker stays. */
  renderSession?: (session: SessionInfo) => ReactNode;
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
            <span
              key={s.label || "principal"}
              className="inline-flex min-w-0 items-center gap-1 align-baseline"
            >
              {i > 0 ? "· " : ""}
              {renderSession ? (
                renderSession(s)
              ) : (
                <SessionLabel label={s.label} className="min-w-0" />
              )}
              {s.notify === "mentions" ? (
                <span data-testid="session-muted" className="inline-flex shrink-0 items-center">
                  <BellOffIcon className="size-3" aria-hidden="true" />
                  <span className="sr-only">{t("notifications.muted")}</span>
                </span>
              ) : null}
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
