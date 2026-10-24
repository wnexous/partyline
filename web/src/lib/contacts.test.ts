import { describe, expect, it } from "vitest";
import type { AIContact } from "@/lib/api/types.gen";
import { makeAIContact, makeContacts, makeOrgAIs } from "@/test/fixtures";
import { atlas, bob, sampleContacts } from "@/test/sample-contacts";
import {
  allAIs,
  filterOrgs,
  knowsAI,
  mergeTargets,
  onlineCount,
  ownAIs,
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
