import { type LucideIcon, XIcon } from "lucide-react";
import { Fragment, type ReactNode, useRef } from "react";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/shared/ui/molecules/sheet";
import { Button } from "@/shared/ui/primitives/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/shared/ui/primitives/dropdown-menu";

/*
 * What a row's actions look like once they are open (ACT-01, ACT-02, ACT-06). A touch device gets
 * a sheet from the bottom, named after what it acts on, with rows a finger can hit; a mouse gets a
 * menu at the pointer that moves with the arrow keys. Both are the same list of actions, both trap
 * focus and close with Escape, and the destructive ones carry the destructive colour, an icon and
 * their word. Who opens it, and what a choice does, is the caller's.
 */

export interface MenuAction {
  id: string;
  label: string;
  icon: LucideIcon;
  onSelect: () => void;
  /** Wrong or irreversible: the destructive colour, set apart from the rest. */
  destructive?: boolean;
  disabled?: boolean;
}

export interface ActionMenuProps {
  open: boolean;
  onClose: () => void;
  /** A touch-first device gets the sheet, a mouse the menu. */
  touch: boolean;
  /** What the actions act on: the sheet's title and, unless `label` says more, the menu's name. */
  title: string;
  /** The menu's name when the title alone is too short to name it ("Actions for the message from Ana"). */
  label?: string;
  /** One line that says which one, under the sheet's title. */
  description?: string;
  /** Where a menu opens: at the pointer, or under the button that was pressed (`end` hangs it from the right). */
  at: { x: number; y: number; align?: "start" | "end" };
  actions: MenuAction[];
  /** Laid above the actions: the quick reactions. */
  header?: ReactNode;
  /**
   * Runs when the surface is gone and would hand focus to whatever opened it. Call
   * `event.preventDefault()` to place it yourself (a dialog that follows takes it instead).
   */
  onCloseFocus?: (event: Event) => void;
}

export function ActionMenu(props: ActionMenuProps) {
  return props.touch ? <ActionSheet {...props} /> : <ActionPopup {...props} />;
}

/** Destructive actions come last, set apart by a rule. */
const firstDestructive = (actions: MenuAction[]) => {
  const at = actions.findIndex((a) => a.destructive);
  return at > 0 ? at : -1;
};

function ActionSheet({
  open,
  onClose,
  title,
  description,
  actions,
  header,
  onCloseFocus,
}: ActionMenuProps) {
  const t = useT();
  const rule = firstDestructive(actions);
  // Whether a press has begun inside the sheet. The finger that opened it is still down, and some
  // browsers send its release as a click on whatever is under it by then: a button of the sheet.
  // Only a click that follows a press here is a tap. A click with no `detail` is the keyboard's
  // or a screen reader's, which press nothing, and is a choice too.
  const pressed = useRef(false);
  return (
    <Sheet open={open} onOpenChange={(next) => (next ? null : onClose())}>
      <SheetContent
        side="bottom"
        showCloseButton={false}
        // Without a line under the title the sheet has no description to point at.
        {...(description ? {} : { "aria-describedby": undefined })}
        onPointerDownCapture={() => {
          pressed.current = true;
        }}
        onClickCapture={(event) => {
          if (event.detail === 0) return;
          if (pressed.current) {
            pressed.current = false;
            return;
          }
          event.preventDefault();
          event.stopPropagation();
        }}
        // Focus is in the sheet, on no action: a row that opens with a ring on it looks chosen.
        // The first Tab reaches the first action.
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          pressed.current = false;
          (event.target as HTMLElement | null)?.focus({ preventScroll: true });
        }}
        onCloseAutoFocus={onCloseFocus}
        className="max-h-[85dvh] gap-0 overflow-y-auto rounded-t-xl outline-none bg-popover pb-[max(0.5rem,env(safe-area-inset-bottom))] text-popover-foreground data-[state=closed]:duration-150 data-[state=open]:duration-200"
      >
        <SheetHeader className="gap-0.5 py-3 pr-14 pl-4">
          <SheetTitle className="truncate">{title}</SheetTitle>
          {description ? (
            <SheetDescription className="truncate">{description}</SheetDescription>
          ) : null}
        </SheetHeader>
        {header}
        <ul className="px-2 pb-1">
          {actions.map((a, index) => (
            <li key={a.id} className={cn(index === rule && "mt-1 border-t pt-1")}>
              <button
                type="button"
                disabled={a.disabled}
                onClick={() => {
                  a.onSelect();
                  onClose();
                }}
                className={cn(
                  "flex min-h-12 w-full items-center gap-3 rounded-md px-3 text-left text-[0.9375rem] outline-none transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring/70 focus-visible:ring-inset active:bg-accent disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-5 [&_svg]:shrink-0",
                  a.destructive
                    ? "font-medium text-destructive [&_svg]:text-destructive"
                    : "[&_svg]:text-muted-foreground",
                )}
              >
                <a.icon aria-hidden="true" />
                {a.label}
              </button>
            </li>
          ))}
        </ul>
        {/* Last in the page's order, drawn at the top right: Tab reaches the actions first. */}
        <SheetClose asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="absolute top-1 right-1 size-11 text-muted-foreground"
            aria-label={t("common.close")}
          >
            <XIcon aria-hidden="true" />
          </Button>
        </SheetClose>
      </SheetContent>
    </Sheet>
  );
}

function ActionPopup({
  open,
  onClose,
  title,
  label,
  at,
  actions,
  header,
  onCloseFocus,
}: ActionMenuProps) {
  const rule = firstDestructive(actions);
  return (
    <DropdownMenu open={open} onOpenChange={(next) => (next ? null : onClose())}>
      {/* The menu hangs from a point, not from an element: an empty anchor stands there. The
          menu is named through it (Radix points `aria-labelledby` at its trigger). */}
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-hidden="true"
          aria-label={label ?? title}
          tabIndex={-1}
          className="pointer-events-none fixed size-0 border-0 p-0 opacity-0"
          style={{ left: at.x, top: at.y }}
        />
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align={at.align ?? "start"}
        sideOffset={2}
        // A short menu wraps from its last item to its first, as the ones of the system do.
        loop
        // The anchor is no place for focus to go back to: the caller says where, or it stays put.
        onCloseAutoFocus={onCloseFocus ?? ((event) => event.preventDefault())}
        className="min-w-52"
      >
        {header}
        {actions.map((a, index) => (
          <Fragment key={a.id}>
            {index === rule ? <DropdownMenuSeparator /> : null}
            <DropdownMenuItem
              variant={a.destructive ? "destructive" : "default"}
              disabled={a.disabled}
              onSelect={() => a.onSelect()}
            >
              <a.icon aria-hidden="true" />
              {a.label}
            </DropdownMenuItem>
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
