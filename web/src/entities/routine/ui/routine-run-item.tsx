import {
  CircleAlertIcon,
  CircleCheckIcon,
  CircleMinusIcon,
  ClockIcon,
  type LucideIcon,
} from "lucide-react";
import type { ScheduleRun } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { Badge } from "@/shared/ui/primitives/badge";
import { type RunOutcome, runOutcome, runStatusText } from "../lib/rule";

const OUTCOME_ICON: Record<RunOutcome, { icon: LucideIcon; className: string }> = {
  delivered: { icon: CircleCheckIcon, className: "text-online" },
  waiting: { icon: ClockIcon, className: "text-muted-foreground" },
  failed: { icon: CircleAlertIcon, className: "text-destructive" },
  skipped: { icon: CircleMinusIcon, className: "text-muted-foreground" },
};

/**
 * One run of a routine as whoever manages the AI reads it (HB-11): what became of it in words with a
 * mark that says it at a glance, whether it was a manual "Run now", when it was due (relative and
 * absolute, both shown: a phone has no tooltip) and whether the AI acknowledged it, and when.
 */
export function RoutineRunItem({ run, aiName }: { run: ScheduleRun; aiName: string }) {
  const { t, locale } = useI18n();
  const outcome = runOutcome(run.status);
  const { icon: Icon, className } = OUTCOME_ICON[outcome];
  const waiting = outcome === "waiting";
  return (
    <div className="space-y-1 text-sm" data-testid={`routine-run-${run.id}`}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="inline-flex min-w-0 items-start gap-1.5 font-medium">
          <Icon className={cn("mt-0.5 size-4 shrink-0", className)} aria-hidden="true" />
          <span
            data-testid="routine-run-status"
            className="inline-block [overflow-wrap:anywhere] first-letter:uppercase"
          >
            {runStatusText(t, run.status)}
          </span>
        </span>
        {run.manual ? (
          <Badge variant="outline" data-testid="routine-run-manual">
            {t("routines.runManual")}
          </Badge>
        ) : null}
      </div>
      <p className="text-muted-foreground" data-testid="routine-run-due">
        <RelativeTime value={run.due_at} locale={locale}>
          {(time) => t("routines.runDue", { time })}
        </RelativeTime>
        {" · "}
        <RelativeTime value={run.due_at} locale={locale} format="date" />
      </p>
      <p className="text-muted-foreground [overflow-wrap:anywhere]" data-testid="routine-run-ack">
        {run.acked_at ? (
          <RelativeTime value={run.acked_at} locale={locale}>
            {(time) => t("routines.runAcked", { name: aiName, time })}
          </RelativeTime>
        ) : run.fired_at ? (
          <RelativeTime value={run.fired_at} locale={locale}>
            {(time) =>
              waiting
                ? t("routines.runSentWaiting", { name: aiName, time })
                : t("routines.runSentUnacked", { name: aiName, time })
            }
          </RelativeTime>
        ) : waiting ? (
          t("routines.runNotSentYet", { name: aiName })
        ) : (
          t("routines.runNotSent", { name: aiName })
        )}
      </p>
    </div>
  );
}
