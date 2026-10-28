import type { ElementType, ReactNode } from "react";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { cn } from "@/lib/utils";
import { IntelligenceAvatar } from "./intelligence-avatar";
import { IntelligenceName } from "./intelligence-name";

/**
 * A person or an AI in a list: avatar, name and @handle, with a `meta` line (presence, owner…), a
 * `sessions` line and `actions` on the right, all optional. The name links to the profile and
 * covers the row (PRF-01); the actions sit above it. The row wraps: a long name or a large font
 * pushes the lines down instead of out of the panel, and the actions stay a tap away.
 */
export interface IntelligenceRowProps {
  intelligence: IntelligenceRef;
  /** The viewer's intelligence id: marks the viewer's own name with "You". */
  viewerId?: string;
  /** Under the name: presence, owner, settings. */
  meta?: ReactNode;
  /** Under the meta line: the AI's live sessions (a `SessionList`). */
  sessions?: ReactNode;
  /** On the right: buttons and menus. The row marks itself `group/row` for their hover states. */
  actions?: ReactNode;
  /** Makes the name a link that covers the row. */
  profileLink?: boolean;
  showHandle?: boolean;
  /** The element of the row: `li` inside a list, `div` elsewhere. */
  as?: ElementType;
  testId?: string;
  className?: string;
}

export function IntelligenceRow({
  intelligence,
  viewerId,
  meta,
  sessions,
  actions,
  profileLink = true,
  showHandle = true,
  as: Row = "li",
  testId,
  className,
}: IntelligenceRowProps) {
  return (
    <Row
      data-testid={testId}
      className={cn(
        "group/row relative flex items-start gap-2.5 px-4 py-2 hover:bg-accent/40",
        className,
      )}
    >
      <IntelligenceAvatar intelligence={intelligence} decorative className="mt-0.5 shrink-0" />
      <div className="min-w-0 flex-1 space-y-0.5">
        <IntelligenceName
          intelligence={intelligence}
          viewerId={viewerId}
          showHandle={showHandle}
          profileLink={profileLink}
          stretch={profileLink}
          className="text-sm"
        />
        {meta ? (
          <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
            {meta}
          </p>
        ) : null}
        {sessions}
      </div>
      {actions ? (
        <div className="relative flex shrink-0 flex-wrap items-start justify-end gap-2.5">
          {actions}
        </div>
      ) : null}
    </Row>
  );
}
