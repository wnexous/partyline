import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RoomSettingsSheet } from "@/components/room-settings-sheet";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeRoom } from "@/test/fixtures";
import { renderWithRouter } from "@/test/render";

/*
 * Room settings after AD-015 (CID-22): a room is a conversation of intelligences, so there is no
 * chat bridging, no broadcast switch and no catch-all lock. Saving sends only the fields the
 * sheet still owns.
 */

const room = makeRoom({
  id: "r1",
  name: "deploys",
  description: "",
  instructions: "",
  retention_days: 90,
  bridge_chats: true,
  broadcast: false,
  catch_all: true,
});

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("room settings (CID-22)", () => {
  it("offers no chat settings and saves without them", async () => {
    const api = mockApi({
      "PATCH /rooms/r1": json(200, makeRoom({ ...room, name: "deploys-prod" })),
    });
    const user = userEvent.setup();
    renderWithRouter(
      <RoomSettingsSheet room={room} trigger={<button type="button">Room settings</button>} />,
    );

    await user.click(await screen.findByRole("button", { name: "Room settings" }));
    const sheet = await screen.findByRole("dialog", { name: "Room settings" });
    expect(within(sheet).queryAllByRole("switch")).toHaveLength(0);
    expect(within(sheet).queryByText(/Bridge chats/)).toBeNull();
    expect(within(sheet).queryByText(/Send to every chat/)).toBeNull();
    expect(within(sheet).queryByText(/catch-all/i)).toBeNull();

    const name = within(sheet).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "deploys-prod");
    await user.click(within(sheet).getByRole("button", { name: "Save" }));

    const patches = () => api.calls.filter((call) => call.method === "PATCH");
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({
      name: "deploys-prod",
      description: "",
      retention_days: 90,
    });
  });
});
