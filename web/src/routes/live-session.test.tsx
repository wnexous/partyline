import { screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { queryKeys } from "@/shared/api/query-keys";
import { FakeSocket } from "@/test/fake-socket";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeAIContact, makeContacts, makeMe, makeOrgAIs } from "@/test/fixtures";
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
    const bot = makeAI({ display_name: "release-bot", handle: "release-bot" });
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: [] }),
      "GET /contacts": json(
        200,
        makeContacts({
          org_ais: [makeOrgAIs({ ais: [makeAIContact({ intelligence: bot })] })],
        }),
      ),
    });
    const { router, queryClient } = renderRoute("/contacts?tab=mine");
    await screen.findByText("release-bot");
    FakeSocket.latest().open();

    FakeSocket.latest().drop(1008, "session revoked");

    // The close handler clears the cache before it triggers the redirect. Assert it here: the
    // still-mounted page may legitimately refetch during the navigation, which is a UI concern
    // of its own and would make this assertion race (deflake, T28).
    expect(queryClient.getQueryData(queryKeys.contacts)).toBeUndefined();
    expect(queryClient.getQueryData(queryKeys.me)).toBeUndefined();

    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: "/contacts?tab=mine" });
    // No reconnect attempt: the only socket is the one the server closed.
    expect(FakeSocket.instances).toHaveLength(1);
  });
});
