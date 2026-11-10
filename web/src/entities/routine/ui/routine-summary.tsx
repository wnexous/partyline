import { PauseIcon } from "lucide-react";
import type { ReactNode } from "react";
import { type Schedule, ScheduleOriginSelf } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { browserTimeZone } from "@/shared/lib/time";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { describeRoutine, runStatusText } from "../lib/rule";

/**
 * One routine of an AI as whoever manages it reads it (HB-13): its name, the rule in words, the
 * prompt, when it runs next (or that it is paused, and by whom), its last run and what became of it,
 * and who set it. `actions` is the row's controls, under the facts; the caller decides who gets them.
 */
export function RoutineSummary({
  routine,
  aiName,
  actions,
}: {
  routine: Schedule;
  /** The AI's name, for "set by the AI itself" and the paused notice. */
  aiName: string;
  actions?: ReactNode;
}) {
  const { t, locale } = useI18n();
  const rule = describeRoutine(t, locale, routine, browserTimeZone());
  const setBy =
    routine.origin === ScheduleOriginSelf
      ? t("routines.setBySelf", { name: aiName })
      : t("routines.setBy", { name: routine.set_by.display_name || routine.set_by.handle });
  return (
    <div className="space-y-2" data-testid={`routine-${routine.id}`}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <h3 className="min-w-0 text-base font-semibold [overflow-wrap:anywhere]">{routine.name}</h3>
        {routine.enabled ? null : (
          <span
            data-testid="routine-paused"
            className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium"
          >
            <PauseIcon className="size-3" aria-hidden="true" />
            {t("routines.paused")}
          </span>
        )}
      </div>
      <p className="text-sm font-medium [overflow-wrap:anywhere]" data-testid="routine-rule">
        {rule}
      </p>
      {routine.prompt ? (
        <p className="line-clamp-4 max-w-[65ch] text-sm whitespace-pre-wrap text-muted-foreground [overflow-wrap:anywhere]">
          {routine.prompt}
        </p>
      ) : null}
      <ul className="space-y-0.5 text-sm text-muted-foreground">
        <li data-testid="routine-next">
          {routine.enabled ? (
            routine.next_run_at ? (
              <RelativeTime value={routine.next_run_at} locale={locale}>
                {(time) => t("routines.next", { time })}
              </RelativeTime>
            ) : (
              t("routines.noNext")
            )
          ) : routine.paused_by_manager ? (
            t("routines.pausedByManager", { name: aiName })
          ) : (
            t("routines.pausedPlain")
          )}
        </li>
        <li data-testid="routine-last">
          {routine.last_run_at ? (
            <>
              <RelativeTime value={routine.last_run_at} locale={locale}>
                {(time) => t("routines.last", { time })}
              </RelativeTime>
              {routine.last_status ? ` · ${runStatusText(t, routine.last_status)}` : null}
            </>
          ) : (
            t("routines.neverRan")
          )}
        </li>
        <li>{setBy}</li>
      </ul>
      {actions}
    </div>
  );
}
