import { describe, expect, it } from "vitest";
import type { AIContact } from "@/shared/api/types.gen";
import { makeAI, makeAIContact, makeContacts, makeIntelligence, makeOrgAIs } from "@/test/fixtures";
import { atlas, bob, cora, notes, sampleContacts } from "@/test/sample-contacts";
import {
  allAIs,
  byName,
  filterOrgs,
  inviteCandidates,
  knowsAI,
  mergeTargets,
  onlineCount,
  ownAIs,
  pickableList,
  pickables,
  withIntelligence,
  withoutAI,
} from "./contacts";

/*
 * The pure functions the contacts page and the pickers share (CONT-01, LAY-03): what can be
 * picked, in which order, and what a search looks in. The AIs come by organization (AD-016) and
 * no machine is part of them (AD-022).
 */

describe("pickables (CONT-01, LAY-03)", () => {
  const { ais, users } = pickables(sampleContacts);

  it("lists the AIs of the organizations group by group, then the hosted ones", () => {
    expect(ais.map((p) => p.ref.id)).toEqual(["a1", "a2", "a5", "a3", "a4"]);
  });

  it("lists the other users apart, and no people: they are reached through a platform account", () => {
    expect(users.map((p) => p.ref.id)).toEqual(["u2", "u3"]);
    expect(users.every((p) => p.ai === undefined)).toBe(true);
    const everyone = [...ais, ...users].map((p) => p.ref.id);
    expect(everyone).not.toContain("p1");
  });

  it("lists an AI of two organizations once", () => {
    const twice = makeContacts({
      org_ais: [
        makeOrgAIs({ ais: [makeAIContact({ intelligence: atlas })] }),
        makeOrgAIs({
          org: { id: "o2", name: "Globex", slug: "globex" },
          ais: [makeAIContact({ intelligence: atlas })],
        }),
      ],
    });
    expect(pickables(twice).ais.map((p) => p.ref.id)).toEqual(["a1"]);
  });

  it("searches an AI by name, handle, folder and app, and by nothing about a machine", () => {
    const hay = (id: string) => ais.find((p) => p.ref.id === id)?.haystack ?? "";
    for (const found of ["Lab", "@lab", "/home/ana/c/d/lab", "~/c/d/lab"]) {
      expect(hay("a3")).toContain(found);
    }
    expect(hay("a3")).toContain("Codex");
    expect(hay("a2")).toContain("OpenCode");
    expect(hay("a1")).toContain("Claude Code");
    expect(hay("a3")).not.toContain("notebook");
    expect(hay("a1")).not.toContain("devbox");
  });

  it("searches a user by name and handle only", () => {
    expect(users[0]?.haystack).toBe("Bob Lima\n@bob");
  });

  it("does not search the host of an AI, who is a different contact", () => {
    expect(ais.find((p) => p.ref.id === "a5")?.haystack).not.toContain("bob");
  });

  it("is empty for a user with no contacts", () => {
    expect(pickables(makeContacts())).toEqual({ ais: [], users: [] });
  });
});

describe("pickables of an organization (ORG-03, OMEM-04)", () => {
  const ids = (list: { ref: { id: string } }[]) => list.map((p) => p.ref.id);

  it("lists the AIs that are members of that organization and no others", () => {
    expect(ids(pickables(sampleContacts, "o1").ais)).toEqual(["a1", "a2", "a5"]);
    expect(ids(pickables(sampleContacts, "o2").ais)).toEqual(["a3"]);
  });

  it("leaves out the hosted AIs no organization lists: they cannot join a group of one", () => {
    for (const org of ["o1", "o2", "o3"]) {
      expect(ids(pickables(sampleContacts, org).ais)).not.toContain("a4");
    }
  });

  it("lists no AI for an organization the contacts do not group", () => {
    expect(pickables(sampleContacts, "o9").ais).toEqual([]);
    expect(pickables(sampleContacts, "o3").ais).toEqual([]);
  });

  it("keeps every user of the instance: the contacts do not tie them to an organization", () => {
    expect(ids(pickables(sampleContacts, "o2").users)).toEqual(["u2", "u3"]);
  });

  it("describes the AIs the way the unscoped list does", () => {
    const scoped = pickables(sampleContacts, "o1").ais;
    const all = pickables(sampleContacts).ais;
    expect(scoped).toEqual(all.filter((p) => ["a1", "a2", "a5"].includes(p.ref.id)));
  });

  it("is everything the contacts list when no organization is named", () => {
    expect(pickables(sampleContacts, undefined)).toEqual(pickables(sampleContacts));
  });
});

