import type { ReactNode } from "react";
import { cn } from "@/shared/lib/utils";

/**
 * A panel's place while it loads, hidden from assistive tech: the caller lays its skeleton bars out
 * as children, in the panel's own spacing (`className`, `space-y-3` by default).
 */
export function PanelSkeleton({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div aria-hidden="true" className={cn("space-y-3", className)}>
      {children}
    </div>
  );
}
