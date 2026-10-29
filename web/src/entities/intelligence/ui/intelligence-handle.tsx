import { cn } from "@/shared/lib/utils";

/*
 * An intelligence's @handle (PROF-09): the one way the app draws it. A handle is what tells two
 * identities with the same display name apart, so it is monospace and muted, beside the name or
 * inline in a sentence, and it looks the same in a row, a detail view and a dialog.
 */

const HANDLE = "min-w-0 truncate font-mono text-xs text-muted-foreground";

export interface IntelligenceHandleProps {
  handle: string;
  className?: string;
}

/** The text of a handle, for places that cannot hold markup (an `<option>`, a search haystack). */
export function handleText(handle: string): string {
  return `@${handle}`;
}

export function IntelligenceHandle({ handle, className }: IntelligenceHandleProps) {
  return (
    <span data-testid="intelligence-handle" className={cn(HANDLE, className)}>
      {handleText(handle)}
    </span>
  );
}
