import { ChevronRightIcon, HistoryIcon } from "lucide-react";
import { useId, useState } from "react";
import { isApiError } from "@/shared/api/client";
import type { Schedule } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { useRoutineRuns } from "../api/routines";
import { RoutineRunItem } from "./routine-run-item";

/** How many runs the server returns at most (`schedules.RunsPageSize`). */
const RUNS_PAGE = 50;

/**
 * The recent runs of one routine, closed until whoever manages the AI opens it (HB-11): the runs are
 * read then, not with the list, and read again when the routines change (the live frame, a write).
 * Only a manager's row has it; the server refuses anyone else (403 for a viewer, 404 otherwise), and
 * such a refusal is said without a retry.
 */
export function RoutineHistory({
  agentId,
  aiName,
  routine,
}: {
  agentId: string;
  aiName: string;
  routine: Pick<Schedule, "id" | "name">;
}) {
  const { t } = useI18n();
  const panelId = useId();
  const [open, setOpen] = useState(false);
  const runs = useRoutineRuns(agentId, routine.id, open);
  const label = t("routines.historyLabel", { name: routine.name });
  const refused = runs.isError && isApiError(runs.error) ? runs.error.status : 0;

  return (
    <div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="-ml-2 pointer-coarse:h-11 pointer-coarse:px-3"
        aria-expanded={open}
        aria-controls={panelId}
        aria-label={label}
        data-testid="routine-history-toggle"
        onClick={() => setOpen(!open)}
      >
        <ChevronRightIcon
          aria-hidden="true"
          className={cn("transition-transform motion-reduce:transition-none", open && "rotate-90")}
        />
        <HistoryIcon aria-hidden="true" />
        {t("routines.history")}
      </Button>
      {open ? (
        <div id={panelId} className="mt-2" data-testid="routine-history">
          {runs.isError && !runs.data ? (
            <div role="alert" className="text-sm">
              {refused === 404 ? (
                <p className="text-destructive">{t("routines.historyGone")}</p>
              ) : refused === 403 ? (
                <p className="text-destructive">
                  {t("routines.historyForbidden", { name: aiName })}
                </p>
              ) : (
                <>
                  <p className="text-destructive">{t("routines.historyLoadError")}</p>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="mt-3 pointer-coarse:h-11 pointer-coarse:px-4"
                    onClick={() => void runs.refetch()}
                  >
                    {t("common.retry")}
                  </Button>
                </>
              )}
            </div>
          ) : runs.isPending ? (
            <div aria-hidden="true" className="space-y-3 rounded-lg border px-3 py-3">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-3 w-56 max-w-full" />
              <Skeleton className="h-3 w-48 max-w-full" />
            </div>
          ) : runs.data.length === 0 ? (
            <p
              role="status"
              className="rounded-lg border border-dashed px-3 py-3 text-sm text-pretty text-muted-foreground"
            >
              {t("routines.historyEmpty")}
            </p>
          ) : (
            <>
              <ol aria-label={label} className="divide-y rounded-lg border">
                {runs.data.map((run) => (
                  <li key={run.id} className="px-3 py-3">
                    <RoutineRunItem run={run} aiName={aiName} />
                  </li>
                ))}
              </ol>
              {runs.data.length >= RUNS_PAGE ? (
                <p className="mt-2 text-xs text-muted-foreground">
                  {t("routines.historyNewest", { count: RUNS_PAGE })}
                </p>
              ) : null}
            </>
          )}
        </div>
      ) : null}
    </div>
  );
}