describe("ownAIs and mergeTargets (MRG-01)", () => {
  it("lists the AIs the user hosts, those no organization lists included", () => {
    expect(ownAIs(sampleContacts).map((c) => c.intelligence.id)).toEqual(["a1", "a2", "a3", "a4"]);
  });

  it("offers the user's other AIs of the same home organization, never the one being merged away and never another host's", () => {
    // Atlas and Notes belong to Acme (o1); Lab to Globex (o2); the old helper has no organization left (o3).
    expect(mergeTargets(sampleContacts, "a1").map((r) => r.id)).toEqual(["a2"]);
    expect(mergeTargets(sampleContacts, "a2").map((r) => r.id)).toEqual(["a1"]);
    expect(mergeTargets(sampleContacts, "a3")).toEqual([]);
    expect(mergeTargets(sampleContacts, "a5").map((r) => r.id)).toEqual(["a1", "a2"]);
  });
});

describe("allAIs and knowsAI", () => {
  it("lists every AI of the contacts once: the organizations' and the hosted", () => {
    expect(allAIs(sampleContacts).map((c) => c.intelligence.id)).toEqual([
      "a1",
      "a2",
      "a5",
      "a3",
      "a4",
    ]);
  });

  it("tells whether the contacts list an AI", () => {
    expect(knowsAI(sampleContacts, "a2")).toBe(true);
    expect(knowsAI(sampleContacts, "a5")).toBe(true);
    expect(knowsAI(sampleContacts, "a4")).toBe(true);
    expect(knowsAI(sampleContacts, "a9")).toBe(false);
  });
});

describe("withIntelligence and withoutAI", () => {
  it("renames an AI wherever it is listed, and the host that is it", () => {
    const renamed = { ...atlas, display_name: "Atlas Prime" };
    const twice = makeContacts({
      org_ais: [
        makeOrgAIs({
          ais: [
            makeAIContact({ intelligence: atlas }),
            makeAIContact({ intelligence: bob, owner: atlas }),
          ],
        }),
        makeOrgAIs({
          org: { id: "o2", name: "Globex", slug: "globex" },
          ais: [makeAIContact({ intelligence: atlas })],
        }),
      ],
      hosted: [makeAIContact({ intelligence: atlas })],
    });
    const next = withIntelligence(twice, renamed);
    expect(next.org_ais[0]?.ais[0]?.intelligence.display_name).toBe("Atlas Prime");
    expect(next.org_ais[0]?.ais[1]?.owner?.display_name).toBe("Atlas Prime");
    expect(next.org_ais[1]?.ais[0]?.intelligence.display_name).toBe("Atlas Prime");
    expect(next.hosted[0]?.intelligence.display_name).toBe("Atlas Prime");
  });

  it("keeps the object it was given when nothing changes", () => {
    expect(withIntelligence(sampleContacts, { ...atlas, id: "nobody" })).toBe(sampleContacts);
    expect(withoutAI(sampleContacts, "nobody")).toBe(sampleContacts);
  });

  it("removes an AI from every list that holds it", () => {
    const gone = withoutAI(sampleContacts, "a4");
    expect(gone.hosted).toEqual([]);
    const fromGroups = withoutAI(sampleContacts, "a2");
    expect(fromGroups.org_ais[0]?.ais.map((c) => c.intelligence.id)).toEqual(["a1", "a5"]);
    expect(fromGroups.hosted).toBe(sampleContacts.hosted);
  });
});

