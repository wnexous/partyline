import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "@/shared/api/query-keys";
import type { Contacts, IntelligenceProfile } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeAIContact,
  makeContacts,
  makeIntelligence,
  makeOrgAIs,
  makePerson,
  makeProfile,
} from "@/test/fixtures";
import { useDeleteAgent, useMergeAgent } from "./agents";
import { useContacts } from "./contacts";
import { useIntelligence } from "./profile";

/*
 * The hooks behind the contacts and profile pages (T42 part 2): what each one asks of the server
 * and what it leaves in the cache for the pages to read.
 */

let qc: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);
const stale = (key: readonly unknown[]) => qc.getQueryState(key)?.isInvalidated;

beforeEach(() => {
  qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
});
afterEach(() => {
  qc.clear();
  vi.unstubAllGlobals();
});

describe("contacts (CONT-01)", () => {
  it("loads GET /contacts with every section as the server sent it", async () => {
    const contacts = makeContacts({
      org_ais: [
        makeOrgAIs({
          ais: [
            makeAIContact(),
            makeAIContact({ owner: makeIntelligence({ id: "u2", handle: "bob" }) }),
          ],
        }),
      ],
      hosted: [makeAIContact({ folder: "", folder_label: "" })],
      users: [makeIntelligence({ id: "u2", handle: "bob", display_name: "Bob Lima" })],
      people: [{ connector: { id: "k1", kind: "whatsapp", name: "ops-line" }, count: 3 }],
    });
    const { calls } = mockApi({ "GET /contacts": json(200, contacts) });
    const { result } = renderHook(() => useContacts(), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(["GET /contacts"]);
    expect(result.current.data).toEqual(contacts);
  });

  it("is cached under the contacts key, where the live handlers patch it", async () => {
    mockApi({ "GET /contacts": json(200, makeContacts()) });
    const { result } = renderHook(() => useContacts(), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(qc.getQueryData<Contacts>(queryKeys.contacts)).toEqual(result.current.data);
  });
});

describe("another intelligence's profile (PROF-05, PROF-11, PEOP-03)", () => {
  it("loads a profile by id with what the caller may do with it", async () => {
    const profile: IntelligenceProfile = {
      ...makeProfile(),
      ...makePerson(),
      can_edit: true,
      person: {
        connector: { id: "k1", kind: "whatsapp", name: "ops-line" },
        external_id: "5511999990000",
        name_locked: false,
      },
      apps: [{ kind: "whatsapp", label: "WhatsApp: 5511999990000", connector_id: "k1" }],
      conversations: [{ id: "r1", name: "support" }],
    };
    const { calls } = mockApi({ "GET /intelligences/p1": json(200, profile) });
    const { result } = renderHook(() => useIntelligence("p1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(calls[0]?.path).toBe("/intelligences/p1");
    expect(result.current.data).toEqual(profile);
  });

  it("reports a profile the caller cannot see as a 404", async () => {
    mockApi({ "GET /intelligences/zz": apiError(404, "not_found") });
    const { result } = renderHook(() => useIntelligence("zz"), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toMatchObject({ status: 404 });
  });
});

describe("deleting an AI from the contacts", () => {
  it("sends DELETE, drops the AI from the hosted list and the organization groups, and refreshes the rest", async () => {
    const old = makeAIContact({
      intelligence: makeAI({ id: "a7", display_name: "Old helper", handle: "old-helper" }),
      folder: "",
      folder_label: "",
    });
    const keep = makeAIContact({ intelligence: makeAI({ id: "a8", handle: "keeper" }) });
    const { calls } = mockApi({ "DELETE /agents/a7": new Response(null, { status: 204 }) });
    qc.setQueryData(
      queryKeys.contacts,
      makeContacts({ hosted: [old, keep], org_ais: [makeOrgAIs({ ais: [old] })] }),
    );
    qc.setQueryData(queryKeys.intelligence("a7"), makeProfile({ id: "a7" }));
    const { result } = renderHook(() => useDeleteAgent(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync("a7");
    });

    expect(calls[0]).toMatchObject({ method: "DELETE", path: "/agents/a7" });
    const contacts = qc.getQueryData<Contacts>(queryKeys.contacts);
    expect(contacts?.hosted.map((c) => c.intelligence.id)).toEqual(["a8"]);
    expect(contacts?.org_ais[0]?.ais).toEqual([]);
    // The contacts are the server's to rebuild.
    expect(stale(queryKeys.contacts)).toBe(true);
    // The deleted AI's profile is stale but not refetched into a 404 under the page that shows it.
    expect(stale(queryKeys.intelligence("a7"))).toBe(true);
  });

  it("changes nothing when the server refuses", async () => {
    mockApi({ "DELETE /agents/a7": apiError(403, "forbidden") });
    const contacts = makeContacts({
      hosted: [makeAIContact({ intelligence: makeAI({ id: "a7" }) })],
    });
    qc.setQueryData(queryKeys.contacts, contacts);
    const { result } = renderHook(() => useDeleteAgent(), { wrapper });
    await act(() => result.current.mutateAsync("a7").catch(() => undefined));
    expect(qc.getQueryData(queryKeys.contacts)).toEqual(contacts);
    expect(stale(queryKeys.contacts)).toBe(false);
  });
});

describe("merging an AI (MRG-01, MRG-02)", () => {
  const old = makeAIContact({
    intelligence: makeAI({ id: "a7", display_name: "Old helper", handle: "old-helper" }),
    folder: "",
    folder_label: "",
  });
  const keep = makeAIContact({ intelligence: makeAI({ id: "a8", handle: "atlas" }) });

  it("sends POST with the AI it merges into, drops it from the cached lists and refreshes the contacts", async () => {
    const survivor = makeAgent({ id: "a8", name: "Atlas" });
    const { calls } = mockApi({ "POST /agents/a7/merge": json(200, survivor) });
    qc.setQueryData(
      queryKeys.contacts,
      makeContacts({ hosted: [old, keep], org_ais: [makeOrgAIs({ ais: [old] })] }),
    );
    qc.setQueryData(queryKeys.rooms, []);
    qc.setQueryData(queryKeys.conversations, []);
    qc.setQueryData(queryKeys.intelligence("a7"), makeProfile({ id: "a7" }));
    const { result } = renderHook(() => useMergeAgent(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ from: "a7", into: "a8" });
    });

    expect(calls[0]).toMatchObject({ method: "POST", path: "/agents/a7/merge" });
    expect(calls[0]?.body).toEqual({ into: "a8" });
    const contacts = qc.getQueryData<Contacts>(queryKeys.contacts);
    expect(contacts?.hosted.map((c) => c.intelligence.id)).toEqual(["a8"]);
    expect(contacts?.org_ais[0]?.ais).toEqual([]);
    // The contacts, the conversations and the rooms the merged AI was in are the server's to rebuild.
    expect(stale(queryKeys.contacts)).toBe(true);
    expect(stale(queryKeys.conversations)).toBe(true);
    expect(stale(queryKeys.rooms)).toBe(true);
    // Its profile is stale without a refetch: the row it belonged to is about to leave.
    expect(stale(queryKeys.intelligence("a7"))).toBe(true);
  });

  it("leaves the cached contacts alone when the AI is online (409 identity_online)", async () => {
    mockApi({ "POST /agents/a7/merge": apiError(409, "identity_online") });
    const contacts = makeContacts({ hosted: [old, keep] });
    qc.setQueryData(queryKeys.contacts, contacts);
    const { result } = renderHook(() => useMergeAgent(), { wrapper });
    await act(() => result.current.mutateAsync({ from: "a7", into: "a8" }).catch(() => undefined));
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toMatchObject({ status: 409, code: "identity_online" });
    expect(qc.getQueryData(queryKeys.contacts)).toEqual(contacts);
    expect(stale(queryKeys.contacts)).toBe(false);
  });
});
