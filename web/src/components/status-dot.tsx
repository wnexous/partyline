import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/** Agent presence: a filled, pulsing green dot when online, a hollow grey ring when offline. */
export function StatusDot({
  online,
  withLabel = false,
  className,
  labelClassName,
}: {
  online: boolean;
  withLabel?: boolean;
  className?: string;
  /** Classes of the word beside the dot, where it sits among text bigger than a list row's. */
  labelClassName?: string;
}) {
  const { t } = useI18n();
  const label = online ? t("presence.online") : t("presence.offline");
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <span
        aria-hidden="true"
        className={cn(
          "size-2 shrink-0 rounded-full",
          online ? "animate-line-pulse bg-online" : "border border-muted-foreground bg-transparent",
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
