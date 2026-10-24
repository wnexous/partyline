import { describe, expect, it } from "vitest";
import { makeConversation, makeMe, makeMembership } from "@/test/fixtures";
import {
  canCreateGroupsIn,
  defaultOrgIdOf,
  inOrg,
  isAdminOf,
  type MyOrg,
  resolveCurrentOrg,
  toMyOrgs,
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
  it("come from the memberships of their own profile, in the order the server listed them", () => {
    expect(
      toMyOrgs([
        makeMembership({
          org_id: "o1",
          org_name: "Acme",
          org_slug: "acme",
          permission: "admin",
          position_name: "Admin",
        }),
        makeMembership({
          org_id: "o2",
          org_name: "Globex",
          org_slug: "globex",
          permission: "guest",
        }),
      ]),
    ).toEqual([
      { id: "o1", name: "Acme", slug: "acme", permission: "admin", position: "Admin" },
      { id: "o2", name: "Globex", slug: "globex", permission: "guest" },
    ]);
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
