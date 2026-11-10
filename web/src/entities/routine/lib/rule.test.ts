import { describe, expect, it } from "vitest";
import { type Locale, type Translate, translate } from "@/shared/i18n";
import {
  describeRoutine,
  describeRule,
  ruleOf,
  ruleOfCron,
  ruleRequest,
  runOutcome,
  runStatusText,
  sameRule,
} from "./rule";

/*
 * The rule of a routine in words (HB-13): the presets a phone picks are read back out of the two shapes
 * the server keeps (`every_minutes`, a 5-field cron), and any other cron stays as it is written.
 */

const tr =
  (locale: Locale): Translate =>
  (key, vars) =>
    translate(locale, key, vars);
const pt = tr("pt-BR");
const en = tr("en");

describe("ruleOf", () => {
  it("reads a whole number of hours as hours and anything else as minutes", () => {
    expect(ruleOf({ every_minutes: 120 })).toEqual({ kind: "hours", every: 2 });
    expect(ruleOf({ every_minutes: 45 })).toEqual({ kind: "minutes", every: 45 });
  });

  it("reads the daily, weekday and weekly presets out of a cron", () => {
    expect(ruleOfCron("0 9 * * *")).toEqual({ kind: "daily", hour: 9, minute: 0 });
    expect(ruleOfCron(" 30  18 * * 1-5 ")).toEqual({ kind: "weekdays", hour: 18, minute: 30 });
    expect(ruleOfCron("15 7 * * 3")).toEqual({ kind: "weekly", day: 3, hour: 7, minute: 15 });
    expect(ruleOfCron("0 8 * * 7")).toEqual({ kind: "weekly", day: 0, hour: 8, minute: 0 });
  });

  it("keeps any other cron as it is written", () => {
    for (const cron of ["*/30 * * * *", "0 9 1 * *", "0 9 * * 1,3", "0 25 * * *", "@daily"]) {
      expect(ruleOfCron(cron)).toEqual({ kind: "cron", cron });
    }
  });
});

describe("ruleRequest", () => {
  it("sends exactly one of every_minutes and cron", () => {
    expect(ruleRequest({ kind: "hours", every: 3 })).toEqual({ every_minutes: 180 });
    expect(ruleRequest({ kind: "minutes", every: 15 })).toEqual({ every_minutes: 15 });
    expect(ruleRequest({ kind: "daily", hour: 9, minute: 5 })).toEqual({ cron: "5 9 * * *" });
    expect(ruleRequest({ kind: "weekdays", hour: 9, minute: 0 })).toEqual({ cron: "0 9 * * 1-5" });
    expect(ruleRequest({ kind: "weekly", day: 0, hour: 9, minute: 0 })).toEqual({
      cron: "0 9 * * 0",
    });
  });

  it("tells the same rule written two ways apart from a new one", () => {
    expect(sameRule(ruleOfCron("0 9 * * 7"), { kind: "weekly", day: 0, hour: 9, minute: 0 })).toBe(
      true,
    );
    expect(sameRule({ kind: "hours", every: 1 }, { kind: "minutes", every: 60 })).toBe(true);
    expect(sameRule({ kind: "daily", hour: 9, minute: 0 }, ruleOfCron("0 10 * * *"))).toBe(false);
  });
});

describe("describeRule", () => {
  it("says the rule in Portuguese", () => {
    expect(describeRule(pt, "pt-BR", ruleOfCron("0 9 * * *"))).toBe("Todo dia às 09:00");
    expect(describeRule(pt, "pt-BR", ruleOfCron("0 9 * * 1-5"))).toBe(
      "De segunda a sexta às 09:00",
    );
    expect(describeRule(pt, "pt-BR", ruleOfCron("30 14 * * 1"))).toBe(
      "Toda semana, segunda-feira às 14:30",
    );
    expect(describeRule(pt, "pt-BR", ruleOf({ every_minutes: 60 }))).toBe("A cada hora");
    expect(describeRule(pt, "pt-BR", ruleOf({ every_minutes: 180 }))).toBe("A cada 3 horas");
    expect(describeRule(pt, "pt-BR", ruleOf({ every_minutes: 20 }))).toBe("A cada 20 minutos");
    expect(describeRule(pt, "pt-BR", ruleOfCron("*/30 9-17 * * *"))).toBe(
      "Regra personalizada: */30 9-17 * * *",
    );
  });

  it("says the rule in English", () => {
    expect(describeRule(en, "en", ruleOfCron("0 9 * * *")).replace(/\s/g, " ")).toBe(
      "Every day at 09:00 AM",
    );
    expect(describeRule(en, "en", ruleOf({ every_minutes: 120 }))).toBe("Every 2 hours");
  });

  it("names the routine's timezone only when it is not the viewer's", () => {
    const routine = { cron: "0 9 * * *", tz: "America/Sao_Paulo" };
    expect(describeRoutine(pt, "pt-BR", routine, "America/Sao_Paulo")).toBe("Todo dia às 09:00");
    expect(describeRoutine(pt, "pt-BR", routine, "Europe/Lisbon")).toBe(
      "Todo dia às 09:00 (horário de America/Sao_Paulo)",
    );
  });
});

describe("runOutcome", () => {
  it("says at a glance whether a run reached the AI, may still, failed to or was set aside", () => {
    expect(runOutcome("delivered")).toBe("delivered");
    expect(runOutcome("pending")).toBe("waiting");
    for (const status of ["missed", "expired", "client_outdated"]) {
      expect(runOutcome(status)).toBe("failed");
    }
    for (const status of ["superseded", "cancelled", "something_new"]) {
      expect(runOutcome(status)).toBe("skipped");
    }
  });

  it("keeps the words of a status, and an unknown one as the server names it", () => {
    expect(runStatusText(pt, "missed")).toBe("perdida: a IA estava offline");
    expect(runStatusText(en, "something_new")).toBe("something_new");
  });
});
