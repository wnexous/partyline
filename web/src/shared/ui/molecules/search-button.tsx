import { SearchIcon } from "lucide-react";
import { Button } from "@/shared/ui/primitives/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/shared/ui/primitives/tooltip";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";

/** True when the shortcut should read ⌘K (Apple platforms) rather than Ctrl K. */
export function isApplePlatform(): boolean {
  if (typeof navigator === "undefined") return false;
  return /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent);
}

/** Opens the palette; the tooltip shows the keyboard shortcut next to its label. */
export function SearchButton({
  className,
  onOpen,
}: {
  className?: string;
  /** Opens the palette: the caller wires the live store. */
  onOpen: () => void;
}) {
  const t = useT();
  const apple = isApplePlatform();
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("palette.open")}
          aria-keyshortcuts={apple ? "Meta+K" : "Control+K"}
          onClick={onOpen}
          className={cn("size-11 shrink-0 md:size-9", className)}
        >
          <SearchIcon aria-hidden="true" />
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {t("palette.open")}{" "}
        <kbd className="ml-1 rounded-[3px] border border-background/30 px-1 font-mono text-[0.68rem]">
          {apple ? "⌘K" : "Ctrl\u00a0K"}
        </kbd>
      </TooltipContent>
    </Tooltip>
  );
}
