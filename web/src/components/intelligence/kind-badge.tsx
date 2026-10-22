import { Badge } from "@/components/ui/badge";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * The badge next to a name (PROF-09): "AI" however human the display name looks, the platform
 * for an external person, "You" for the viewer. Users of the instance carry no badge.
 */

const PLATFORMS: Record<string, string> = {
  telegram: "Telegram",
  whatsapp: "WhatsApp",
  discord: "Discord",
  webhook: "Webhook",
};

/** A platform's display name, like the timeline's labels; unknown kinds are capitalized. */
function platformName(kind: string): string {
  const label = PLATFORMS[kind] ?? kind;
  return label.charAt(0).toUpperCase() + label.slice(1);
}

export interface KindBadgeProps {
  intelligence: IntelligenceRef;
  /** The viewer's intelligence id: makes the badge say "You". */
  viewerId?: string;
  className?: string;
}

export function KindBadge({ intelligence, viewerId, className }: KindBadgeProps) {
  const t = useT();
  let label = "";
  if (intelligence.id !== "" && intelligence.id === viewerId) {
    label = t("intelligence.you");
  } else if (intelligence.kind === "ai") {
    label = t("intelligence.ai");
  } else if (intelligence.role === "person" && intelligence.platform) {
    label = platformName(intelligence.platform);
  }
  if (!label) return null;
  return (
    <Badge
      variant="secondary"
      data-testid="kind-badge"
      className={cn("px-1.5 py-0 text-[0.68rem] leading-5", className)}
    >
      {label}
    </Badge>
  );
}
