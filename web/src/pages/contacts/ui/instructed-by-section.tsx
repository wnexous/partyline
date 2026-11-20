import { PlusIcon, UserMinusIcon } from "lucide-react";
import { useState } from "react";
import { IntelligenceRow } from "@/entities/intelligence";
import { AddInstructorDialog, RemoveInstructorDialog } from "@/features/manage-instructors";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { PageSection } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";

/**
 * Who can instruct an AI (SR2-07, SR2-10): its host, always, and the identities the host named as its
 * instructors. Their messages are the AI's trusted ones; nothing else makes a message trusted. The
 * host adds an instructor by @handle and removes one (`canEdit`); everyone else who is shown the list
 * only reads it.
 */
export function InstructedBySection({
  ai,
  host,
  instructors,
  canEdit,
  viewerId,
}: {
  ai: { id: string; name: string };
  host: IntelligenceRef;
  instructors: readonly IntelligenceRef[];
  canEdit: boolean;
  viewerId: string | undefined;
}) {
  const t = useT();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<IntelligenceRef | null>(null);
  return (
    <PageSection
      layout="stacked"
      title={t("profile.instructedBy")}
      actions={
        canEdit ? (
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            <PlusIcon aria-hidden="true" />
            {t("instructors.add")}
          </Button>
        ) : undefined
      }
    >
      <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground [overflow-wrap:anywhere]">
        {instructors.length === 0
          ? t("profile.instructedByHostOnly", { name: ai.name })
          : t("profile.instructedByBody", { name: ai.name })}
      </p>
      <ul aria-label={t("profile.instructedBy")} className="divide-y rounded-lg border">
        <IntelligenceRow
          intelligence={host}
          viewerId={viewerId}
          className="py-3"
          meta={t("profile.host")}
        />
        {instructors.map((who) => (
          <IntelligenceRow
            key={who.id}
            intelligence={who}
            viewerId={viewerId}
            className="py-3"
            actions={
              canEdit ? (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-destructive pointer-coarse:size-11"
                  aria-label={t("instructors.removeAction", { who: who.display_name })}
                  title={t("instructors.remove")}
                  onClick={() => setRemoving(who)}
                >
                  <UserMinusIcon aria-hidden="true" />
                </Button>
              ) : undefined
            }
          />
        ))}
      </ul>
      {canEdit ? (
        <>
          <AddInstructorDialog ai={ai} open={adding} onOpenChange={setAdding} />
          <RemoveInstructorDialog ai={ai} instructor={removing} onClose={() => setRemoving(null)} />
        </>
      ) : null}
    </PageSection>
  );
}
