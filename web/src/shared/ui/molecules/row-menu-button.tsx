import { EllipsisIcon } from "lucide-react";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";

/*
 * The "⋯" that opens a row's actions with a mouse (ACT-02). It hangs on the row's corner and shows
 * while the row is hovered or has focus, so a row at rest carries no clutter, and it stays one
 * Tab away for the keyboard. The menu it opens hangs from the button's bottom right edge.
 */

export function RowMenuButton({
  label,
  expanded,
  onOpen,
  className,
}: {
  /** What it opens, as a name: "Actions for deploys". */
  label: string;
  expanded: boolean;
  onOpen: (at: { x: number; y: number; align: "end" }, button: HTMLButtonElement) => void;
  /** Where it sits and when it shows: the row's own hover and focus variants. */
  className?: string;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      aria-label={label}
      aria-haspopup="menu"
      aria-expanded={expanded}
      onClick={(e) => {
        const button = e.currentTarget;
        const box = button.getBoundingClientRect();
        onOpen({ x: box.right, y: box.bottom, align: "end" }, button);
      }}
      className={cn("text-muted-foreground focus-visible:opacity-100", className)}
    >
      <EllipsisIcon aria-hidden="true" />
    </Button>
  );
}
