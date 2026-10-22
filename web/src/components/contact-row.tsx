import type { ReactNode } from "react";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { cn } from "@/lib/utils";

/**
 * One contact on the contacts page: avatar, name with its badge and @handle, a line of what
 * tells it apart (`meta`) and its actions on the right. `prefix` is the chain of folders without
 * an AI that the tree collapsed above this one ("projects/acme/agents"), shown before the name.
 */
export function ContactRow({
  intelligence,
  testId,
  prefix,
  meta,
  actions,
  className,
}: {
  intelligence: IntelligenceRef;
  testId?: string;
  prefix?: string;
  meta?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div data-testid={testId} className={cn("flex items-start gap-3 py-2.5", className)}>
      <IntelligenceAvatar intelligence={intelligence} decorative className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <p className="flex min-w-0 flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
          {prefix ? (
            <span className="font-mono text-xs break-all text-muted-foreground">{prefix} ›</span>
          ) : null}
          <IntelligenceName intelligence={intelligence} showHandle />
        </p>
        {meta ? (
          <p className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
            {meta}
          </p>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-1">{actions}</div> : null}
    </div>
  );
}
