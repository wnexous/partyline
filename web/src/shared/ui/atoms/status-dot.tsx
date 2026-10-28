import { cn } from "@/shared/lib/utils";

/**
 * The look of a status dot, one per state: an agent's presence (online or stopped) and a
 * connector's status (connected, connecting, error, stopped) share it.
 */
export const STATUS_DOT_CLASS = {
  online: "animate-line-pulse bg-online",
  connecting: "animate-pulse bg-muted-foreground",
  error: "bg-destructive",
  stopped: "border border-muted-foreground bg-transparent",
} as const;

export type StatusVariant = keyof typeof STATUS_DOT_CLASS;

/** Agent presence: a filled, pulsing green dot when online, a hollow grey ring when offline. */
export function StatusDot({
  online,
  variant,
  label,
  withLabel = false,
  className,
  labelClassName,
}: {
  online: boolean;
  /** The state to draw; without it the dot is online or stopped, as `online` says. */
  variant?: StatusVariant;
  /** The accessible label ("Online"/"Offline"): the caller translates it. */
  label: string;
  withLabel?: boolean;
  className?: string;
  /** Classes of the word beside the dot, where it sits among text bigger than a list row's. */
  labelClassName?: string;
}) {
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <span
        aria-hidden="true"
        className={cn(
          "size-2 shrink-0 rounded-full",
          STATUS_DOT_CLASS[variant ?? (online ? "online" : "stopped")],
        )}
      />
      {withLabel ? (
        <span className={cn("text-xs text-muted-foreground", labelClassName)}>{label}</span>
      ) : (
        <span className="sr-only">{label}</span>
      )}
    </span>
  );
}
