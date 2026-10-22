import { KindBadge } from "@/components/intelligence/kind-badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { cn } from "@/lib/utils";

/*
 * A display name with its kind badge and @handle (PROF-09). The handle lives in a tooltip that
 * also opens on keyboard focus, or inline in detail views through `showHandle`.
 */

export interface IntelligenceNameProps {
  intelligence: IntelligenceRef;
  /** The viewer's intelligence id: makes the badge say "You". */
  viewerId?: string;
  /** Shows the @handle next to the name instead of only in the tooltip. */
  showHandle?: boolean;
  className?: string;
}

const NAME = "min-w-0 truncate font-semibold tracking-[-0.01em]";

export function IntelligenceName({
  intelligence,
  viewerId,
  showHandle = false,
  className,
}: IntelligenceNameProps) {
  const name = intelligence.display_name.trim() || intelligence.handle || "?";
  const handle = intelligence.handle ? `@${intelligence.handle}` : "";
  return (
    <span
      data-testid="intelligence-name"
      className={cn("inline-flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5", className)}
    >
      {handle && !showHandle ? (
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              className={cn(
                NAME,
                "cursor-default appearance-none border-0 bg-transparent p-0 text-left text-inherit focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
              )}
            >
              {name}
            </button>
          </TooltipTrigger>
          <TooltipContent>{handle}</TooltipContent>
        </Tooltip>
      ) : (
        <span className={NAME}>{name}</span>
      )}
      <KindBadge intelligence={intelligence} viewerId={viewerId} />
      {showHandle && handle ? (
        <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">{handle}</span>
      ) : null}
    </span>
  );
}
