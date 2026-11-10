import { cn } from "@/shared/lib/utils";

/** The partyline mark: three wires of a shared line, paper on ink (inverted in dark mode). */
export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={cn("size-7 shrink-0", className)}>
      <rect width="32" height="32" rx="7" className="fill-primary" />
      <path
        d="M8 11h16M8 16h10M8 21h13"
        strokeWidth="3"
        strokeLinecap="round"
        className="stroke-primary-foreground"
      />
    </svg>
  );
}

/**
 * The mark and the name. `textClassName` styles the name alone: a narrow bar hides it with a
 * container query and keeps the mark, rather than pushing its buttons off a phone with large text.
 */
export function Wordmark({
  className,
  textClassName,
}: {
  className?: string;
  textClassName?: string;
}) {
  return (
    <span className={cn("flex min-w-0 items-center gap-2.5", className)}>
      <LogoMark />
      <span
        translate="no"
        className={cn(
          "text-[1.05rem] font-bold tracking-[-0.02em] [font-stretch:112%]",
          textClassName,
        )}
      >
        partyline
      </span>
    </span>
  );
}
