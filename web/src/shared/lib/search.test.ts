import { describe, expect, it } from "vitest";
import { foldText, matchesTerms } from "./search";

describe("foldText and matchesTerms", () => {
  it("folds case and accents", () => {
    expect(foldText("Ação À Vista")).toBe("acao a vista");
    expect(foldText("ÀÉÎÕÜ ç")).toBe("aeiou c");
  });

  it("matches a haystack against every word of the query", () => {
    expect(matchesTerms("Atlas @atlas devbox ~/atlas", "atlas dev")).toBe(true);
    expect(matchesTerms("Atlas @atlas devbox ~/atlas", "atlas notebook")).toBe(false);
    expect(matchesTerms("anything", "")).toBe(true);
  });
});
