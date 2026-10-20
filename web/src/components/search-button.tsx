import { SearchIcon } from "lucide-react";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { cn } from "@/lib/utils";

/** True when the shortcut should read ⌘K (Apple platforms) rather than Ctrl K. */
export function isApplePlatform(): boolean {
  if (typeof navigator === "undefined") return false;
  return /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent);
}

/** Opens the palette; shows the keyboard shortcut next to its label. */
export function SearchButton({ className }: { className?: string }) {
  const t = useT();
  const setOpen = useLiveStore((s) => s.setPaletteOpen);
  const shortcut = isApplePlatform() ? "⌘K" : "Ctrl K";
  return (
    <button
      type="button"
      onClick={() => setOpen(true)}
      aria-keyshortcuts={isApplePlatform() ? "Meta+K" : "Control+K"}
      className={cn(
        "flex h-9 w-full items-center gap-2 rounded-md border border-sidebar-border bg-background/70 px-2.5 text-sm text-muted-foreground shadow-xs outline-none transition-colors hover:bg-background hover:text-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring",
        className,
      )}
    >
      <SearchIcon className="size-4 shrink-0" aria-hidden="true" />
      <span className="flex-1 text-left">{t("palette.open")}</span>
      <kbd className="rounded-[3px] border bg-muted px-1.5 py-px font-mono text-[0.68rem] text-muted-foreground">
        {shortcut}
      </kbd>
    </button>
  );
}
