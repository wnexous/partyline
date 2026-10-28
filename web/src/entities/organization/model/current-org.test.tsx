import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe, makeOrg } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { CURRENT_ORG_KEY, type MeWithDefaultOrg } from "../lib/orgs";
import { useCurrentOrg } from "./current-org";

/*
 * The organization the app is about (ORG-02, OMEM-03): the viewer's organizations from their own
 * profile, the one they picked (kept in this browser) and the fallbacks when there is none.
 */

const acme = makeOrg({
  id: "o1",
  name: "Acme",
  slug: "acme",
  permission: "admin",
  position_id: "pos1",
  avatar_url: "/api/v1/orgs/o1/avatar?v=abc",
});
const globex = makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "member" });
const initech = makeOrg({ id: "o3", name: "Initech", slug: "initech", permission: "guest" });

let qc: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

function api(orgs = [acme, globex, initech], me: MeWithDefaultOrg = makeMe()) {
  return mockApi({
    "GET /me": json(200, me),
    "GET /orgs": json(200, { items: orgs }),
  });
}

/** Renders the hook and waits for the organizations to load. */
async function current() {
  const view = renderHook(() => useCurrentOrg(), { wrapper });
  await waitFor(() => expect(view.result.current.loading).toBe(false));
  await waitFor(() => expect(view.result.current.orgs.length).toBeGreaterThan(0));
  return view;
}

beforeEach(() => {
  qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  window.localStorage.clear();
  useLiveStore.setState({ currentOrgId: null });
});
afterEach(() => {
  qc.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("the viewer's organizations", () => {
  it("are those GET /orgs lists, each with the permission, position and picture the viewer has", async () => {
    const { calls } = api();
    const { result } = await current();
    expect(result.current.orgs).toEqual([
      {
        id: "o1",
        name: "Acme",
        slug: "acme",
        permission: "admin",
        positionId: "pos1",
        avatarUrl: "/api/v1/orgs/o1/avatar?v=abc",
      },
      { id: "o2", name: "Globex", slug: "globex", permission: "member" },
      { id: "o3", name: "Initech", slug: "initech", permission: "guest" },
    ]);
    expect(calls.map((c) => `${c.method} ${c.path}`).sort()).toEqual(["GET /me", "GET /orgs"]);
  });

  it("are none, and not loading, when the list can't be read", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": apiError(500, "internal", "boom"),
    });
    const view = renderHook(() => useCurrentOrg(), { wrapper });
    await waitFor(() => expect(qc.getQueryState(["orgs"])?.status).toBe("error"));
    expect(view.result.current.orgs).toEqual([]);
    expect(view.result.current.org).toBeUndefined();
    expect(view.result.current.loading).toBe(false);
  });
});

describe("the current organization", () => {
  it("is the first of the viewer's organizations when nothing was picked and no default is known", async () => {
    api();
    const { result } = await current();
    expect(result.current.org?.id).toBe("o1");
  });

  it("is the account's default when nothing was picked", async () => {
    api([acme, globex, initech], { ...makeMe(), default_org_id: "o2" });
    const { result } = await current();
    expect(result.current.org?.id).toBe("o2");
  });

  it("is the one picked, which also takes over from the default", async () => {
    api([acme, globex, initech], { ...makeMe(), default_org_id: "o2" });
    const { result } = await current();
    act(() => result.current.select("o3"));
    expect(result.current.org?.id).toBe("o3");
  });

  it("survives a remount: the pick is kept in this browser", async () => {
    api();
    const first = await current();
    act(() => first.result.current.select("o2"));
    expect(window.localStorage.getItem(CURRENT_ORG_KEY)).toBe("o2");
    first.unmount();

    const second = await current();
    expect(second.result.current.org?.id).toBe("o2");
  });

  it("falls back to the default when the pick is an organization the viewer left", async () => {
    window.localStorage.setItem(CURRENT_ORG_KEY, "gone");
    useLiveStore.setState({ currentOrgId: "gone" });
    api([acme, globex], { ...makeMe(), default_org_id: "o2" });
    const { result } = await current();
    expect(result.current.org?.id).toBe("o2");
  });

  it("is none for a viewer who belongs to no organization", async () => {
    api([]);
    const view = renderHook(() => useCurrentOrg(), { wrapper });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.org).toBeUndefined();
    expect(view.result.current.orgs).toEqual([]);
  });
});

describe("where the pick is kept", () => {
  it("is read from this browser when the app starts", async () => {
    window.localStorage.setItem(CURRENT_ORG_KEY, "o2");
    vi.resetModules();
    const { useLiveStore: fresh } = await import("@/test/live-store");
    expect(fresh.getState().currentOrgId).toBe("o2");
  });

  it("reads nothing, and so falls back to the default, when the browser refuses storage", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    vi.resetModules();
    const { useLiveStore: fresh } = await import("@/test/live-store");
    expect(fresh.getState().currentOrgId).toBeNull();

    // The hook of this run starts from the same empty pick and lands on the account's default.
    api([acme, globex], { ...makeMe(), default_org_id: "o2" });
    const { result } = await current();
    expect(result.current.org?.id).toBe("o2");
  });

  it("still switches for this tab when the browser refuses to store the pick", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    api([acme, globex]);
    const { result } = await current();
    act(() => result.current.select("o2"));
    expect(result.current.org?.id).toBe("o2");
  });
});
