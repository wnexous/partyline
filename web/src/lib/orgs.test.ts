import { describe, expect, it } from "vitest";
import { makeConversation, makeMe, makeOrg } from "@/test/fixtures";
import {
  canCreateGroupsIn,
  defaultOrgIdOf,
  inOrg,
  isAdminOf,
  MaxOrgNameLength,
  MaxPositionNameLength,
  type MyOrg,
  resolveCurrentOrg,
  toMyOrgs,
  unreadByOrg,
  unreadOutsideOrg,
} from "./orgs";

/*
 * The viewer's organizations as the web app reads them (ORG-02, OMEM-03, OMEM-04): which one is
 * current, who may create what in it, and which conversations belong under it.
 */

const org = (id: string, permission: string, over: Partial<MyOrg> = {}): MyOrg => ({
  id,
  name: id.toUpperCase(),
  slug: id,
  permission,
  ...over,
});

describe("the viewer's organizations", () => {
  it("come from GET /orgs, in the order the server listed them, with the picture and position they have", () => {
    expect(
      toMyOrgs([
        makeOrg({
          id: "o1",
          name: "Acme",
          slug: "acme",
          permission: "admin",
          position_id: "pos1",
          avatar_url: "/api/v1/orgs/o1/avatar?v=abc",
        }),
        makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "guest" }),
      ]),
    ).toEqual([
      {
        id: "o1",
        name: "Acme",
        slug: "acme",
        permission: "admin",
        positionId: "pos1",
        avatarUrl: "/api/v1/orgs/o1/avatar?v=abc",
      },
      { id: "o2", name: "Globex", slug: "globex", permission: "guest" },
    ]);
  });

  it("keep the limits of the contract: 100 characters for an organization's name, 60 for a position's", () => {
    expect(MaxOrgNameLength).toBe(100);
    expect(MaxPositionNameLength).toBe(60);
  });

  it("let admins and members create groups, and only admins add connections; a guest does neither", () => {
    expect([canCreateGroupsIn(org("a", "admin")), isAdminOf(org("a", "admin"))]).toEqual([
      true,
      true,
    ]);
    expect([canCreateGroupsIn(org("m", "member")), isAdminOf(org("m", "member"))]).toEqual([
      true,
      false,
    ]);
    expect([canCreateGroupsIn(org("g", "guest")), isAdminOf(org("g", "guest"))]).toEqual([
      false,
      false,
    ]);
  });
});

describe("the current organization", () => {
  const orgs = [org("o1", "admin"), org("o2", "member"), org("o3", "guest")];

  it("is the one the viewer picked", () => {
    expect(resolveCurrentOrg(orgs, "o2", "o3")?.id).toBe("o2");
  });

  it("is the account's default when nothing was picked", () => {
    expect(resolveCurrentOrg(orgs, null, "o3")?.id).toBe("o3");
  });

  it("is the first of the viewer's organizations when nothing was picked and no default is known", () => {
    expect(resolveCurrentOrg(orgs, null, undefined)?.id).toBe("o1");
  });

  it("ignores a pick the viewer no longer belongs to (they left it, or another account picked it)", () => {
    expect(resolveCurrentOrg(orgs, "gone", "o3")?.id).toBe("o3");
    expect(resolveCurrentOrg(orgs, "gone", "also-gone")?.id).toBe("o1");
  });

  it("is none for a viewer who belongs to no organization", () => {
    expect(resolveCurrentOrg([], "o1", "o1")).toBeUndefined();
  });
});

describe("the account's default organization", () => {
  it("is what GET /me names, and nothing for a server or an account that names none", () => {
    expect(defaultOrgIdOf(undefined)).toBeUndefined();
    expect(defaultOrgIdOf(makeMe())).toBeUndefined();
    expect(defaultOrgIdOf({ ...makeMe(), default_org_id: "o2" })).toBe("o2");
    expect(defaultOrgIdOf({ ...makeMe(), default_org_id: null })).toBeUndefined();
    expect(defaultOrgIdOf({ ...makeMe(), default_org_id: "" })).toBeUndefined();
  });
});