describe("filterOrgs (CONT-01)", () => {
  const groups = sampleContacts.org_ais;
  const handles = (list: typeof groups) =>
    list.map((g) => [g.org.name, g.ais.map((c) => c.intelligence.handle)]);

  it("keeps everything for an empty or blank filter", () => {
    expect(filterOrgs(groups, "")).toBe(groups);
    expect(filterOrgs(groups, "   ")).toBe(groups);
  });

  it("keeps the AIs that match and drops the organizations left with none", () => {
    expect(handles(filterOrgs(groups, "notes"))).toEqual([["Acme", ["notes"]]]);
  });

  it("matches the display name, the handle, the folder and the app", () => {
    for (const query of ["Lab", "@lab", "c/d/lab", "~/c/d", "codex"]) {
      expect(handles(filterOrgs(groups, query)), query).toEqual([["Globex", ["lab"]]]);
    }
  });

  it("keeps a whole organization when its name or slug matches", () => {
    expect(handles(filterOrgs(groups, "acme"))).toEqual([["Acme", ["atlas", "notes", "sage"]]]);
    expect(handles(filterOrgs(groups, "GLOBEX"))).toEqual([["Globex", ["lab"]]]);
  });

  it("finds an AI by words that are in its organization and in its own text", () => {
    expect(handles(filterOrgs(groups, "globex lab"))).toEqual([["Globex", ["lab"]]]);
    expect(filterOrgs(groups, "globex notes")).toEqual([]);
  });

  it("ignores case and accents", () => {
    expect(handles(filterOrgs(groups, "ATLAS"))).toEqual([["Acme", ["atlas", "notes"]]]);
    expect(handles(filterOrgs(groups, "LÀB"))).toEqual([["Globex", ["lab"]]]);
  });

  it("returns nothing when nothing matches and does not change what it was given", () => {
    const before = JSON.stringify(groups);
    expect(filterOrgs(groups, "zzzz")).toEqual([]);
    expect(JSON.stringify(groups)).toBe(before);
  });
});

describe("onlineCount", () => {
  it("counts the AIs online now and all of them", () => {
    const ais = sampleContacts.org_ais[0]?.ais ?? [];
    const online = (ai: AIContact) => ai.intelligence.id === "a2";
    expect(onlineCount(ais, online)).toEqual({ online: 1, total: 3 });
    expect(onlineCount(ais, () => true)).toEqual({ online: 3, total: 3 });
    expect(onlineCount(ais, () => false)).toEqual({ online: 0, total: 3 });
  });
});

describe("pickables for the users of an organization (T39)", () => {
  it("lists the users it is given instead of the instance's", () => {
    expect(pickables(sampleContacts, "o1", [cora]).users.map((p) => p.ref.id)).toEqual(["u3"]);
  });

  it("lists none when it is given none, which is not the instance's users", () => {
    expect(pickables(sampleContacts, "o1", []).users).toEqual([]);
  });

  it("lists the instance's users when it is given nothing", () => {
    expect(pickables(sampleContacts, "o1").users.map((p) => p.ref.id)).toEqual(["u2", "u3"]);
  });

  it("does not change which AIs it lists", () => {
    expect(pickables(sampleContacts, "o1", [cora]).ais.map((p) => p.ref.id)).toEqual([
      "a1",
      "a2",
      "a5",
    ]);
  });
});

describe("pickableList (AD-024)", () => {
  const ids = (list: { ref: { id: string } }[]) => list.map((p) => p.ref.id);

  it("lists AIs and users together by display name, not the AIs first and the users after", () => {
    expect(ids(pickableList(sampleContacts))).toEqual(["a1", "u2", "u3", "a3", "a2", "a4", "a5"]);
  });

  it("offers the same people as pickables, scoped the same way, in the one order", () => {
    for (const [org, users] of [
      [undefined, undefined],
      ["o1", undefined],
      ["o2", [cora]],
      ["o1", []],
    ] as const) {
      const split = pickables(sampleContacts, org, users);
      expect(ids(pickableList(sampleContacts, org, users)).sort()).toEqual(
        ids([...split.ais, ...split.users]).sort(),
      );
    }
    expect(ids(pickableList(sampleContacts, "o1", [cora]))).toEqual(["a1", "u3", "a2", "a5"]);
    expect(ids(pickableList(sampleContacts, "o2", []))).toEqual(["a3"]);
  });

  it("keeps what tells an AI from another beside its name, and gives a user nothing extra", () => {
    const list = pickableList(sampleContacts);
    expect(list.find((p) => p.ref.id === "a1")?.ai?.folder_label).toBe("~/atlas");
    expect(list.find((p) => p.ref.id === "u2")?.ai).toBeUndefined();
  });

  it("is empty for a user with no contacts", () => {
    expect(pickableList(makeContacts())).toEqual([]);
  });
});

