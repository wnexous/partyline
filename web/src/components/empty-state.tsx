import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Empty states teach the interface: what this place is for and the one action that fills it.
 * The icon sits on a strip of telegram tape, the app's mark for "this is where traffic lands".
 */
export function EmptyState({
  icon: Icon,
  title,
  children,
  action,
  className,
  headingLevel: Heading = "h2",
}: {
  icon: LucideIcon;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
  /** h1 when it is the whole page, h3 inside a titled section, p where a heading would skip levels. */
  headingLevel?: "h1" | "h2" | "h3" | "p";
}) {
  return (
    <section
      className={cn(
        "mx-auto flex max-w-md flex-col items-center px-6 py-16 text-center md:py-24",
        className,
      )}
    >
      <div className="relative mb-6 flex h-12 w-28 items-center justify-center" aria-hidden="true">
        <span className="absolute inset-x-0 top-1/2 h-7 -translate-y-1/2 -rotate-2 rounded-[3px] bg-tape/80 shadow-sm" />
        <Icon className="relative size-6 text-tape-foreground" strokeWidth={1.75} />
      </div>
      <Heading className="text-lg font-semibold tracking-[-0.01em] text-balance">{title}</Heading>
      {children ? (
        <div className="mt-2 text-sm text-pretty text-muted-foreground">{children}</div>
      ) : null}
      {action ? <div className="mt-6 flex flex-wrap justify-center gap-2">{action}</div> : null}
    </section>
  );
}
