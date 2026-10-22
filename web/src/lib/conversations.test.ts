import { describe, expect, it } from "vitest";
import { makeConversation } from "@/test/fixtures";
import { conversationMatches, foldText, matchesTerms } from "./conversations";

/*
 * Sidebar search (LAY-03): a conversation is found by its name and by the people and AIs in it.
 */

const deploys = makeConversation({
  id: "r1",
  title: "deploys",
  member_names: ["Ana Souza @ana", "Bob Lima @bob", "Atlas @atlas"],
});
const dm = makeConversation({
  id: "r2",
  kind: "dm",
  title: "João Pereira",
  member_names: ["João Pereira @joao"],
});

describe("conversationMatches (LAY-03)", () => {
  it("finds a conversation by its name, whatever the case", () => {
    expect(conversationMatches(deploys, "deploys")).toBe(true);
    expect(conversationMatches(deploys, "DEPLO")).toBe(true);
    expect(conversationMatches(deploys, "ploy")).toBe(true);
    expect(conversationMatches(deploys, "support")).toBe(false);
  });

  it("finds a group by the display name or the handle of a member", () => {
    expect(conversationMatches(deploys, "lima")).toBe(true);
    expect(conversationMatches(deploys, "bob")).toBe(true);
    expect(conversationMatches(deploys, "@atlas")).toBe(true);
    expect(conversationMatches(deploys, "zed")).toBe(false);
  });

  it("finds a direct message by its counterpart", () => {
    expect(conversationMatches(dm, "pereira")).toBe(true);
    expect(conversationMatches(dm, "@joao")).toBe(true);
  });

  it("ignores accents on either side", () => {
    expect(conversationMatches(dm, "joao")).toBe(true);
    expect(conversationMatches(dm, "JOÃO")).toBe(true);
    expect(conversationMatches(makeConversation({ title: "reunião" }), "reuniao")).toBe(true);
  });

  it("needs every word of the search, each found in the name or among the members", () => {
    expect(conversationMatches(deploys, "deploys bob")).toBe(true);
    expect(conversationMatches(deploys, "bob deploys")).toBe(true);
    expect(conversationMatches(deploys, "deploys zed")).toBe(false);
    expect(conversationMatches(deploys, "ana atlas")).toBe(true);
  });

  it("matches everything for an empty or blank search", () => {
    expect(conversationMatches(deploys, "")).toBe(true);
    expect(conversationMatches(deploys, "   ")).toBe(true);
  });

  it("finds nothing about a group's members it was not told of, such as a bridged chat", () => {
    expect(conversationMatches(makeConversation({ title: "ops", member_names: [] }), "bob")).toBe(
      false,
    );
  });
});

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
