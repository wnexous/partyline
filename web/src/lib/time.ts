/*
 * Time formatting for live screens. Relative times are short ("5 min ago") with the absolute
 * date in a tooltip; both follow the UI locale.
 */

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

export function formatRelative(iso: string, locale: string, now = Date.now()): string {
  const time = Date.parse(iso);
  if (Number.isNaN(time)) return "";
  const diff = time - now;
  const abs = Math.abs(diff);
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "short" });
  if (abs < 45_000) return rtf.format(0, "second");
  if (abs < HOUR) return rtf.format(Math.round(diff / MINUTE), "minute");
  if (abs < DAY) return rtf.format(Math.round(diff / HOUR), "hour");
  if (abs < 7 * DAY) return rtf.format(Math.round(diff / DAY), "day");
  return new Intl.DateTimeFormat(locale, { day: "numeric", month: "short" }).format(time);
}

export function formatAbsolute(iso: string, locale: string): string {
  const time = Date.parse(iso);
  if (Number.isNaN(time)) return "";
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(time);
}

/** Clock time (HH:MM) for message rows. */
export function formatClock(iso: string, locale: string): string {
  const time = Date.parse(iso);
  if (Number.isNaN(time)) return "";
  return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }).format(time);
}

const startOfDay = (time: number) => new Date(time).setHours(0, 0, 0, 0);

/**
 * The time on a conversation row (LAY-01): the clock for today, "yesterday" for yesterday, the
 * weekday for the rest of the last week, then the date, with the year when it is not this one.
 */
export function formatListTime(iso: string, locale: string, now = Date.now()): string {
  const time = Date.parse(iso);
  if (Number.isNaN(time)) return "";
  const days = Math.round((startOfDay(now) - startOfDay(time)) / DAY);
  if (days <= 0) return formatClock(iso, locale);
  if (days === 1) {
    const yesterday = new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(-1, "day");
    return yesterday.charAt(0).toLocaleUpperCase(locale) + yesterday.slice(1);
  }
  if (days < 7) return new Intl.DateTimeFormat(locale, { weekday: "short" }).format(time);
  const thisYear = new Date(time).getFullYear() === new Date(now).getFullYear();
  return new Intl.DateTimeFormat(
    locale,
    thisYear
      ? { day: "numeric", month: "short" }
      : { day: "numeric", month: "short", year: "numeric" },
  ).format(time);
}
