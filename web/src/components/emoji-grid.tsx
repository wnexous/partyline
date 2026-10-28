import { type KeyboardEvent, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { cn } from "@/shared/lib/utils";

/*
 * The larger set of emoji behind "More" (RCT-02): a grid of buttons, each named "React with <emoji>" and pressed
 * when the viewer used it already. It is one stop of the Tab key (the first emoji the viewer used, else the first,
 * which is where a popover puts focus as it opens) and moves with the arrow keys (Home and End go to the ends of
 * a row, with Control to the ends of the grid), so a keyboard does not walk 48 buttons.
 */

// A finger's cell is 44 px high and as wide as its column allows, so eight of them fit a phone of 320 px.
const SIZES = {
  touch: "h-11 w-full max-w-11 text-2xl",
  pointer: "size-8 text-lg",
} as const;

export function EmojiGrid({
  emojis,
  columns,
  reacted,
  onPick,
  label,
  size,
}: {
  emojis: readonly string[];
  columns: number;
  /** The emoji the viewer used on the message. */
  reacted: readonly string[];
  onPick: (emoji: string) => void;
  /** What the grid is for, as a name. */
  label: string;
  size: keyof typeof SIZES;
}) {
  const t = useT();
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  // The one emoji Tab reaches: the first the viewer used, else the first.
  const [stop, setStop] = useState(() =>
    Math.max(
      0,
      emojis.findIndex((e) => reacted.includes(e)),
    ),
  );

  const go = (index: number) => {
    const next = Math.min(emojis.length - 1, Math.max(0, index));
    setStop(next);
    buttons.current[next]?.focus({ preventScroll: true });
  };
  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const row = Math.floor(index / columns) * columns;
    const target: Record<string, number> = {
      ArrowRight: index + 1,
      ArrowLeft: index - 1,
      ArrowDown: index + columns,
      ArrowUp: index - columns,
      Home: event.ctrlKey ? 0 : row,
      End: event.ctrlKey ? emojis.length - 1 : Math.min(row + columns - 1, emojis.length - 1),
    };
    const to = target[event.key];
    if (to === undefined) return;
    event.preventDefault();
    // An arrow that would leave the grid stays where it is, as one that wraps would surprise.
    if (to < 0 || to >= emojis.length) return;
    go(to);
  };

  return (
    // biome-ignore lint/a11y/useSemanticElements: a fieldset would add a legend and a border to a grid of buttons
    <div
      role="group"
      aria-label={label}
      className="grid justify-items-center gap-0.5"
      style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
    >
      {emojis.map((emoji, index) => {
        const mine = reacted.includes(emoji);
        return (
          <button
            key={emoji}
            ref={(node) => {
              buttons.current[index] = node;
            }}
            type="button"
            aria-label={t("reactions.react", { emoji })}
            aria-pressed={mine}
            tabIndex={index === stop ? 0 : -1}
            onClick={() => {
              setStop(index);
              onPick(emoji);
            }}
            onKeyDown={(event) => onKeyDown(event, index)}
            className={cn(
              "grid cursor-pointer place-items-center rounded-md leading-none outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/70 active:bg-accent",
              SIZES[size],
              mine && "bg-tape hover:bg-tape",
            )}
          >
            <span aria-hidden="true">{emoji}</span>
          </button>
        );
      })}
    </div>
  );
}
