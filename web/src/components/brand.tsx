import { cn } from "@/lib/utils";

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

export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn("flex items-center gap-2.5", className)}>
      <LogoMark />
      <span
        translate="no"
        className="text-[1.05rem] font-bold tracking-[-0.02em] [font-stretch:112%]"
      >
        partyline
      </span>
    </span>
  );
}
