import type { Reaction } from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * The reactions under a message (RCT-01, RCT-08): one chip per emoji with how many used it, in the order each
 * emoji was first used. The viewer's own is marked with the tape fill and `aria-pressed`, and a tap adds their
 * reaction or takes it back. Someone who may not react sees the same chips with nothing to press. The name of a
 * chip carries the emoji, the count and whether it includes the viewer: "👍 2 reactions, including yours".
 */

const CHIP =
  "inline-flex h-7 items-center gap-1 rounded-md border px-2 text-xs leading-none pointer-coarse:h-9 pointer-coarse:px-2.5";
const MINE = "border-tape-strong bg-tape text-tape-foreground";
const OTHERS = "border-border bg-secondary text-secondary-foreground";

export function ReactionChips({
  reactions,
  meId,
  onToggle,
}: {
  reactions: Reaction[];
  meId: string;
  /** Adds the viewer's reaction with the emoji, or takes it back. Left out where the viewer may not react. */
  onToggle?: (emoji: string) => void;
}) {
  const { t } = useI18n();
  if (reactions.length === 0) return null;
  return (
    <ul aria-label={t("reactions.list")} className="mt-1.5 flex flex-wrap gap-1.5">
      {reactions.map((r) => {
        const mine = r.by.includes(meId);
        const count = r.by.length;
        const name =
          count === 1
            ? t(mine ? "reactions.chipOneMine" : "reactions.chipOne", { emoji: r.emoji })
            : t(mine ? "reactions.chipManyMine" : "reactions.chipMany", { emoji: r.emoji, count });
        const face = (
          <>
            <span aria-hidden="true" className="text-sm">
              {r.emoji}
            </span>
            <span aria-hidden="true" className="tabular font-medium">
              {count}
            </span>
          </>
        );
        return (
          <li key={r.emoji}>
            {onToggle ? (
              <button
                type="button"
                aria-pressed={mine}
                aria-label={name}
                onClick={() => onToggle(r.emoji)}
                className={cn(
                  CHIP,
                  "cursor-pointer transition-colors outline-none hover:border-tape-strong/60 focus-visible:ring-[3px] focus-visible:ring-ring/70",
                  mine ? MINE : cn(OTHERS, "hover:bg-accent"),
                )}
              >
                {face}
              </button>
            ) : (
              <span className={cn(CHIP, mine ? MINE : OTHERS)}>
                {face}
                <span className="sr-only">{name}</span>
              </span>
            )}
          </li>
        );
      })}
    </ul>
  );
}