describe("which conversations an organization holds (ORG-02)", () => {
  it("holds a group of its own and a DM both sides share with it", () => {
    expect(inOrg(makeConversation({ org_ids: ["o1"] }), "o1")).toBe(true);
    expect(inOrg(makeConversation({ kind: "dm", org_ids: ["o2", "o1"] }), "o1")).toBe(true);
  });

  it("does not hold a group of another organization, nor a DM filed only under others", () => {
    expect(inOrg(makeConversation({ org_ids: ["o2"] }), "o1")).toBe(false);
    expect(inOrg(makeConversation({ kind: "dm", org_ids: ["o2", "o3"] }), "o1")).toBe(false);
  });

  it("holds a conversation the server filed under no organization, so it is never unreachable", () => {
    expect(inOrg(makeConversation({ org_ids: [] }), "o1")).toBe(true);
  });
});

describe("the unread messages each organization holds (T39 switcher)", () => {
  const orgs = [{ id: "o1" }, { id: "o2" }, { id: "o3" }];

  it("sums the unread of the conversations an organization holds", () => {
    expect(
      unreadByOrg(
        [
          makeConversation({ id: "r1", org_ids: ["o1"], unread: 2 }),
          makeConversation({ id: "r2", org_ids: ["o1"], unread: 3 }),
          makeConversation({ id: "r3", org_ids: ["o2"], unread: 4 }),
        ],
        orgs,
      ),
    ).toEqual({ o1: 5, o2: 4, o3: 0 });
  });

  it("counts a DM two organizations share in both of them", () => {
    expect(
      unreadByOrg(
        [makeConversation({ id: "d1", kind: "dm", org_ids: ["o1", "o2"], unread: 3 })],
        orgs,
      ),
    ).toEqual({ o1: 3, o2: 3, o3: 0 });
  });

  it("counts a conversation filed under no organization in every one, as the sidebar lists it", () => {
    expect(unreadByOrg([makeConversation({ org_ids: [], unread: 1 })], orgs)).toEqual({
      o1: 1,
      o2: 1,
      o3: 1,
    });
  });

  it("is 0 for every organization when nothing is unread, and has an entry for each one", () => {
    expect(unreadByOrg([], orgs)).toEqual({ o1: 0, o2: 0, o3: 0 });
    expect(unreadByOrg([makeConversation({ unread: 0 })], orgs)).toEqual({ o1: 0, o2: 0, o3: 0 });
  });

  it("ignores the conversations of organizations the viewer is not shown", () => {
    expect(unreadByOrg([makeConversation({ org_ids: ["gone"], unread: 9 })], orgs)).toEqual({
      o1: 0,
      o2: 0,
      o3: 0,
    });
  });
});

describe("the unread messages the current list does not show (T39 switcher)", () => {
  it("sums the conversations the organization does not hold, once each", () => {
    expect(
      unreadOutsideOrg(
        [
          makeConversation({ id: "g1", org_ids: ["o1"], unread: 2 }),
          makeConversation({ id: "g2", org_ids: ["o2"], unread: 5 }),
          // Held by the current organization too: the list shows it.
          makeConversation({ id: "d1", kind: "dm", org_ids: ["o1", "o2"], unread: 3 }),
          // Held by two other organizations: counted once.
          makeConversation({ id: "d2", kind: "dm", org_ids: ["o2", "o3"], unread: 4 }),
        ],
        "o1",
      ),
    ).toBe(9);
  });

  it("is 0 when everything unread is in the current organization, and for a conversation filed under none", () => {
    expect(unreadOutsideOrg([makeConversation({ org_ids: ["o1"], unread: 7 })], "o1")).toBe(0);
    expect(unreadOutsideOrg([makeConversation({ org_ids: [], unread: 7 })], "o1")).toBe(0);
    expect(unreadOutsideOrg([], "o1")).toBe(0);
  });
});
