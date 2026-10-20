import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { stubPointer } from "@/test/media";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

// UI-24: on phones, focusing a field on mount pops the on-screen keyboard over the form.
// Fields focus themselves only with a fine pointer (mouse, trackpad).

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

const loginApi = () =>
  mockApi({
    "GET /me": json(401, { error: { code: "unauthorized", message: "unauthorized" } }),
    "GET /setup/status": json(200, { needs_setup: false }),
  });

const roomsApi = () =>
  mockApi({ "GET /me": json(200, makeMe()), "GET /rooms": json(200, { items: [] }) });

async function openNewRoom() {
  const user = userEvent.setup();
  await user.click((await screen.findAllByRole("button", { name: "New room" }))[0] as HTMLElement);
  return screen.findByRole("dialog", { name: "New room" });
}

describe("autofocus by pointer (UI-24)", () => {
  it("focuses the login username with a fine pointer", async () => {
    stubPointer("fine");
    loginApi();
    renderRoute("/login");
    const username = await screen.findByLabelText("Username");
    await waitFor(() => expect(username).toHaveFocus());
  });

  it("leaves the login form unfocused on a coarse pointer", async () => {
    stubPointer("coarse");
    loginApi();
    renderRoute("/login");
    const username = await screen.findByLabelText("Username");
    expect(username).not.toHaveFocus();
    expect(screen.getByLabelText("Password")).not.toHaveFocus();
  });

  it("focuses the first dialog field with a fine pointer", async () => {
    stubPointer("fine");
    roomsApi();
    renderRoute("/rooms");
    const dialog = await openNewRoom();
    await waitFor(() => expect(within(dialog).getByLabelText("Name")).toHaveFocus());
  });

  it("focuses the dialog itself, not its first field, on a coarse pointer", async () => {
    stubPointer("coarse");
    roomsApi();
    renderRoute("/rooms");
    const dialog = await openNewRoom();
    await waitFor(() => expect(dialog).toHaveFocus());
    expect(within(dialog).getByLabelText("Name")).not.toHaveFocus();
  });
});