describe("byName (AD-024)", () => {
  const named = (display_name: string, handle = "x", id = "i") =>
    makeIntelligence({ id, display_name, handle });

  it("orders by display name whatever the case and the accents, and numbers as numbers", () => {
    const sorted = [named("zed"), named("Ágata"), named("agent 10"), named("Agent 2"), named("Bob")]
      .sort(byName)
      .map((r) => r.display_name);
    expect(sorted).toEqual(["Ágata", "Agent 2", "agent 10", "Bob", "zed"]);
  });

  it("tells two identities with one name apart by their handle, an AI as any other", () => {
    const ana = named("Ana Souza", "ana", "u1");
    const ghost = makeAI({ id: "a1", display_name: "Ana Souza", handle: "atlas" });
    expect([ghost, ana].sort(byName).map((r) => r.handle)).toEqual(["ana", "atlas"]);
    expect([ana, ghost].sort(byName).map((r) => r.handle)).toEqual(["ana", "atlas"]);
  });

  it("never depends on the kind: the same names sort the same as AIs and as users", () => {
    const names = ["Mia", "Atlas", "Zoe", "Bob"];
    const asUsers = names.map((n, i) => makeIntelligence({ id: `u${i}`, display_name: n }));
    const asAIs = names.map((n, i) => makeAI({ id: `a${i}`, display_name: n }));
    expect(asUsers.sort(byName).map((r) => r.display_name)).toEqual(
      asAIs.sort(byName).map((r) => r.display_name),
    );
  });
});

describe("who an invitation suggests (OMEM-01)", () => {
  const none = new Set<string>();
  const ids = (query: string, members: ReadonlySet<string> = none) =>
    inviteCandidates(sampleContacts, members, query).map((p) => p.ref.id);

  it("finds users and AIs by name or @handle, users first", () => {
    // "o": Bob Lima, Cora Dias, Notes and Old helper have one; Atlas, Sage and Lab do not.
    expect(ids("o")).toEqual(["u2", "u3", "a2", "a4"]);
    expect(ids("@atl")).toEqual(["a1"]);
    expect(ids("lima")).toEqual(["u2"]);
  });

  it("leaves out the viewer", () => {
    expect(ids("ana")).toEqual([]);
  });

  it("leaves out whoever belongs to the organization already", () => {
    expect(ids("o", new Set(["u2", "a2"]))).toEqual(["u3", "a4"]);
  });

  it("leaves out the one whose handle is exactly what was typed, because it is chosen already", () => {
    expect(ids("bob")).toEqual([]);
    expect(ids("@BOB")).toEqual([]);
    // A longer handle that starts with it is still suggested.
    expect(
      inviteCandidates(
        makeContacts({
          users: [bob, { ...cora, id: "u9", handle: "bobby", display_name: "Bobby" }],
        }),
        none,
        "bob",
      ).map((p) => p.ref.id),
    ).toEqual(["u9"]);
  });

  it("looks at names and handles only, not at the folder or app of an AI", () => {
    // Every AI of the sample lives under /home or /srv; none is named that.
    expect(ids("home")).toEqual([]);
    expect(ids("opencode")).toEqual([]);
  });

  it("suggests nothing for an empty or blank field, and at most five otherwise", () => {
    expect(ids("")).toEqual([]);
    expect(ids("   ")).toEqual([]);
    expect(ids("@")).toEqual([]);
    const many = makeContacts({
      users: Array.from({ length: 8 }, (_, i) => ({
        ...cora,
        id: `x${i}`,
        handle: `person${i}`,
        display_name: `Person ${i}`,
      })),
    });
    expect(inviteCandidates(many, none, "person")).toHaveLength(5);
  });

  it("marks the AIs, so the dialog can show what they are", () => {
    const [first] = inviteCandidates(sampleContacts, none, "note");
    expect(first?.ref.id).toBe(notes.id);
    expect(first?.ai).toBeDefined();
    expect(inviteCandidates(sampleContacts, none, "bo")[0]?.ai).toBeUndefined();
  });
});
