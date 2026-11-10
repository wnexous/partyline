import { PlusIcon } from "lucide-react";
import { useState } from "react";
import { RoutineSummary, useRoutines } from "@/entities/routine";
import { RoutineActions, RoutineDialog } from "@/features/manage-routines";
import { isApiError } from "@/shared/api/client";
import { MaxSchedulesPerAgent } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { PageSection } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

/**
 * The routines of an AI on its profile (HB-13). Whoever manages the AI sees each one — name, rule in
 * words, prompt, next and last run, who set it — and creates, pauses, runs, edits and deletes them.
 * Whoever only sees the AI learns how many it has and not what they say (HB-12); a viewer with no
 * routine to learn about, or who may not read them at all (403, 404), gets no section.
 */
export function RoutinesSection({ agentId, aiName }: { agentId: string; aiName: string }) {
  const t = useT();
  const query = useRoutines(agentId);
  const [creating, setCreating] = useState(false);

  if (query.isPending) {
    return (
      <div aria-hidden="true" className="space-y-3">
        <Skeleton className="h-6 w-28" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  if (query.isError) {
    if (isApiError(query.error) && [403, 404].includes(query.error.status)) return null;
    return (
      <PageSection layout="stacked" title={t("routines.title")}>
        <div role="alert" className="text-sm">
          <p className="text-destructive">{t("routines.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => query.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      </PageSection>
    );
  }

  const list = query.data;
  if (!list.can_manage) {
    if (list.count === 0) return null;
    return (
      <PageSection layout="stacked" title={t("routines.title")}>
        <p
          data-testid="routines-hidden"
          className="max-w-[65ch] text-sm text-pretty text-muted-foreground [overflow-wrap:anywhere]"
        >
          {list.count === 1
            ? t("routines.hiddenOne", { name: aiName })
            : t("routines.hiddenMany", { name: aiName, count: list.count })}
        </p>
      </PageSection>
    );
  }

  const full = list.count >= MaxSchedulesPerAgent;
  return (
    <PageSection
      layout="stacked"
      title={t("routines.title")}
      actions={
        <Button
          type="button"
          size="sm"
          className="pointer-coarse:h-11 pointer-coarse:px-4"
          disabled={full}
          onClick={() => setCreating(true)}
        >
          <PlusIcon aria-hidden="true" />
          {t("routines.new")}
        </Button>
      }
    >
      <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground [overflow-wrap:anywhere]">
        {full
          ? t("routines.tooMany", { max: MaxSchedulesPerAgent })
          : t("routines.description", { name: aiName })}
      </p>
      {list.items.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-5 text-sm text-pretty text-muted-foreground [overflow-wrap:anywhere]">
          {t("routines.empty", { name: aiName })}
        </p>
      ) : (
        <ul
          aria-label={t("routines.listLabel", { name: aiName })}
          className="divide-y rounded-lg border"
        >
          {list.items.map((routine) => (
            <li key={routine.id} className="px-4 py-4">
              <RoutineSummary
                routine={routine}
                aiName={aiName}
                actions={<RoutineActions agentId={agentId} aiName={aiName} routine={routine} />}
              />
            </li>
          ))}
        </ul>
      )}
      <RoutineDialog agentId={agentId} aiName={aiName} open={creating} onOpenChange={setCreating} />
    </PageSection>
  );
}
