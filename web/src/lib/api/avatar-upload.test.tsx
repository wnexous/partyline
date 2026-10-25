import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mockApi } from "@/test/fetch";
import { AvatarUploadTimeoutMs, useUploadAvatar, useUploadRoomAvatar } from "./queries";

/*
 * A stalled avatar upload must end: the request is abandoned after 30 s, so the profile's photo
 * never stays "sending" with no answer.
 */

function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("avatar upload (PROF-07)", () => {
  it("abandons an upload that does not answer within 30 s", async () => {
    vi.useFakeTimers();
    const api = mockApi({
      "PUT /intelligences/a1/avatar": (_url, init) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("The operation was aborted.", "AbortError")),
          );
        }),
    });
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    const { result } = renderHook(() => useUploadAvatar("a1"), { wrapper: wrapper(client) });

    act(() => result.current.mutate(new Blob(["photo"], { type: "image/png" })));
    // Let the request start, so its abort listener is in place before the clock moves.
    await act(async () => {
      await Promise.resolve();
    });
    expect(api.calls).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(AvatarUploadTimeoutMs);
      await vi.runAllTimersAsync();
    });

    expect(result.current.isError).toBe(true);
    expect((result.current.error as DOMException).name).toBe("AbortError");
  });

  it("abandons the upload of a group's photo that does not answer within 30 s (GAV-06)", async () => {
    vi.useFakeTimers();
    const api = mockApi({
      "PUT /rooms/r1/avatar": (_url, init) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("The operation was aborted.", "AbortError")),
          );
        }),
    });
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    const { result } = renderHook(() => useUploadRoomAvatar("r1"), { wrapper: wrapper(client) });

    act(() => result.current.mutate(new Blob(["photo"], { type: "image/png" })));
    await act(async () => {
      await Promise.resolve();
    });
    expect(api.calls).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(AvatarUploadTimeoutMs);
      await vi.runAllTimersAsync();
    });

    expect(result.current.isError).toBe(true);
    expect((result.current.error as DOMException).name).toBe("AbortError");
  });
});
