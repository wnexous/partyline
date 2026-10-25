import type { ReactNode } from "react";
import { EmojiGrid } from "@/components/emoji-grid";
import { Popover, PopoverContent } from "@/components/ui/popover";
import { useT } from "@/lib/i18n";
import { MORE_REACTIONS, PICKER_COLUMNS } from "@/lib/reactions";

/*
 * The larger set of emoji for a mouse (RCT-02): a popover with the grid, hung from whatever opened it. The
 * grid's stop takes focus as it opens, an emoji picked closes it, and Escape closes it without a choice. Focus
 * goes back to `anchor` unless the caller places it (`onCloseFocus`).
 */

export function ReactionPicker({
  open,
  onOpenChange,
  anchor,
  reacted,
  onPick,
  onCloseFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** A `PopoverTrigger` (the button that opens it) or a `PopoverAnchor` (a point it hangs from). */
  anchor: ReactNode;
  /** The emoji the viewer used on the message. */
  reacted: readonly string[];
  onPick: (emoji: string) => void;
  onCloseFocus?: (event: Event) => void;
}) {
  const t = useT();
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      {anchor}
      <PopoverContent
        align="start"
        aria-label={t("reactions.pick")}
        onCloseAutoFocus={onCloseFocus}
        className="w-auto p-1.5"
      >
        <EmojiGrid
          emojis={MORE_REACTIONS}
          columns={PICKER_COLUMNS}
          reacted={reacted}
          onPick={(emoji) => {
            onPick(emoji);
            onOpenChange(false);
          }}
          label={t("reactions.more")}
          size="pointer"
        />
      </PopoverContent>
    </Popover>
  );
}
