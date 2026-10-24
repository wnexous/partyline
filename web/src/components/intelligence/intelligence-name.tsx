import { KindBadge } from "@/components/intelligence/kind-badge";
import { ProfileLink } from "@/components/profile-link";
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
  /**
   * Makes the name a link to the intelligence's profile (PDM-03, PRF-01): a click opens the profile
   * panel, a click with Ctrl, Cmd or Shift the page. The @handle stays in the tooltip of that same
   * link.
   */
  profileLink?: boolean;
  /**
   * With `profileLink`, lets the link cover the row it sits in (a `relative` ancestor), so the whole
   * row is the target and the row's own buttons, made `relative` too, stay above it. Still one tab
   * stop.
   */
  stretch?: boolean;
  className?: string;
}

const NAME = "min-w-0 truncate font-semibold tracking-[-0.01em]";
const FOCUS = "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";
/** The link's focus ring is drawn on the row it covers, since the link itself is only the name. */
const STRETCH =
  "outline-none after:absolute after:inset-0 focus-visible:after:ring-2 focus-visible:after:ring-ring focus-visible:after:ring-inset";

export function IntelligenceName({
  intelligence,
  viewerId,
  showHandle = false,
  profileLink = false,
  stretch = false,
  className,
}: IntelligenceNameProps) {
  const name = intelligence.display_name.trim() || intelligence.handle || "?";
  const handle = intelligence.handle ? `@${intelligence.handle}` : "";
  // The handle's tooltip trigger: the profile link when the name is one, else the plain button.
  const nameEl = profileLink ? (
    <ProfileLink id={intelligence.id} className={cn(NAME, "rounded-sm", stretch ? STRETCH : FOCUS)}>
      {name}
    </ProfileLink>
  ) : handle && !showHandle ? (
    <button
      type="button"
      className={cn(
        NAME,
        "cursor-default appearance-none border-0 bg-transparent p-0 text-left text-inherit",
        FOCUS,
      )}
    >
      {name}
    </button>
  ) : (
    <span className={NAME}>{name}</span>
  );
  return (
    <span
      data-testid="intelligence-name"
      className={cn("inline-flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5", className)}
    >
      {handle && !showHandle ? (
        <Tooltip>
          <TooltipTrigger asChild>{nameEl}</TooltipTrigger>
          <TooltipContent>{handle}</TooltipContent>
        </Tooltip>
      ) : (
        nameEl
      )}
      <KindBadge intelligence={intelligence} viewerId={viewerId} />
      {showHandle && handle ? (
        <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">{handle}</span>
      ) : null}
    </span>
  );
}
