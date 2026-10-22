import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { queryKeys, useNotifyOptions, useSetNotify } from "./queries";
import type { Me, NotifyOption } from "./types.gen";

/*
 * "Notify me here" (NOTI-01): the chats of the caller's linked accounts that can receive a copy
 * of the DMs AIs write to them, and the choice stored on the account.
 */

let qc: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

const me = makeMe();
const options: NotifyOption[] = [
  {
    chat_id: "c1",
    title: "Lia Costa",
    connector: { id: "k1", kind: "whatsapp", name: "ops-line" },
    account: "+5511999990001",
  },
  {
    chat_id: "c2",
    title: "Atlas",
    connector: { id: "k2", kind: "telegram", name: "ops-bot" },
    account: "@ana",
  },
];

beforeEach(() => {
  qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
});
afterEach(() => {
  qc.clear();
  vi.unstubAllGlobals();
});

describe("the chats that can notify me (NOTI-01)", () => {
  it("loads GET /me/notify-options as the list of chats, under the account's key", async () => {
    const { calls } = mockApi({ "GET /me/notify-options": json(200, { items: options }) });
    const { result } = renderHook(() => useNotifyOptions(), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(["GET /me/notify-options"]);
    expect(result.current.data).toEqual(options);
    expect(qc.getQueryData(queryKeys.notifyOptions)).toEqual(options);
  });

  it("is an empty list, not an error, when no linked account can receive a copy", async () => {
    mockApi({ "GET /me/notify-options": json(200, { items: [] }) });
    const { result } = renderHook(() => useNotifyOptions(), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([]);
  });
});

describe("choosing where to be notified (NOTI-01)", () => {
  it("sends the chat and stores the account it answers with", async () => {
    const chosen = { ...me, notify_chat_id: "c2" };
    const { calls } = mockApi({ "PUT /me/notify": json(200, chosen) });
    qc.setQueryData<Me>(queryKeys.me, me);
    const { result } = renderHook(() => useSetNotify(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync("c2");
    });
    expect(calls[0]).toMatchObject({ method: "PUT", path: "/me/notify" });
    expect(calls[0]?.body).toEqual({ chat_id: "c2" });
    expect(qc.getQueryData<Me>(queryKeys.me)?.notify_chat_id).toBe("c2");
  });

  it("clears the choice with a null chat", async () => {
    const { calls } = mockApi({ "PUT /me/notify": json(200, me) });
    qc.setQueryData<Me>(queryKeys.me, { ...me, notify_chat_id: "c2" });
    const { result } = renderHook(() => useSetNotify(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync(null);
    });
    expect(calls[0]?.body).toEqual({ chat_id: null });
    expect(qc.getQueryData<Me>(queryKeys.me)?.notify_chat_id).toBeUndefined();
  });

  it("keeps the stored choice when the server refuses a chat", async () => {
    mockApi({
      "PUT /me/notify": apiError(422, "invalid", "invalid", { chat_id: "not an option" }),
    });
    const stored = { ...me, notify_chat_id: "c1" };
    qc.setQueryData<Me>(queryKeys.me, stored);
    const { result } = renderHook(() => useSetNotify(), { wrapper });
    await act(() => result.current.mutateAsync("c9").catch(() => undefined));
    // The mutation state settles after the rejected promise, so wait for it.
    await waitFor(() =>
      expect(result.current.error).toMatchObject({ status: 422, code: "invalid" }),
    );
    expect(qc.getQueryData<Me>(queryKeys.me)).toEqual(stored);
  });
});
