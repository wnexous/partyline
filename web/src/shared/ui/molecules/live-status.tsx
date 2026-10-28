import { RadioTowerIcon } from "lucide-react";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";

/** The socket states the line indicator knows. */
export type LiveStatus = "connecting" | "open" | "closed";

/** The line indicator: a filled, pulsing green dot while the live socket is open, a pulsing ring while it connects. */
export function LiveIndicator({
  status,
  className,
}: {
  /** The live socket's state: the caller reads it from the live store. */
  status: LiveStatus;
  className?: string;
}) {
  const t = useT();
  const label =
    status === "open"
      ? t("live.open")
      : status === "connecting"
        ? t("live.connecting")
        : t("live.closed");
  return (
    <span className={cn("inline-flex items-center gap-2 text-xs font-medium", className)}>
      <span
        aria-hidden="true"
        className={cn(
          "size-2 rounded-full",
          status === "open" && "animate-line-pulse bg-online",
          status === "connecting" && "animate-pulse border border-online bg-transparent",
          status === "closed" && "bg-muted-foreground/50",
        )}
      />
      <span>{label}</span>
    </span>
  );
}

/**
 * Shown above the page while the live socket has been down for more than 2 s (UI-06). The live
 * region stays mounted and only its content changes: screen readers skip regions that are
 * inserted together with their text.
 */
export function LiveBanner({ disconnected }: { /** True once the socket has been down past the grace period. */ disconnected: boolean }) {
  const t = useT();
  return (
    <div role="status" aria-live="polite">
      {disconnected ? (
        <div className="flex items-start gap-3 border-b bg-foreground px-4 py-2.5 text-sm text-background md:px-8">
          <RadioTowerIcon className="mt-0.5 size-4 shrink-0 animate-pulse" aria-hidden="true" />
          <p className="min-w-0">
            <span className="font-semibold">{t("live.reconnectingTitle")}</span>{" "}
            <span className="text-background/80">{t("live.reconnectingBody")}</span>
          </p>
        </div>
      ) : null}
    </div>
  );
}
