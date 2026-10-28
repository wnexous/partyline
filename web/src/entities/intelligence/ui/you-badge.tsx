import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Badge } from "@/shared/ui/primitives/badge";

/*
 * "You" beside the viewer's own name (PROF-09). It says whose name this is, never what the identity
 * is: an AI, a user and a person carry no mark of their kind (AD-024), so a list of names reads the
 * same whoever is in it.
 */

export interface YouBadgeProps {
  intelligence: Pick<IntelligenceRef, "id">;
  /** The viewer's intelligence id: the badge shows only on that identity. */
  viewerId?: string;
  className?: string;
}

export function YouBadge({ intelligence, viewerId, className }: YouBadgeProps) {
  const t = useT();
  if (intelligence.id === "" || intelligence.id !== viewerId) return null;
  return (
    <Badge
      variant="secondary"
      data-testid="you-badge"
      className={cn("px-1.5 py-0 text-[0.68rem] leading-5", className)}
    >
      {t("intelligence.you")}
    </Badge>
  );
}
