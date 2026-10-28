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

/** The absolute date and time in the long style ("21 de outubro de 2026, 15:00"). */
export function formatFull(iso: string, locale: string): string {
  const time = Date.parse(iso);
  if (Number.isNaN(time)) return "";
  return new Intl.DateTimeFormat(locale, { dateStyle: "long", timeStyle: "short" }).format(time);
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

/** The local day an instant falls on: two instants share it when they are on the same day. */
export const dayKey = (iso: string): string => new Date(iso).toDateString();

/**
 * The label of a day separator in the message timeline: "today", "yesterday", then the weekday and
 * the date. The two words come from the caller's dictionary; `now` is the moment it is read.
 */
export function formatDayLabel(
  iso: string,
  locale: string,
  words: { today: string; yesterday: string },
  now = Date.now(),
): string {
  const today = new Date(now);
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  const key = dayKey(iso);
  if (key === today.toDateString()) return words.today;
  if (key === yesterday.toDateString()) return words.yesterday;
  return new Intl.DateTimeFormat(locale, {
    weekday: "long",
    day: "numeric",
    month: "long",
  }).format(new Date(iso));
}
