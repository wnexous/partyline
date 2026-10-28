import { IntelligenceAvatar } from "@/entities/intelligence";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { candidateKey, type MentionCandidate, mentionOptionId } from "../model/mention";

export interface MentionSuggestionsProps {
  id: string;
  candidates: MentionCandidate[];
  /** Index of the highlighted option. */
  activeIndex: number;
  onPick: (candidate: MentionCandidate) => void;
}

/**
 * The listbox above the composer. Options are chosen with the keyboard from the textarea
 * (`aria-activedescendant`) or tapped; they are not focusable themselves.
 */
export function MentionSuggestions({
  id,
  candidates,
  activeIndex,
  onPick,
}: MentionSuggestionsProps) {
  const { t } = useI18n();
  return (
    <div
      id={id}
      role="listbox"
      aria-label={t("composer.mentions")}
      className="absolute inset-x-0 bottom-full z-20 mb-1 max-h-64 overflow-y-auto rounded-lg border bg-popover p-1 shadow-md"
    >
      {candidates.map((candidate, index) => {
        const active = index === activeIndex;
        return (
          <div
            key={candidateKey(candidate.intelligence.id, candidate.label)}
            id={mentionOptionId(id, candidate)}
            role="option"
            aria-selected={active}
            tabIndex={-1}
            className={cn(
              "flex min-h-11 cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5",
              active ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
            )}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => onPick(candidate)}
            onKeyDown={(e) => {
              // Keyboard picks happen in the textarea; this also keeps the option usable when a
              // screen reader moves focus to it.
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onPick(candidate);
              }
            }}
          >
            <IntelligenceAvatar intelligence={candidate.intelligence} size="sm" decorative />
            <span className="min-w-0 flex-1 truncate text-sm font-medium">
              {candidate.intelligence.display_name.trim() || candidate.intelligence.handle}
            </span>
            {/* The label is part of the address the option inserts, so a phone sees which
                session it is picking (SESS-33). The principal stays the plain handle. */}
            <span className="shrink-0 font-mono text-xs text-muted-foreground">
              <span>@{candidate.intelligence.handle}</span>
              {candidate.label ? <span>:{candidate.label}</span> : null}
            </span>
          </div>
        );
      })}
    </div>
  );
}
