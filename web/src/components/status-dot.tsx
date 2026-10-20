import { useI18n } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/** Agent presence: a tape-yellow pulse when online, a hollow ring when offline. */
export function StatusDot({
  online,
  withLabel = false,
  className,
}: {
  online: boolean;
  withLabel?: boolean;
  className?: string;
}) {
  const { t } = useI18n();
  const label = online ? t("presence.online") : t("presence.offline");
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <span
        aria-hidden="true"
        className={cn(
          "size-2 shrink-0 rounded-full",
          online
            ? "animate-line-pulse bg-tape-strong"
            : "border border-muted-foreground/60 bg-transparent",
        )}
      />
      {withLabel ? (
        <span className="text-xs text-muted-foreground">{label}</span>
      ) : (
        <span className="sr-only">{label}</span>
      )}
    </span>
  );
}
