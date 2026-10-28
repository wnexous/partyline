import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/**
 * One live session of an AI, named the way it is addressed: `:label`, or "principal" for the main
 * session (label ""). The one rendering of the vocabulary (SESS-32, SESS-44).
 */
export function SessionLabel({ label, className }: { label: string; className?: string }) {
  const t = useT();
  return (
    <span data-testid="session-label" className={cn("font-mono", className)}>
      {label === "" ? t("profile.sessionPrincipal") : `:${label}`}
    </span>
  );
}
