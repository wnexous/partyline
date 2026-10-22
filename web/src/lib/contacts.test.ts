import { describe, expect, it } from "vitest";
import { makeAIContact, makeContacts, makeMachineTree, makeTreeNode } from "@/test/fixtures";
import { atlas, bob, notes, sampleContacts } from "@/test/sample-contacts";
import { allAIs, flattenTree, knowsAI, machineLabels, pickables } from "./contacts";

/*
 * The pure functions the contacts page and the pickers share (CONT-01, LAY-03): what can be
 * picked, in which order, and what a search looks in.
 */

describe("pickables (CONT-01, LAY-03)", () => {
  const { ais, users } = pickables(sampleContacts);

  it("lists the user's own AIs with parents before their children, then the unlinked, then the shared", () => {
    expect(ais.map((p) => p.ref.id)).toEqual(["a1", "a2", "a3", "a4", "a5"]);
  });

  it("lists the other users apart, and no people: they are reached through a platform account", () => {
    expect(users.map((p) => p.ref.id)).toEqual(["u2", "u3"]);
    expect(users.every((p) => p.ai === undefined)).toBe(true);
    const everyone = [...ais, ...users].map((p) => p.ref.id);
    expect(everyone).not.toContain("p1");
  });

  it("names an own AI's machine as the owner labelled it, and a shared AI's as its hostname", () => {
    const place = (id: string) => ais.find((p) => p.ref.id === id)?.machineLabel;
    expect(place("a1")).toBe("devbox");
    expect(place("a3")).toBe("Laptop");
    expect(place("a5")).toBe("cloud");
  });

  it("searches an AI by name, handle, hostname, machine label, folder and app", () => {
    const hay = (id: string) => ais.find((p) => p.ref.id === id)?.haystack ?? "";
    for (const found of [
      "Lab",
      "@lab",
      "notebook-01",
      "Laptop",
      "/home/ana/c/d/lab",
      "~/c/d/lab",
    ]) {
      expect(hay("a3")).toContain(found);
    }
    expect(hay("a3")).toContain("Codex");
    expect(hay("a2")).toContain("OpenCode");
    expect(hay("a1")).toContain("Claude Code");
  });

  it("searches a user by name and handle only", () => {
    expect(users[0]?.haystack).toBe("Bob Lima\n@bob");
  });

  it("does not search the owner of a shared AI, who is a different contact", () => {
    expect(ais.find((p) => p.ref.id === "a5")?.haystack).not.toContain("bob");
  });

  it("is empty for a user with no contacts", () => {
    expect(pickables(makeContacts())).toEqual({ ais: [], users: [] });
  });

  it("finds an AI at any depth of the tree", () => {
    const deep = makeContacts({
      machines: [
        makeMachineTree({
          nodes: [
            makeTreeNode({
              ai: makeAIContact({ intelligence: atlas }),
              children: [
                makeTreeNode({
                  ai: makeAIContact({ intelligence: notes }),
                  children: [
                    makeTreeNode({
                      ai: makeAIContact({ intelligence: { ...bob, kind: "ai", role: "ai" } }),
                    }),
                  ],
                }),
              ],
            }),
          ],
        }),
      ],
    });
    expect(pickables(deep).ais.map((p) => p.ref.id)).toEqual(["a1", "a2", "u2"]);
  });
});

describe("machineLabels (TREE-04)", () => {
  it("maps each of the user's hostnames to the label they gave it", () => {
    expect([...machineLabels(sampleContacts)]).toEqual([
      ["devbox", "devbox"],
      ["notebook-01", "Laptop"],
    ]);
  });
});

describe("flattenTree, allAIs and knowsAI", () => {
  it("flattens a tree with parents before their children", () => {
    const tree = sampleContacts.machines[0]?.nodes ?? [];
    expect(flattenTree(tree).map((c) => c.intelligence.id)).toEqual(["a1", "a2"]);
  });

  it("lists every AI of the contacts: own, unlinked and shared", () => {
    expect(allAIs(sampleContacts).map((c) => c.intelligence.id)).toEqual([
      "a1",
      "a2",
      "a3",
      "a4",
      "a5",
    ]);
  });

  it("tells whether the contacts list an AI", () => {
    expect(knowsAI(sampleContacts, "a2")).toBe(true);
    expect(knowsAI(sampleContacts, "a5")).toBe(true);
    expect(knowsAI(sampleContacts, "a9")).toBe(false);
  });
});
