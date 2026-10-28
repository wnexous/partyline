import { describe, expect, it } from "vitest";
import { dayKey, formatListTime as format, formatDayLabel, formatFull } from "./time";

// Newer ICU versions put a narrow no-break space before AM and PM.
const formatListTime = (...args: Parameters<typeof format>) => format(...args).replace(/\s/g, " ");

/*
 * The time on a conversation row (LAY-01). The spec only says "time", so these pin the rule the
 * list uses: the clock for today, "yesterday" for yesterday, the weekday inside the last week,
 * the date after that (with the year when it is not this one).
 */

// Local-time constructors keep the day boundaries independent of the machine's time zone.
const at = (y: number, month: number, d: number, h = 12, min = 30) =>
  new Date(y, month - 1, d, h, min).toISOString();
const NOW = new Date(2026, 9, 21, 15, 0).getTime(); // Wednesday 21 Oct 2026, 15:00

describe("formatListTime", () => {
  it("shows the clock for a message from today", () => {
    expect(formatListTime(at(2026, 10, 21, 8, 5), "pt-BR", NOW)).toBe("08:05");
    expect(formatListTime(at(2026, 10, 21, 8, 5), "en", NOW)).toBe("08:05 AM");
  });

  it("shows the clock just after midnight and just before it, both today", () => {
    expect(formatListTime(at(2026, 10, 21, 0, 1), "pt-BR", NOW)).toBe("00:01");
    expect(
      formatListTime(at(2026, 10, 21, 23, 59), "pt-BR", new Date(2026, 9, 21, 23, 59).getTime()),
    ).toBe("23:59");
  });

  it("says yesterday for the day before, even a minute before midnight", () => {
    expect(formatListTime(at(2026, 10, 20, 23, 59), "en", NOW)).toBe("Yesterday");
    expect(formatListTime(at(2026, 10, 20, 0, 0), "pt-BR", NOW)).toBe("Ontem");
  });

  it("shows the weekday for the days before yesterday inside the last week", () => {
    expect(formatListTime(at(2026, 10, 19), "en", NOW)).toBe("Mon");
    expect(formatListTime(at(2026, 10, 15), "en", NOW)).toBe("Thu");
    expect(formatListTime(at(2026, 10, 19), "pt-BR", NOW)).toBe("seg.");
  });

  it("shows the date from a week ago on, with the year only when it is another year", () => {
    expect(formatListTime(at(2026, 10, 14), "en", NOW)).toBe("Oct 14");
    expect(formatListTime(at(2026, 10, 14), "pt-BR", NOW)).toBe("14 de out.");
    expect(formatListTime(at(2025, 12, 31), "en", NOW)).toBe("Dec 31, 2025");
  });

  it("is empty for a time it cannot read", () => {
    expect(formatListTime("not a time", "en", NOW)).toBe("");
  });
});

describe("formatDayLabel", () => {
  const words = { today: "Today", yesterday: "Yesterday" };
  it("says today and yesterday for the two days before now, else the weekday and date", () => {
    expect(formatDayLabel(at(2026, 10, 21, 8), "en", words, NOW)).toBe("Today");
    expect(formatDayLabel(at(2026, 10, 20, 23), "en", words, NOW)).toBe("Yesterday");
    expect(formatDayLabel(at(2026, 10, 19, 12), "en", words, NOW)).toBe("Monday, October 19");
  });

  it("groups by local day", () => {
    expect(dayKey(at(2026, 10, 21, 0, 1))).toBe(dayKey(at(2026, 10, 21, 23, 59)));
    expect(dayKey(at(2026, 10, 21))).not.toBe(dayKey(at(2026, 10, 20)));
  });
});

describe("formatFull", () => {
  it("is the long date with the short time", () => {
    expect(formatFull(at(2026, 10, 21, 15, 0), "en").replace(/\s/g, " ")).toBe(
      "October 21, 2026 at 3:00 PM",
    );
  });
});
