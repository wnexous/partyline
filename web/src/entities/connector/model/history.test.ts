import { describe, expect, it } from "vitest";
import { HistoryForever, HistoryOff } from "@/shared/api/types.gen";
import { historyChoices, historyTurnsOff } from "./history";

describe("chat history retention (CH-01, CH-03)", () => {
  it("offers off, 7, 30, 90 days and forever", () => {
    expect(historyChoices(HistoryOff)).toEqual([0, 7, 30, 90, -1]);
    expect(historyChoices(HistoryForever)).toEqual([0, 7, 30, 90, -1]);
  });

  it("keeps a day count set through the API in its place", () => {
    expect(historyChoices(1)).toEqual([0, 1, 7, 30, 90, -1]);
    expect(historyChoices(60)).toEqual([0, 7, 30, 60, 90, -1]);
    expect(historyChoices(365)).toEqual([0, 7, 30, 90, 365, -1]);
  });

  it("asks before only when the history is turned off", () => {
    expect(historyTurnsOff(30, 0)).toBe(true);
    expect(historyTurnsOff(-1, 0)).toBe(true);
    expect(historyTurnsOff(0, 0)).toBe(false);
    expect(historyTurnsOff(90, 7)).toBe(false);
    expect(historyTurnsOff(0, 30)).toBe(false);
  });
});
