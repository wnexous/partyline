import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MessagePages } from "@/shared/api/cache-types";
import { queryKeys } from "@/shared/api/query-keys";
import type { Conversation, IntelligenceProfile, Me, Message } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeConversation, makeIntelligence, makeMe, makeMessage } from "@/test/fixtures";
import {
  useIntelligence,
  useRemoveAvatar,
  useUpdateIntelligence,
  useUploadAvatar,
} from "./profile";

/*
 * The hooks of the messenger's data layer: what each one sends, and what it leaves in the cache
 * for the sidebar, the conversation page and the profile pages to read.
 */

let qc: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

beforeEach(() => {
  qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
});
afterEach(() => {
  qc.clear();
  vi.unstubAllGlobals();
});

const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Atlas",
  handle: "atlas",
});
const profileOf = (ref = atlas): IntelligenceProfile => ({
  ...ref,
  bio: "Keeps the deploys honest.",
  can_edit: true,
  apps: [],
  conversations: [],
  memberships: [],
  can_end_session: false,
});

describe("profiles (PROF-03, PROF-05)", () => {
  it("reads GET /intelligences/{id}", async () => {
    mockApi({ "GET /intelligences/a1": json(200, profileOf()) });
    const { result } = renderHook(() => useIntelligence("a1"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toMatchObject({ id: "a1", handle: "atlas", can_edit: true });
  });

  const seedEverywhere = () => {
    qc.setQueryData<Me>(queryKeys.me, makeMe());
    qc.setQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"), profileOf());
    qc.setQueryData<Conversation[]>(queryKeys.conversations, [
      makeConversation({ id: "r2", kind: "dm", title: "Atlas", counterpart: atlas }),
    ]);
    qc.setQueryData<MessagePages>(queryKeys.roomMessages("r1"), {
      pages: [
        {
          items: [
            makeMessage({
              room_id: "r1",
              seq: 1,
              author_kind: "agent",
              author_id: "a1",
              author_name: "Atlas",
              author: atlas,
            }),
          ],
          has_more: false,
        },
      ],
      pageParams: [undefined],
    });
  };
  const timelineAuthor = () =>
    qc.getQueryData<MessagePages>(queryKeys.roomMessages("r1"))?.pages[0]?.items[0] as Message;

  it("patches the name and handle, and shows them in the sidebar and timelines at once", async () => {
    const saved = profileOf({ ...atlas, display_name: "Atlas Prime", handle: "atlas-prime" });
    const { calls } = mockApi({ "PATCH /intelligences/a1": json(200, saved) });
    seedEverywhere();
    const { result } = renderHook(() => useUpdateIntelligence("a1"), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ display_name: "Atlas Prime", handle: "atlas-prime" });
    });
    expect(calls[0]).toMatchObject({
      method: "PATCH",
      path: "/intelligences/a1",
      body: { display_name: "Atlas Prime", handle: "atlas-prime" },
    });
    expect(qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"))).toEqual(saved);
    expect(qc.getQueryData<Conversation[]>(queryKeys.conversations)?.[0]).toMatchObject({
      title: "Atlas Prime",
      counterpart: { handle: "atlas-prime" },
    });
    expect(timelineAuthor()).toMatchObject({
      author_name: "Atlas Prime",
      author: { handle: "atlas-prime" },
    });
  });

  it("updates the account's own data when the user edits their own profile", async () => {
    const mine = profileOf(makeIntelligence({ display_name: "Ana S.", handle: "ana-s" }));
    mockApi({ "PATCH /intelligences/u1": json(200, mine) });
    seedEverywhere();
    const { result } = renderHook(() => useUpdateIntelligence("u1"), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ display_name: "Ana S." });
    });
    expect(qc.getQueryData<Me>(queryKeys.me)).toMatchObject({
      display_name: "Ana S.",
      intelligence: { handle: "ana-s" },
    });
  });

  it("rejects with the field errors and keeps the cache when the handle is taken (PROF-10)", async () => {
    mockApi({
      "PATCH /intelligences/a1": apiError(409, "handle_taken", "handle taken", {
        handle: "taken",
      }),
    });
    seedEverywhere();
    const { result } = renderHook(() => useUpdateIntelligence("a1"), { wrapper });
    const failure = await act(() =>
      result.current.mutateAsync({ handle: "bob" }).catch((e: unknown) => e),
    );
    expect(failure).toMatchObject({
      status: 409,
      code: "handle_taken",
      fields: { handle: "taken" },
    });
    expect(qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"))).toEqual(profileOf());
  });

  it("uploads the image as the raw body with its type and shows the new avatar everywhere (PROF-07)", async () => {
    const withAvatar = profileOf({ ...atlas, avatar_url: "/api/v1/intelligences/a1/avatar?v=9" });
    const { calls } = mockApi({ "PUT /intelligences/a1/avatar": json(200, withAvatar) });
    seedEverywhere();
    const file = new File(["png-bytes"], "me.png", { type: "image/png" });
    const { result } = renderHook(() => useUploadAvatar("a1"), { wrapper });
    await act(async () => {
      await result.current.mutateAsync(file);
    });
    expect(calls[0]).toMatchObject({ method: "PUT", path: "/intelligences/a1/avatar" });
    expect(calls[0]?.body).toBe(file);
    expect(qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"))?.avatar_url).toBe(
      "/api/v1/intelligences/a1/avatar?v=9",
    );
    expect(
      qc.getQueryData<Conversation[]>(queryKeys.conversations)?.[0]?.counterpart?.avatar_url,
    ).toBe("/api/v1/intelligences/a1/avatar?v=9");
    expect(timelineAuthor().author.avatar_url).toBe("/api/v1/intelligences/a1/avatar?v=9");
  });

  it("rejects an invalid image with its code and keeps the old avatar (PROF-06)", async () => {
    mockApi({ "PUT /intelligences/a1/avatar": apiError(422, "avatar_invalid", "not an image") });
    seedEverywhere();
    const { result } = renderHook(() => useUploadAvatar("a1"), { wrapper });
    const failure = await act(() =>
      result.current
        .mutateAsync(new File(["x"], "x.gif", { type: "image/gif" }))
        .catch((e: unknown) => e),
    );
    expect(failure).toMatchObject({ status: 422, code: "avatar_invalid" });
    expect(qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"))).toEqual(profileOf());
  });

  it("removes the avatar and shows the initials everywhere", async () => {
    const withAvatar = profileOf({ ...atlas, avatar_url: "/api/v1/intelligences/a1/avatar?v=9" });
    const { calls } = mockApi({
      "DELETE /intelligences/a1/avatar": new Response(null, { status: 204 }),
      "GET /intelligences/a1": json(200, profileOf()),
    });
    seedEverywhere();
    qc.setQueryData(queryKeys.intelligence("a1"), withAvatar);
    const { result } = renderHook(() => useRemoveAvatar("a1"), { wrapper });
    await act(async () => {
      await result.current.mutateAsync();
    });
    expect(calls[0]).toMatchObject({ method: "DELETE", path: "/intelligences/a1/avatar" });
    expect(
      qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"))?.avatar_url,
    ).toBeUndefined();
    expect(timelineAuthor().author.avatar_url).toBeUndefined();
  });
});
