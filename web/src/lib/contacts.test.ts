import { describe, expect, it } from "vitest";
import type { AIContact } from "@/lib/api/types.gen";
import { makeAIContact, makeContacts, makeMachineTree, makeTreeNode } from "@/test/fixtures";
import { atlas, bob, notes, sampleContacts } from "@/test/sample-contacts";
import {
  allAIs,
  filterMachines,
  flattenTree,
  knowsAI,
  machineLabels,
  machineOnlineCount,
  mergeTargets,
  ownAIs,
  pickables,
  sortMachines,
} from "./contacts";

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

describe("ownAIs and mergeTargets (MRG-01)", () => {
  it("lists the user's own AIs, the tree parents before their children and the unlinked last", () => {
    expect(ownAIs(sampleContacts).map((c) => c.intelligence.id)).toEqual(["a1", "a2", "a3", "a4"]);
  });

  it("offers the owner's other AIs, never the one being merged away and never a shared one", () => {
    expect(mergeTargets(sampleContacts, "a1").map((r) => r.id)).toEqual(["a2", "a3", "a4"]);
    expect(mergeTargets(sampleContacts, "a4").map((r) => r.id)).toEqual(["a1", "a2", "a3"]);
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

describe("filterMachines (TREE-05)", () => {
  const machines = sampleContacts.machines;
  const handles = (list: typeof machines) =>
    list.map((m) => [m.hostname, flattenTree(m.nodes).map((c) => c.intelligence.handle)]);

  it("keeps everything for an empty or blank filter", () => {
    expect(filterMachines(machines, "")).toBe(machines);
    expect(filterMachines(machines, "   ")).toBe(machines);
  });

  it("keeps a match with its ancestors and drops everything else", () => {
    // Notes lives inside Atlas's folder: Atlas stays as its parent, the other machine goes.
    expect(handles(filterMachines(machines, "notes"))).toEqual([["devbox", ["atlas", "notes"]]]);
  });

  it("matches the display name, the handle, the folder and the app", () => {
    for (const query of ["Lab", "@lab", "c/d/lab", "~/c/d", "codex"]) {
      expect(handles(filterMachines(machines, query)), query).toEqual([["notebook-01", ["lab"]]]);
    }
  });

  it("keeps the collapsed path of a node that is kept", () => {
    const [kept] = filterMachines(machines, "lab");
    expect(kept?.nodes[0]).toMatchObject({ path: "c/d", name: "lab" });
  });

  it("drops the children of a match that do not match themselves", () => {
    // "atlas" is in Atlas's name and in Notes's folder, so both stay; "claude" only Atlas.
    expect(handles(filterMachines(machines, "atlas"))).toEqual([["devbox", ["atlas", "notes"]]]);
    expect(handles(filterMachines(machines, "claude"))).toEqual([["devbox", ["atlas"]]]);
    expect(filterMachines(machines, "claude")[0]?.nodes[0]?.children).toEqual([]);
  });

  it("keeps a whole machine when its label or its hostname matches", () => {
    expect(handles(filterMachines(machines, "devbox"))).toEqual([["devbox", ["atlas", "notes"]]]);
    // The owner's label counts as well as the hostname (TREE-04).
    expect(handles(filterMachines(machines, "laptop"))).toEqual([["notebook-01", ["lab"]]]);
    expect(handles(filterMachines(machines, "notebook-01"))).toEqual([["notebook-01", ["lab"]]]);
  });

  it("finds a node by words that are in its machine and in its own text", () => {
    expect(handles(filterMachines(machines, "laptop lab"))).toEqual([["notebook-01", ["lab"]]]);
    expect(handles(filterMachines(machines, "devbox notes"))).toEqual([
      ["devbox", ["atlas", "notes"]],
    ]);
    expect(filterMachines(machines, "laptop notes")).toEqual([]);
  });

  it("ignores case and accents", () => {
    expect(handles(filterMachines(machines, "ATLAS"))).toEqual([["devbox", ["atlas", "notes"]]]);
    expect(handles(filterMachines(machines, "LÀB"))).toEqual([["notebook-01", ["lab"]]]);
  });

  it("returns nothing when nothing matches", () => {
    expect(filterMachines(machines, "zzzz")).toEqual([]);
  });

  it("does not change what it was given", () => {
    const before = JSON.stringify(machines);
    filterMachines(machines, "notes");
    expect(JSON.stringify(machines)).toBe(before);
  });
});

describe("sortMachines (TREE-03)", () => {
  const machine = (hostname: string, label: string, ids: string[]) =>
    makeMachineTree({
      hostname,
      label,
      nodes: ids.map((id) =>
        makeTreeNode({ ai: makeAIContact({ intelligence: { ...atlas, id }, machine: hostname }) }),
      ),
    });
  const zeta = machine("h1", "Zeta", ["z1"]);
  const alpha = machine("h2", "alpha", ["a1"]);
  const mid = machine("h3", "Mid", ["m1"]);
  const none = (_ai: AIContact) => false;
  const only = (ids: string[]) => (ai: AIContact) => ids.includes(ai.intelligence.id);
  const labels = (list: ReturnType<typeof sortMachines>) => list.map((m) => m.label);

  it("lists machines by label, ignoring case", () => {
    expect(labels(sortMachines([zeta, mid, alpha], none))).toEqual(["alpha", "Mid", "Zeta"]);
  });

  it("puts machines with an online AI first, then the rest by label", () => {
    expect(labels(sortMachines([alpha, mid, zeta], only(["z1"])))).toEqual([
      "Zeta",
      "alpha",
      "Mid",
    ]);
    expect(labels(sortMachines([alpha, mid, zeta], only(["z1", "m1"])))).toEqual([
      "Mid",
      "Zeta",
      "alpha",
    ]);
  });

  it("goes by who is online now, not by the flag the server sent", () => {
    const stale = { ...alpha, online: true };
    expect(labels(sortMachines([stale, zeta], only(["z1"])))).toEqual(["Zeta", "alpha"]);
  });

  it("finds an online AI at any depth of the tree", () => {
    const deep = makeMachineTree({
      hostname: "h4",
      label: "Deep",
      nodes: [
        makeTreeNode({
          ai: makeAIContact({ intelligence: { ...atlas, id: "d1" } }),
          children: [makeTreeNode({ ai: makeAIContact({ intelligence: { ...atlas, id: "d2" } }) })],
        }),
      ],
    });
    expect(labels(sortMachines([alpha, deep], only(["d2"])))).toEqual(["Deep", "alpha"]);
  });

  it("falls back to the hostname for a machine with no label and does not change its input", () => {
    const bare = makeMachineTree({ hostname: "box", label: "", nodes: [] });
    const input = [zeta, bare];
    expect(sortMachines(input, none).map((m) => m.hostname)).toEqual(["box", "h1"]);
    expect(input).toEqual([zeta, bare]);
  });
});

describe("machineOnlineCount", () => {
  it("counts the AIs online now, at any depth, and all of them", () => {
    const tree = sampleContacts.machines[0];
    if (!tree) throw new Error("fixture has no machine");
    const online = (ai: AIContact) => ai.intelligence.id === "a2";
    expect(machineOnlineCount(tree, online)).toEqual({ online: 1, total: 2 });
    expect(machineOnlineCount(tree, () => true)).toEqual({ online: 2, total: 2 });
    expect(machineOnlineCount(tree, () => false)).toEqual({ online: 0, total: 2 });
  });
});
