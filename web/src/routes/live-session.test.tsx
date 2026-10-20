import { screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "@/lib/api/queries";
import { useLiveStore } from "@/lib/live/store";
import { FakeSocket } from "@/test/fake-socket";
import { json, mockApi } from "@/test/fetch";
import { makeAgent, makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

/*
 * The real live socket inside the app shell: the server closes it with 1008 when the session
 * is revoked (logout elsewhere, password change, account disabled), which means logged out.
 */

beforeEach(() => {
  FakeSocket.instances = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  useLiveStore.setState({ locale: "en" });
});
afterEach(() => vi.unstubAllGlobals());

describe("revoked session on the live socket (UI-02)", () => {
  it("sends the user to /login?redirect=<current route> and drops cached data", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /agents": json(200, { items: [makeAgent({ name: "release-bot" })] }),
    });
    const { router, queryClient } = renderRoute("/agents?tab=mine");
    await screen.findByText("release-bot");
    FakeSocket.latest().open();

    FakeSocket.latest().drop(1008, "session revoked");

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: "/agents?tab=mine" });
    expect(queryClient.getQueryData(queryKeys.agents)).toBeUndefined();
    expect(queryClient.getQueryData(queryKeys.me)).toBeUndefined();
    // No reconnect attempt: the only socket is the one the server closed.
    expect(FakeSocket.instances).toHaveLength(1);
  });
});
