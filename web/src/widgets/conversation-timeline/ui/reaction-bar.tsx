import { SmilePlusIcon } from "lucide-react";
import { Toolbar } from "radix-ui";
import { useState } from "react";
import { QUICK_REACTIONS, ReactionPicker } from "@/features/react-to-message";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import {
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/shared/ui/primitives/dropdown-menu";
import { PopoverTrigger } from "@/shared/ui/primitives/popover";

/*
 * The six emoji at hand (RCT-02), in the three places a message offers them: a bar in the action sheet of a
 * touch device, a row of items in the menu of a mouse, and a toolbar on the message's row that a mouse reveals
 * by hovering and the keyboard by focus. Each is the same six buttons and a seventh for the rest; the emoji the
 * viewer used already are pressed, and choosing one of those takes the reaction back.
 */

/** The seventh button: the rest of the emoji. */
function MoreIcon() {
  return <SmilePlusIcon aria-hidden="true" className="size-5 text-muted-foreground" />;
}

const EMOJI_BUTTON =
  "grid cursor-pointer place-items-center rounded-md leading-none outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/70 active:bg-accent";

// A finger's button is 44 px high and shares the row with the others, up to 48 px wide.
const SHEET_BUTTON = "h-11 min-w-0 max-w-12 flex-1 basis-0";

/** The bar of the sheet: large buttons for a finger. */
export function ReactionBar({
  reacted,
  onReact,
  onMore,
  expanded,
}: {
  reacted: readonly string[];
  onReact: (emoji: string) => void;
  onMore: () => void;
  /** The rest of the emoji is open under the bar. */
  expanded: boolean;
}) {
  const t = useT();
  return (
    // biome-ignore lint/a11y/useSemanticElements: a fieldset would add a legend and a border to a row of buttons
    <div
      role="group"
      aria-label={t("reactions.quick")}
      className="flex items-center justify-between px-2 pb-1"
    >
      {QUICK_REACTIONS.map((emoji) => {
        const mine = reacted.includes(emoji);
        return (
          <button
            key={emoji}
            type="button"
            aria-label={t("reactions.react", { emoji })}
            aria-pressed={mine}
            onClick={() => onReact(emoji)}
            className={cn(EMOJI_BUTTON, SHEET_BUTTON, "text-2xl", mine && "bg-tape hover:bg-tape")}
          >
            <span aria-hidden="true">{emoji}</span>
          </button>
        );
      })}
      <button
        type="button"
        aria-label={t("reactions.more")}
        aria-expanded={expanded}
        onClick={onMore}
        className={cn(EMOJI_BUTTON, SHEET_BUTTON, expanded && "bg-accent")}
      >
        <MoreIcon />
      </button>
    </div>
  );
}

/** The row of the menu of a mouse: items of the menu, so its arrow keys walk them. */
export function ReactionMenuRow({
  reacted,
  onReact,
  onMore,
}: {
  reacted: readonly string[];
  onReact: (emoji: string) => void;
  onMore: () => void;
}) {
  const t = useT();
  return (
    <>
      <DropdownMenuGroup
        aria-label={t("reactions.quick")}
        className="flex items-center gap-0.5 p-0.5"
      >
        {QUICK_REACTIONS.map((emoji) => {
          const mine = reacted.includes(emoji);
          return (
            <DropdownMenuItem
              key={emoji}
              // What the item is: a reaction that is on or off.
              role="menuitemcheckbox"
              aria-checked={mine}
              aria-label={t("reactions.react", { emoji })}
              onSelect={() => onReact(emoji)}
              className={cn(
                "size-8 justify-center p-0 text-lg leading-none",
                mine && "bg-tape focus:bg-accent",
              )}
            >
              <span aria-hidden="true">{emoji}</span>
            </DropdownMenuItem>
          );
        })}
        <DropdownMenuItem
          aria-label={t("reactions.more")}
          onSelect={onMore}
          className="size-8 justify-center p-0"
        >
          <MoreIcon />
        </DropdownMenuItem>
      </DropdownMenuGroup>
      <DropdownMenuSeparator />
    </>
  );
}

/**
 * The toolbar of a row: one stop of the Tab key, moved with the arrows. Its last button opens the picker.
 * `className` says where it sits and when it shows.
 */
export function ReactionToolbar({
  reacted,
  onReact,
  className,
}: {
  reacted: readonly string[];
  onReact: (emoji: string) => void;
  className?: string;
}) {
  const t = useT();
  const [picking, setPicking] = useState(false);
  return (
    <Toolbar.Root
      aria-label={t("reactions.quick")}
      className={cn(
        "flex items-center gap-0.5 rounded-md border bg-card p-0.5 shadow-xs",
        className,
      )}
    >
      {QUICK_REACTIONS.map((emoji) => {
        const mine = reacted.includes(emoji);
        return (
          <Toolbar.Button
            key={emoji}
            aria-label={t("reactions.react", { emoji })}
            aria-pressed={mine}
            onClick={() => onReact(emoji)}
            className={cn(
              EMOJI_BUTTON,
              "size-7 rounded-sm text-base",
              mine && "bg-tape hover:bg-tape",
            )}
          >
            <span aria-hidden="true">{emoji}</span>
          </Toolbar.Button>
        );
      })}
      <ReactionPicker
        open={picking}
        onOpenChange={setPicking}
        reacted={reacted}
        onPick={onReact}
        anchor={
          <PopoverTrigger asChild>
            <Toolbar.Button
              aria-label={t("reactions.more")}
              className={cn(EMOJI_BUTTON, "size-7 rounded-sm")}
            >
              <SmilePlusIcon aria-hidden="true" className="size-4 text-muted-foreground" />
            </Toolbar.Button>
          </PopoverTrigger>
        }
      />
    </Toolbar.Root>
  );
}
