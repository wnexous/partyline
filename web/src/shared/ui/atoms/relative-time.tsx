import type { ReactNode } from "react";
import {
  formatAbsolute,
  formatClock,
  formatFull,
  formatListTime,
  formatRelative,
} from "@/shared/lib/time";
import { cn } from "@/shared/lib/utils";

/*
 * A moment as the app shows it: "5 min ago" by default, with the absolute time in a tooltip, in a
 * `<time>` element so the instant is machine-readable too. `format="clock"` shows the clock, `format="list"` the
 * day label or the clock of a list row (`formatListTime`),
 * `format="date"` the long date and time (no tooltip: it is already absolute).
 *
 * `children` builds a sentence around the time: `<RelativeTime value={iso} locale={locale}>
 * {(time) => t("contacts.lastSeen", { time })}</RelativeTime>`. An atom takes no translation, so
 * the caller passes its locale.
 */
export function RelativeTime({
  value,
  locale,
  format = "relative",
  children,
  className,
}: {
  /** The instant, as an ISO string. */
  value: string;
  locale: string;
  format?: "relative" | "clock" | "list" | "date";
  children?: (time: string) => ReactNode;
  className?: string;
}) {
  const text =
    format === "clock"
      ? formatClock(value, locale)
      : format === "list"
        ? formatListTime(value, locale)
        : format === "date"
          ? formatFull(value, locale)
          : formatRelative(value, locale);
  const title = format === "date" ? undefined : formatAbsolute(value, locale);
  return (
    <time dateTime={value} title={title} className={cn(className)}>
      {children ? children(text) : text}
    </time>
  );
}
